package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
)

// Wire behavior follows ST U-Boot d1bbf3f7edb786fe854034810355e750b08a9851.
// In particular, status integers are little endian, commands big endian, and
// only manifestation/true upload EOF clean the shared backend transaction.
type phaseStatus struct {
	phase           uint8
	address, offset uint32
	extra           []byte
}

func parsePhaseResponse(data []byte, requestSize int) (phaseStatus, error) {
	// ST stm32prog_cmd_read's PHASE_MIN_SIZE: one phase byte plus two little-endian uint32 values.
	if len(data) < 9 || len(data) >= requestSize {
		return phaseStatus{}, errors.New("invalid or nonterminal GetPhase response")
	}
	status := phaseStatus{phase: data[0], address: binary.LittleEndian.Uint32(data[1:5]), offset: binary.LittleEndian.Uint32(data[5:9]), extra: append([]byte(nil), data[9:]...)}
	if status.phase == 0xff {
		return status, fmt.Errorf("target phase FF: %s; restart the programming service", strings.TrimRight(string(status.extra), "\x00"))
	}
	if status.phase == 0 && len(status.extra) == 0 {
		return status, errors.New("phase zero response lacks stage information")
	}
	return status, nil
}

// Each extent reads from its own file and writes at a partition-relative offset.
type extent struct {
	offset, length int64
	source         io.ReaderAt
	sourceOffset   int64
	fileName       string
	fileSize       int64
}

type payload struct {
	part    partition
	size    int64
	extents []extent
}

func paddedSize(size int64) (int64, error) {
	// MMC sectors are 512 bytes; 511 is the rounding mask and maximum added padding.
	if size <= 0 || size > math.MaxInt64-511 {
		return 0, errors.New("empty or oversized payload")
	}
	return (size + 511) &^ 511, nil
}

func validatePayload(item payload) error {
	total, err := paddedSize(item.size)
	if err != nil {
		return err
	}
	if bound := item.part.capacity; bound > 0 && total > bound {
		return fmt.Errorf("%s: sector-rounded payload %d exceeds partition %d", item.part.Name, total, bound)
	}
	if len(item.extents) == 0 {
		return errors.New("payload has no extents")
	}
	end := int64(0)
	for _, x := range item.extents {
		// MMC writes are sector aligned; SetPhase encodes the partition-relative byte offset as uint32.
		if x.source == nil || x.length <= 0 || x.length%512 != 0 || x.sourceOffset < 0 || x.sourceOffset > math.MaxInt64-x.length || x.offset < end || x.offset > math.MaxUint32 || x.offset%512 != 0 || x.offset > total || x.length > total-x.offset {
			return fmt.Errorf("%s: invalid write extent %+v", item.part.Name, x)
		}
		end = x.offset + x.length
	}
	return nil
}

// paddedImage supplies deterministic bytes through the final MMC sector.
// It does not wrap layout, virtual commands, or bootstrap RAM downloads.
type paddedImage struct {
	source      io.ReaderAt
	size, total int64
}

func (p paddedImage) ReadAt(dst []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, errors.New("negative image offset")
	}
	if offset >= p.total {
		return 0, io.EOF
	}
	n := len(dst)
	if int64(n) > p.total-offset {
		n = int(p.total - offset)
	}
	sourceBytes := int64(n)
	if sourceBytes > p.size-offset {
		sourceBytes = max(int64(0), p.size-offset)
	}
	if sourceBytes > 0 {
		count, err := p.source.ReadAt(dst[:int(sourceBytes)], offset)
		if err != nil && !(err == io.EOF && count == int(sourceBytes)) {
			return count, err
		}
		if count != int(sourceBytes) {
			return count, io.ErrUnexpectedEOF
		}
	}
	clear(dst[int(sourceBytes):n])
	if n < len(dst) {
		return n, io.EOF
	}
	return n, nil
}

type stm32progClient struct {
	session    *usbSession
	skipVerify bool
}

func (c *stm32progClient) readPhase(ctx context.Context) (phaseStatus, error) {
	alt, ok := c.session.descriptor.phases[0xf1]
	if !ok {
		return phaseStatus{}, errors.New("virtual phase F1 is missing")
	}
	// ST stm32prog.h defines CMD_SIZE as 512 bytes for the virtual command area, not an MMC sector.
	data, err := c.session.dfu.uploadShort(ctx, alt.alt, 512)
	if err != nil {
		return phaseStatus{}, err
	}
	status, err := parsePhaseResponse(data, 512)
	if err != nil {
		return status, c.session.dfu.fail(err)
	}
	return status, nil
}

func (c *stm32progClient) requireFresh(ctx context.Context) error {
	status, err := c.readPhase(ctx)
	if err != nil {
		return err
	}
	if status.phase != 0 || len(status.extra) != 1 || status.extra[0] != 0 {
		return errors.New("service already contains a layout or interrupted session; restart stm32prog (no implicit resume)")
	}
	return nil
}

func (c *stm32progClient) setPhase(ctx context.Context, phase uint8, offset uint32) error {
	if phase == 0 || phase > 0xf0 {
		return fmt.Errorf("invalid storage phase %02x", phase)
	}
	// ST stm32prog_cmd_write expects one phase byte and a four-byte big-endian byte offset.
	var command [5]byte
	command[0] = phase
	binary.BigEndian.PutUint32(command[1:], offset)
	return c.session.dfu.download(ctx, c.session.descriptor.phases[0xf1].alt, bytes.NewReader(command[:]), 5)
}

func (c *stm32progClient) expectPhase(ctx context.Context, want uint8) error {
	status, err := c.readPhase(ctx)
	if err != nil {
		return err
	}
	if status.phase != want {
		return c.session.dfu.fail(fmt.Errorf("target phase %02x, expected %02x; restart stm32prog", status.phase, want))
	}
	return nil
}

func (c *stm32progClient) initialize(ctx context.Context, plan flashPlan) error {
	wire, err := plan.wireLayout()
	if err != nil {
		return err
	}
	if err := c.requireFresh(ctx); err != nil {
		return err
	}
	for _, p := range plan.partitions {
		if p.writes() {
			logInfo("Write phase %02X: %s", p.Phase, p.Name)
		}
		if p.erases() {
			logInfo("Erase phase %02X: %s", p.Phase, p.Name)
		}
	}
	partial := false
	for _, p := range plan.partitions {
		partial = partial || p.operation == "-"
	}
	if partial {
		logInfo("Partial update: target must validate the existing GPT geometry")
	} else {
		logInfo("Full update: target may recreate GPT")
	}
	if err := c.session.dfu.download(ctx, c.session.descriptor.phases[0].alt, bytes.NewReader(wire), int64(len(wire))); err != nil {
		return fmt.Errorf("layout download: %w", err)
	}
	status, err := c.readPhase(ctx)
	if err != nil {
		return err
	}
	if status.phase != 0 || len(status.extra) != 1 || status.extra[0] != 1 {
		return errors.New("target did not parse FlashLayout")
	}
	// Layout initialization is detach-only. A bus reset here would reboot.
	if err := c.session.dfu.detach(ctx, 0); err != nil {
		return fmt.Errorf("layout detach: %w", err)
	}
	if err := c.session.reconnectFn(ctx, stageUBoot, plan.expectedAlternates()); err != nil {
		return err
	}
	if err := c.session.startDFU(ctx, false); err != nil {
		return err
	}
	if err := c.expectPhase(ctx, plan.nextPhase(0)); err != nil {
		return fmt.Errorf("media/GPT initialization: %w", err)
	}
	return nil
}

func (c *stm32progClient) checkCapacity(item payload) error {
	alt, ok := c.session.descriptor.phases[item.part.Phase]
	if !ok || alt.name != item.part.Name || alt.permissions != 'e' {
		return fmt.Errorf("missing writable alternate for %s", item.part.Name)
	}
	total, err := paddedSize(item.size)
	if err != nil {
		return err
	}
	if exact := item.part.capacity; exact > 0 {
		// The display truncates to whole MiB (KiB for <=1 MiB).
		unit := int64(1)
		if exact > 1024*1024 {
			unit = 1024 * 1024
		} else if exact > 1024 {
			unit = 1024
		}
		if alt.capacity != exact/unit*unit {
			return fmt.Errorf("%s: descriptor capacity %d disagrees with fixed layout %d", item.part.Name, alt.capacity, exact)
		}
	} else if total > alt.capacity {
		return fmt.Errorf("%s: payload %d exceeds advertised capacity lower bound %d", item.part.Name, total, alt.capacity)
	}
	return nil
}

func (c *stm32progClient) writePayload(ctx context.Context, plan flashPlan, item payload) (downloaded, readBack int64, err error) {
	if err = validatePayload(item); err != nil {
		return
	}
	if err = c.checkCapacity(item); err != nil {
		return
	}
	next := plan.nextPhase(item.part.Phase)
	alt := c.session.descriptor.phases[item.part.Phase].alt
	for _, x := range item.extents {
		if err = c.setPhase(ctx, item.part.Phase, uint32(x.offset)); err != nil {
			return
		}
		if err = c.session.dfu.download(ctx, alt, io.NewSectionReader(x.source, x.sourceOffset, x.length), x.length); err != nil {
			return
		}
		downloaded += x.length
		if err = c.expectPhase(ctx, next); err != nil {
			return
		}
	}
	if c.skipVerify {
		return
	}
	// Do not SetPhase for readback. Automatic advancement is what prevents
	// dfu_initiated_callback from overriding the read's initial offset zero.
	expected := make([]byte, len(c.session.buffer))
	readBack, err = c.session.dfu.uploadToEOF(ctx, alt, func(offset int64, data []byte) error {
		for _, x := range item.extents {
			start, end := max(offset, x.offset), min(offset+int64(len(data)), x.offset+x.length)
			if start >= end {
				continue
			}
			n := int(end - start)
			count, err := x.source.ReadAt(expected[:n], x.sourceOffset+start-x.offset)
			if err != nil {
				return err
			}
			if count != n {
				return io.ErrUnexpectedEOF
			}
			if !bytes.Equal(expected[:n], data[int(start-offset):int(end-offset)]) {
				return fmt.Errorf("verification mismatch in %s at offset 0x%x", item.part.Name, start)
			}
		}
		return nil
	})
	if err != nil {
		return
	}
	last := item.extents[len(item.extents)-1]
	if readBack < last.offset+last.length {
		err = fmt.Errorf("%s: premature readback EOF at %d", item.part.Name, readBack)
		return
	}
	if exact := item.part.capacity; exact > 0 && readBack != exact {
		err = fmt.Errorf("%s: readback capacity %d, expected %d", item.part.Name, readBack, exact)
		return
	}
	err = c.expectPhase(ctx, next)
	return
}

func (c *stm32progClient) program(ctx context.Context, plan flashPlan, images []payload) error {
	// Validate the complete input set before layout initialization can erase MMC.
	if _, err := plan.wireLayout(); err != nil {
		return err
	}
	byPhase := make(map[uint8]payload, len(images))
	for _, image := range images {
		if _, exists := byPhase[image.part.Phase]; exists {
			return errors.New("duplicate planned payload")
		}
		if err := validatePayload(image); err != nil {
			return err
		}
		byPhase[image.part.Phase] = image
	}
	count := 0
	for _, p := range plan.partitions {
		if !p.writes() {
			continue
		}
		item, exists := byPhase[p.Phase]
		if !exists || item.part != p {
			return fmt.Errorf("missing or inconsistent payload for %s", p.Name)
		}
		count++
	}
	if count != len(images) {
		return errors.New("unselected payload in execution plan")
	}
	if c.skipVerify {
		logInfo("Warning: read-back verification disabled (--verify=false); stored contents will not be compared.")
	}
	if err := c.initialize(ctx, plan); err != nil {
		return err
	}
	// Check all newly enumerated capacities before the first payload write.
	for _, item := range images {
		if err := c.checkCapacity(item); err != nil {
			return err
		}
	}
	completed := make(map[uint8]bool, len(images))
	for _, p := range plan.partitions {
		if !p.writes() {
			continue
		}
		item := byPhase[p.Phase]
		start := time.Now()
		downloaded, readBack, err := c.writePayload(ctx, plan, item)
		if err != nil {
			return fmt.Errorf("phase %02X (%s): %w; restart the programming service before retrying", p.Phase, p.Name, err)
		}
		completed[p.Phase] = true
		total, _ := paddedSize(item.size)
		if c.skipVerify {
			logInfo("Programmed %s: downloaded %d, skipped %d bytes; read-back verification disabled (%s)", p.Name, downloaded, total-downloaded, time.Since(start).Round(time.Millisecond))
		} else {
			logInfo("Verified %s: downloaded %d, skipped %d, read back %d bytes (%s)", p.Name, downloaded, total-downloaded, readBack, time.Since(start).Round(time.Millisecond))
		}
		if downloaded != total {
			logInfo("%s: omitted gap was not erased", p.Name)
		}
	}
	if len(completed) != count {
		return errors.New("incomplete host write/verification ledger")
	}
	if err := c.expectPhase(ctx, 0xfe); err != nil {
		return err
	}
	if err := c.session.dfu.detach(ctx, 0); err != nil {
		return fmt.Errorf("final detach: %w", err)
	}
	// At FE, DETACH completes stm32prog and unregisters the gadget. A bus
	// reset races that shutdown and can wait for a device that will not return.
	// ROM/TF-A bootstrap resets are separate and remain required.
	if c.skipVerify {
		logSuccess("Programming complete; read-back verification disabled. DFU service detached (OS boot not observed).")
	} else {
		logSuccess("Programming and read-back verification complete. DFU service detached (OS boot not observed).")
	}
	return nil
}

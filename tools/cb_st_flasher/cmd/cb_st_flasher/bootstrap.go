package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	bootstrapROMCapacity  = 128 * 1024
	bootstrapFIPCapacity  = 16 * 1024 * 1024
	bootstrapPhaseRequest = 255
)

type bootstrapImage struct {
	key   string
	phase uint8
	file  *os.File
	size  int64
}

// Validate framing, not authenticity: only the ROM and TF-A can authenticate
// against the board's keys. In particular, never strip or rebuild extensions.
func validateBootstrapFile(file *os.File, key string) (int64, error) {
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	limit := int64(bootstrapFIPCapacity)
	if key == "tfa_usb" {
		limit = bootstrapROMCapacity
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return 0, fmt.Errorf("%s must be a nonempty regular file no larger than %d bytes", key, limit)
	}
	if key == "tfa_usb" {
		var header [512]byte
		if _, err := file.ReadAt(header[:], 0); err != nil {
			return 0, fmt.Errorf("read complete STM32MP13 header: %w", err)
		}
		if string(header[:4]) != "STM2" || binary.LittleEndian.Uint32(header[72:76]) != 0x00020000 {
			return 0, errors.New("USB TF-A requires STM2 magic and an STM32MP13 v2.0 header")
		}
		length := int64(binary.LittleEndian.Uint32(header[76:80]))
		if binary.LittleEndian.Uint32(header[104:108]) != 384 || length == 0 || length+512 != info.Size() {
			return 0, errors.New("USB TF-A requires its complete 512-byte v2 header/extensions and matching nonzero image length")
		}
	} else {
		var header [16]byte
		if _, err := file.ReadAt(header[:], 0); err != nil {
			return 0, fmt.Errorf("read FIP ToC header: %w", err)
		}
		if binary.LittleEndian.Uint32(header[:4]) != 0xaa640001 || binary.LittleEndian.Uint32(header[4:8]) == 0 || info.Size() <= int64(len(header)) {
			return 0, errors.New("FIP requires a raw ToC header with magic aa640001, nonzero serial, and payload")
		}
	}
	return info.Size(), nil
}

func openBootstrapImages(stage bootStage, images map[string]string) ([]bootstrapImage, error) {
	var required []bootstrapImage
	switch stage {
	case stageROM:
		required = []bootstrapImage{{key: "tfa_usb", phase: 1}, {key: "fip_optee", phase: 3}}
	case stageTFA:
		required = []bootstrapImage{{key: "fip_optee", phase: 3}}
	case stageUBoot:
		return nil, nil
	default:
		return nil, errors.New("unsupported bootstrap stage")
	}
	for i := range required {
		image := &required[i]
		path := images[image.key]
		var err error
		if path == "" {
			err = fmt.Errorf("missing required bootstrap artifact %s", image.key)
		} else {
			image.file, err = os.Open(path)
			if err == nil {
				image.size, err = validateBootstrapFile(image.file, image.key)
			}
		}
		if err != nil {
			return nil, errors.Join(fmt.Errorf("%s: %w", image.key, err), closeBootstrapImages(required))
		}
	}
	return required, nil
}

func closeBootstrapImages(images []bootstrapImage) error {
	var result error
	for _, image := range images {
		if image.file != nil {
			result = errors.Join(result, image.file.Close())
		}
	}
	return result
}

func validateBootstrapImages(stage bootStage, images map[string]string) error {
	opened, err := openBootstrapImages(stage, images)
	if err != nil {
		return err
	}
	return closeBootstrapImages(opened)
}

func readBootstrapPhase(ctx context.Context, session *usbSession) (phaseStatus, error) {
	virtual, ok := session.descriptor.phases[0xf1]
	if !ok {
		return phaseStatus{}, errors.New("bootstrap virtual F1 alternate is missing")
	}
	data, err := session.dfu.uploadShort(ctx, virtual.alt, bootstrapPhaseRequest)
	if err != nil {
		return phaseStatus{}, err
	}
	status, err := parsePhaseResponse(data, bootstrapPhaseRequest)
	if err != nil {
		return phaseStatus{}, session.dfu.fail(err)
	}
	return status, nil
}

func bootstrap(ctx context.Context, session *usbSession, stage bootStage, images map[string]string) (result error) {
	// Keep these validated handles open across both transitions. A missing or
	// malformed FIP must be detected before even the first ROM download.
	opened, err := openBootstrapImages(stage, images)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, closeBootstrapImages(opened))
		if result != nil {
			result = errors.Join(fmt.Errorf("bootstrap at %s: %w; restart from a known ROM/service entry", session.stage, result), session.Close())
		}
	}()
	if session.stage != stage {
		return fmt.Errorf("expected initial %s, found %s", stage, session.stage)
	}
	if err := session.startDFU(ctx, stage == stageUBoot); err != nil {
		return err
	}
	if stage != stageUBoot {
		logInfo("Warning: bootstrap requires approved signed loader/FIP versions; firmware authentication can advance rollback OTP state.")
	}
	for _, image := range opened {
		status, err := readBootstrapPhase(ctx, session)
		if err != nil {
			return err
		}
		if status.phase != image.phase || status.offset != 0 {
			return fmt.Errorf("expected fresh bootstrap phase %02x, got phase %02x offset %08x", image.phase, status.phase, status.offset)
		}
		alternate, ok := session.descriptor.phases[image.phase]
		if !ok || alternate.capacity < image.size {
			return fmt.Errorf("bootstrap phase %02x is missing or too small for %s", image.phase, image.key)
		}
		logInfo("Loading %s through %s (%d bytes)", image.key, session.stage, image.size)
		if err := session.dfu.download(ctx, alternate.alt, io.NewSectionReader(image.file, 0, image.size), image.size); err != nil {
			return fmt.Errorf("download phase %02x: %w", image.phase, err)
		}
		status, err = readBootstrapPhase(ctx, session)
		if err != nil {
			return err
		}
		if status.phase != 0 || len(status.extra) != 1 || status.extra[0] != 1 {
			return fmt.Errorf("phase %02x did not request bootstrap detach", image.phase)
		}
		if session.stage == stageTFA && (status.address != 0xffffffff || status.offset != 0) {
			return errors.New("TF-A returned an unexpected bootstrap detach response")
		}
		next := stageTFA
		if session.stage == stageTFA {
			next = stageUBoot
		}
		if err := session.dfu.detach(ctx, 1000); err != nil && !isUSBDisconnected(err) {
			return fmt.Errorf("bootstrap detach: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := session.transport.Reset(); err != nil && !isUSBResetReenumeration(err) {
			return fmt.Errorf("bootstrap USB reset: %w", err)
		}
		if err := session.reconnectFn(ctx, next, nil); err != nil {
			return err
		}
		if session.stage != next {
			return fmt.Errorf("expected bootstrap arrival %s, found %s", next, session.stage)
		}
		if err := session.startDFU(ctx, next == stageUBoot); err != nil {
			return err
		}
	}
	return (&stm32progClient{session: session}).requireFresh(ctx)
}

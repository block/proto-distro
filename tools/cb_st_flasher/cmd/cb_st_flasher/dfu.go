package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/gousb"
)

const (
	dfuDetach      = 0
	dfuDownload    = 1
	dfuUpload      = 2
	dfuGetStatus   = 3
	dfuClearStatus = 4

	dfuIdle         = 2
	dfuDownloadSync = 3
	dfuDownloadBusy = 4
	dfuDownloadIdle = 5
	dfuManifestSync = 6
	dfuManifest     = 7
	dfuUploadIdle   = 9
	dfuError        = 10

	dfuStatusTimeout = 120 * time.Second
)

type dfuStatus struct {
	status      byte
	delay       time.Duration
	state       byte
	stringIndex byte
}

// One client belongs to one enumeration. A failure may leave the firmware's
// shared backend live; neither CLRSTATUS nor ABORT safely repairs that state.
// The owning session must close on error, and recovery requires re-entry into
// the programming service rather than replaying an uncertain USB request.
type dfuClient struct {
	transport       usbTransport
	interfaceNumber uint16
	buffer          []byte
	statusBuffer    [6]byte
	failure         error
	idle            bool
}

func (s *usbSession) startDFU(ctx context.Context, recoverInitialError bool) error {
	if s.dfu != nil {
		return errors.New("DFU client already started for this USB connection")
	}
	if s.transport == nil || s.descriptor.interfaceNumber < 0 || s.descriptor.interfaceNumber > 255 ||
		s.descriptor.transferSize < 1 || s.descriptor.transferSize > 4096 || len(s.buffer) != s.descriptor.transferSize {
		return errors.New("invalid USB connection for DFU")
	}
	d := &dfuClient{transport: s.transport, interfaceNumber: uint16(s.descriptor.interfaceNumber), buffer: s.buffer}
	s.dfu = d
	status, err := d.getStatus(ctx)
	if err != nil {
		return err
	}
	if status.state == dfuError && recoverInitialError {
		if _, err := d.control(ctx, 0x21, dfuClearStatus, 0, nil); err != nil {
			return err
		}
		status, err = d.getStatus(ctx)
		if err != nil {
			return err
		}
	}
	if status.status != 0 || status.state != dfuIdle {
		return d.fail(fmt.Errorf("initial DFU state %d, status 0x%02x, string %d: restart the programming service", status.state, status.status, status.stringIndex))
	}
	d.idle = true
	return nil
}

func (d *dfuClient) fail(err error) error {
	if d.failure == nil {
		d.failure = err
	}
	d.idle = false
	return err
}

func (d *dfuClient) ready(ctx context.Context) error {
	if d.failure != nil {
		return fmt.Errorf("DFU connection unusable; restart the programming service: %w", d.failure)
	}
	if err := ctx.Err(); err != nil {
		return d.fail(err)
	}
	return nil
}

func (d *dfuClient) control(ctx context.Context, direction, request uint8, value uint16, data []byte) (int, error) {
	if err := d.ready(ctx); err != nil {
		return 0, err
	}
	n, err := d.transport.Control(direction, request, value, d.interfaceNumber, data)
	if err != nil {
		return n, d.fail(fmt.Errorf("DFU request %d block/value %d: %w", request, value, err))
	}
	if n < 0 || n > len(data) || (direction == 0x21 && n != len(data)) {
		return n, d.fail(fmt.Errorf("DFU request %d transferred %d of %d bytes", request, n, len(data)))
	}
	if err := ctx.Err(); err != nil {
		return n, d.fail(err)
	}
	return n, nil
}

func (d *dfuClient) getStatus(ctx context.Context) (dfuStatus, error) {
	n, err := d.control(ctx, 0xa1, dfuGetStatus, 0, d.statusBuffer[:])
	if err != nil {
		return dfuStatus{}, err
	}
	if n != len(d.statusBuffer) {
		return dfuStatus{}, d.fail(fmt.Errorf("DFU GETSTATUS returned %d bytes, expected 6", n))
	}
	b := d.statusBuffer
	return dfuStatus{status: b[0], delay: time.Duration(uint32(b[1])|uint32(b[2])<<8|uint32(b[3])<<16) * time.Millisecond, state: b[4], stringIndex: b[5]}, nil
}

func (d *dfuClient) begin(ctx context.Context, alt int) error {
	if err := d.ready(ctx); err != nil {
		return err
	}
	if !d.idle {
		return d.fail(errors.New("DFU transaction requires a clean idle connection"))
	}
	if err := d.transport.SetAlt(alt); err != nil {
		return d.fail(fmt.Errorf("select DFU alternate %d: %w", alt, err))
	}
	d.idle = false
	return d.ready(ctx)
}

// Each completed block makes progress and gets a new deadline. Large images
// have no total time limit; a stuck status/manifestation sequence does.
func (d *dfuClient) poll(ctx context.Context, manifest bool) error {
	deadline := time.Now().Add(dfuStatusTimeout)
	for {
		if !time.Now().Before(deadline) {
			return d.fail(fmt.Errorf("DFU status wait: %w", context.DeadlineExceeded))
		}
		status, err := d.getStatus(ctx)
		if err != nil {
			return err
		}
		if !time.Now().Before(deadline) {
			return d.fail(fmt.Errorf("DFU status wait: %w", context.DeadlineExceeded))
		}
		if status.status != 0 {
			return d.fail(fmt.Errorf("DFU status 0x%02x in state %d (string %d)", status.status, status.state, status.stringIndex))
		}
		if (!manifest && status.state == dfuDownloadIdle) || (manifest && status.state == dfuIdle) {
			return nil
		}
		valid := status.state == dfuDownloadSync || status.state == dfuDownloadBusy
		if manifest {
			valid = status.state == dfuManifestSync || status.state == dfuManifest
		}
		if !valid {
			return d.fail(fmt.Errorf("unexpected DFU state %d during manifestation=%t", status.state, manifest))
		}
		if err := d.wait(ctx, status.delay, deadline); err != nil {
			return err
		}
	}
}

func (d *dfuClient) wait(ctx context.Context, delay time.Duration, deadline time.Time) error {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return d.fail(fmt.Errorf("DFU status wait: %w", context.DeadlineExceeded))
	}
	if delay > remaining {
		delay = remaining
	}
	if delay == 0 {
		return d.ready(ctx)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return d.fail(ctx.Err())
	case <-timer.C:
		return d.ready(ctx)
	}
}

func (d *dfuClient) download(ctx context.Context, alt int, src io.Reader, size int64) error {
	if size <= 0 || src == nil {
		return d.fail(errors.New("DFU download requires a nonempty payload"))
	}
	if err := d.begin(ctx, alt); err != nil {
		return err
	}
	var block uint16
	for remaining := size; remaining > 0; block++ {
		if err := d.ready(ctx); err != nil {
			return err
		}
		chunk := d.buffer[:min(int64(len(d.buffer)), remaining)]
		if _, err := io.ReadFull(src, chunk); err != nil {
			return d.fail(fmt.Errorf("read DFU payload at offset %d: %w", size-remaining, err))
		}
		if _, err := d.control(ctx, 0x21, dfuDownload, block, chunk); err != nil {
			return err
		}
		if err := d.poll(ctx, false); err != nil {
			return err
		}
		remaining -= int64(len(chunk))
	}
	if _, err := d.control(ctx, 0x21, dfuDownload, block, nil); err != nil {
		return err
	}
	if err := d.poll(ctx, true); err != nil {
		return err
	}
	d.idle = true
	return nil
}

// consume must finish using data before returning; data aliases the session's
// transfer buffer. True EOF (including a final zero response for exact block
// multiples) is the only successful way to end a partition upload.
func (d *dfuClient) uploadToEOF(ctx context.Context, alt int, consume func(int64, []byte) error) (int64, error) {
	if consume == nil {
		return 0, d.fail(errors.New("DFU upload requires a consumer"))
	}
	if err := d.begin(ctx, alt); err != nil {
		return 0, err
	}
	var offset int64
	for block := uint16(0); ; block++ {
		n, err := d.control(ctx, 0xa1, dfuUpload, block, d.buffer)
		if err != nil {
			return offset, err
		}
		if n > 0 {
			if err := consume(offset, d.buffer[:n]); err != nil {
				return offset + int64(n), d.fail(err)
			}
		}
		offset += int64(n)
		if err := d.ready(ctx); err != nil {
			return offset, err
		}
		if n < len(d.buffer) {
			d.idle = true
			return offset, nil
		}
	}
}

// Virtual phase queries are a single short upload, not a bounded partition
// read. A full response means the backend may still be live and is fatal.
func (d *dfuClient) uploadShort(ctx context.Context, alt int, requestSize int) ([]byte, error) {
	if requestSize <= 0 || requestSize > len(d.buffer) {
		return nil, d.fail(fmt.Errorf("invalid short DFU upload size %d", requestSize))
	}
	if err := d.begin(ctx, alt); err != nil {
		return nil, err
	}
	n, err := d.control(ctx, 0xa1, dfuUpload, 0, d.buffer[:requestSize])
	if err != nil {
		return nil, err
	}
	if n == requestSize {
		return nil, d.fail(errors.New("virtual DFU upload did not reach EOF"))
	}
	d.idle = true
	return d.buffer[:n], nil
}

func (d *dfuClient) detach(ctx context.Context, value uint16) error {
	if err := d.ready(ctx); err != nil {
		return err
	}
	if !d.idle {
		return d.fail(errors.New("DFU detach requires completed transactions"))
	}
	if _, err := d.control(ctx, 0x21, dfuDetach, value, nil); err != nil {
		return err
	}
	// This enumeration cannot be used again, even if the old device lingers.
	d.fail(errors.New("DFU connection detached"))
	return nil
}

func isUSBDisconnected(err error) bool {
	return errors.Is(err, gousb.ErrorNoDevice)
}

// libusb_reset_device returns NOT_FOUND when reset invalidates the handle
// (including changed descriptors). The old handle must be closed; bootstrap
// still requires discovery and validation of the next stage.
// This interpretation does not apply to DETACH or other control requests.
func isUSBResetReenumeration(err error) bool {
	return isUSBDisconnected(err) || errors.Is(err, gousb.ErrorNotFound)
}

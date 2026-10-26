package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/gousb"
)

// This target models the ROM/TF-A state and RAM, rather than returning an
// expected request list. An interrupted download leaves RAM live and unusable.
type bootstrapTarget struct {
	stage                bootStage
	alt                  int
	state                byte
	block                uint16
	ram                  []byte
	pending              []byte
	manifested           bool
	detached             bool
	closed               bool
	resets               int
	requestsAfterFailure int
	failed               bool
	timeoutWrite         bool
	badDetach            bool
	badPhase             bool
	loadedUBoot          bool
	initialError         bool
	resetError           error
	detachError          error
}

func (m *bootstrapTarget) Control(kind, request uint8, value, index uint16, data []byte) (int, error) {
	if m.failed {
		m.requestsAfterFailure++
		return 0, errors.New("transaction already failed")
	}
	if m.closed || index != 4 {
		return 0, errors.New("closed target or incorrect interface")
	}
	switch request {
	case 5:
		if kind != 0xa1 || len(data) != 1 {
			return 0, errors.New("invalid GETSTATE")
		}
		data[0] = m.state
		return 1, nil
	case 3:
		if kind != 0xa1 || len(data) != 6 {
			return 0, errors.New("invalid GETSTATUS")
		}
		if len(m.pending) > 0 {
			m.state, m.pending = m.pending[0], m.pending[1:]
			if m.state == 2 && len(m.ram) != 0 {
				m.manifested = true
			}
		}
		clear(data)
		data[4] = m.state
		if m.state == 7 {
			data[1] = 1
		}
		return 6, nil
	case 1:
		if kind != 0x21 || m.stage == stageUBoot || m.alt != 0 || value != m.block || (m.state != 2 && m.state != 5) {
			return 0, errors.New("invalid bootstrap download; no SetPhase is supported")
		}
		if len(data) == 0 {
			if len(m.ram) == 0 {
				return 0, errors.New("empty download")
			}
			m.pending = []byte{7, 2}
		} else {
			m.ram = append(m.ram, data...)
			m.block++
			m.pending = []byte{3, 5}
			if m.timeoutWrite {
				m.failed = true
				return 0, context.DeadlineExceeded
			}
		}
		return len(data), nil
	case 2:
		want := 255
		if m.stage == stageUBoot {
			want = 512
		}
		if kind != 0xa1 || value != 0 || m.alt != 1 || len(data) != want || m.state != 2 {
			return 0, errors.New("invalid GetPhase or unsafe phase query")
		}
		clear(data)
		if m.stage == stageUBoot {
			if m.loadedUBoot {
				data[9] = 1
			}
			return 10, nil
		}
		if m.manifested {
			binary.LittleEndian.PutUint32(data[1:5], 0xffffffff)
			data[9] = 1
			if m.badDetach {
				data[1] = 0
			}
			return 10, nil
		}
		data[0] = 1
		if m.stage == stageTFA {
			data[0] = 3
		}
		if m.badPhase {
			data[0] = 0xf3
		}
		binary.LittleEndian.PutUint32(data[1:5], 0x2ffe0000)
		return 9, nil
	case 0:
		if kind != 0x21 || value != 1000 || len(data) != 0 || !m.manifested || m.state != 2 {
			return 0, errors.New("premature or malformed detach")
		}
		m.detached = true
		return 0, m.detachError
	case 4:
		if kind != 0x21 || m.state != 10 || len(data) != 0 {
			return 0, errors.New("invalid CLRSTATUS")
		}
		m.state = 2
		return 0, nil
	default:
		return 0, fmt.Errorf("unsupported bootstrap request %d", request)
	}
}

func (m *bootstrapTarget) SetAlt(alt int) error {
	if m.failed {
		m.requestsAfterFailure++
		return errors.New("alternate change after uncertain write")
	}
	if m.state != 2 || (alt != 0 && alt != 1) {
		return errors.New("alternate change outside idle")
	}
	m.alt = alt
	return nil
}

func (m *bootstrapTarget) Reset() error {
	if !m.detached {
		return errors.New("reset before detach")
	}
	m.resets++
	return m.resetError
}

func (m *bootstrapTarget) Close() error {
	m.closed = true
	return nil
}

func bootstrapModelDescriptor(stage bootStage) usbDescriptor {
	d := usbDescriptor{interfaceNumber: 4, transferSize: 1024, phases: map[uint8]usbAlternate{
		0xf1: {alt: 1, phase: 0xf1, name: "virtual", capacity: 512, permissions: 'a'},
	}}
	switch stage {
	case stageROM:
		d.phases[1] = usbAlternate{alt: 0, phase: 1, name: "FSBL", capacity: bootstrapROMCapacity, permissions: 'e'}
	case stageTFA:
		d.phases[3] = usbAlternate{alt: 0, phase: 3, name: "SSBL", capacity: bootstrapFIPCapacity, permissions: 'e'}
	case stageUBoot:
		d.transferSize = 4096
		d.phases[0] = usbAlternate{alt: 0, phase: 0, name: "FlashLayout", capacity: 256 * 1024, permissions: 'e'}
		virtual := d.phases[0xf1]
		virtual.permissions = 'e'
		d.phases[0xf1] = virtual
	}
	return d
}

func bootstrapModelSession(targets ...*bootstrapTarget) *usbSession {
	s := &usbSession{ctx: context.Background()}
	adopt := func(m *bootstrapTarget) {
		m.state = 2
		if m.initialError {
			m.state = 10
		}
		s.transport, s.stage = m, m.stage
		s.descriptor = bootstrapModelDescriptor(m.stage)
		s.buffer = make([]byte, s.descriptor.transferSize)
		s.dfu = nil
	}
	adopt(targets[0])
	next := 1
	s.reconnectFn = func(ctx context.Context, expected bootStage, layout map[uint8]usbPhaseExpectation) error {
		old := s.transport.(*bootstrapTarget)
		if !old.detached || old.resets != 1 || layout != nil {
			return errors.New("invalid bootstrap transition")
		}
		if err := old.Close(); err != nil {
			return err
		}
		if next == len(targets) {
			return context.DeadlineExceeded
		}
		adopt(targets[next])
		next++
		return nil
	}
	return s
}

func bootstrapFixtures(t *testing.T) (map[string]string, []byte, []byte) {
	t.Helper()
	loader := bytes.Repeat([]byte{0xa7}, 512+2304)
	copy(loader, "STM2")
	binary.LittleEndian.PutUint32(loader[72:76], 0x00020000)
	binary.LittleEndian.PutUint32(loader[76:80], uint32(len(loader)-512))
	binary.LittleEndian.PutUint32(loader[104:108], 384)
	fip := bytes.Repeat([]byte{0x39}, 3181)
	binary.LittleEndian.PutUint32(fip[:4], 0xaa640001)
	binary.LittleEndian.PutUint32(fip[4:8], 1)
	dir := t.TempDir()
	images := map[string]string{"tfa_usb": filepath.Join(dir, "loader.stm32"), "fip_optee": filepath.Join(dir, "fip.bin")}
	for key, data := range map[string][]byte{"tfa_usb": loader, "fip_optee": fip} {
		if err := os.WriteFile(images[key], data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return images, loader, fip
}

func TestBootstrapPreservesSignedImagesAndArrivesFresh(t *testing.T) {
	images, loader, fip := bootstrapFixtures(t)
	rom := &bootstrapTarget{stage: stageROM, detachError: gousb.ErrorNoDevice, resetError: gousb.ErrorNoDevice}
	tfa := &bootstrapTarget{stage: stageTFA}
	uboot := &bootstrapTarget{stage: stageUBoot}
	s := bootstrapModelSession(rom, tfa, uboot)
	if err := bootstrap(context.Background(), s, stageROM, images); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rom.ram, loader) || !bytes.Equal(tfa.ram, fip) {
		t.Fatal("bootstrap changed signed headers, raw FIP, or artifact length")
	}
	if !rom.closed || !tfa.closed || uboot.closed || uboot.detached || uboot.resets != 0 || s.stage != stageUBoot {
		t.Fatal("bootstrap did not retain only the fresh U-Boot connection")
	}
}

func TestBootstrapResetReenumerationRequiresNextStage(t *testing.T) {
	for _, nextPresent := range []bool{true, false} {
		t.Run(fmt.Sprint(nextPresent), func(t *testing.T) {
			images, loader, fip := bootstrapFixtures(t)
			rom := &bootstrapTarget{stage: stageROM, resetError: fmt.Errorf("reset: %w", gousb.ErrorNotFound)}
			tfa := &bootstrapTarget{stage: stageTFA, resetError: gousb.ErrorNotFound}
			uboot := &bootstrapTarget{stage: stageUBoot}
			targets := []*bootstrapTarget{rom}
			if nextPresent {
				targets = append(targets, tfa, uboot)
			}
			s := bootstrapModelSession(targets...)
			err := bootstrap(context.Background(), s, stageROM, images)
			if nextPresent {
				if err != nil || s.stage != stageUBoot || !bytes.Equal(tfa.ram, fip) {
					t.Fatalf("reset re-enumeration did not reach fresh U-Boot: %v", err)
				}
			} else if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("missing next stage must fail discovery, not accept reset as completion: %v", err)
			}
			if !bytes.Equal(rom.ram, loader) || rom.resets != 1 || !rom.closed || uboot.detached || uboot.resets != 0 {
				t.Fatal("transition replayed the loader/reset or modified the destination stage")
			}
		})
	}
}

func TestBootstrapNotFoundDuringDetachRemainsFatal(t *testing.T) {
	images, _, _ := bootstrapFixtures(t)
	tfa := &bootstrapTarget{stage: stageTFA, detachError: gousb.ErrorNotFound}
	uboot := &bootstrapTarget{stage: stageUBoot}
	s := bootstrapModelSession(tfa, uboot)
	err := bootstrap(context.Background(), s, stageTFA, images)
	if !errors.Is(err, gousb.ErrorNotFound) || tfa.resets != 0 || !tfa.closed || s.stage != stageTFA {
		t.Fatalf("DETACH failure was treated as reset re-enumeration: %v", err)
	}
}

func TestBootstrapFailureDoesNotContinue(t *testing.T) {
	for _, failure := range []string{"timeout", "detach-response", "missing-stage", "wrong-stage", "loaded-uboot", "wrong-phase", "reset-error", "detach-error"} {
		t.Run(failure, func(t *testing.T) {
			images, _, _ := bootstrapFixtures(t)
			tfa := &bootstrapTarget{stage: stageTFA}
			uboot := &bootstrapTarget{stage: stageUBoot}
			targets := []*bootstrapTarget{tfa, uboot}
			switch failure {
			case "timeout":
				tfa.timeoutWrite = true
			case "detach-response":
				tfa.badDetach = true
			case "missing-stage":
				targets = targets[:1]
			case "wrong-stage":
				uboot.stage = stageROM
			case "loaded-uboot":
				uboot.loadedUBoot = true
			case "wrong-phase":
				tfa.badPhase = true
			case "reset-error":
				tfa.resetError = errors.New("reset failed")
			case "detach-error":
				tfa.detachError = errors.New("detach failed")
			}
			s := bootstrapModelSession(targets...)
			if err := bootstrap(context.Background(), s, stageTFA, images); err == nil {
				t.Fatal("unsafe bootstrap accepted")
			}
			if !tfa.closed || tfa.requestsAfterFailure != 0 || uboot.detached || uboot.resets != 0 || len(uboot.ram) != 0 {
				t.Fatal("failed bootstrap continued or leaked its connection")
			}
			if failure == "timeout" && (len(tfa.ram) != 1024 || tfa.detached || tfa.resets != 0) {
				t.Fatal("uncertain accepted download was replayed or advanced")
			}
			if (failure == "detach-response" || failure == "wrong-phase") && (tfa.detached || tfa.resets != 0) {
				t.Fatal("unexpected phase response allowed stage transition")
			}
		})
	}
}
func TestBootstrapRefusesInitialErrorsInROMAndTFA(t *testing.T) {
	for _, failedStage := range []bootStage{stageROM, stageTFA} {
		t.Run(failedStage.String(), func(t *testing.T) {
			images, _, _ := bootstrapFixtures(t)
			rom := &bootstrapTarget{stage: stageROM, initialError: failedStage == stageROM}
			tfa := &bootstrapTarget{stage: stageTFA, initialError: failedStage == stageTFA}
			uboot := &bootstrapTarget{stage: stageUBoot}
			s := bootstrapModelSession(rom, tfa, uboot)
			if err := bootstrap(context.Background(), s, stageROM, images); err == nil {
				t.Fatal("bootstrap recovered an unclean ROM/TF-A session")
			}
			failed := rom
			if failedStage == stageTFA {
				failed = tfa
			}
			if len(failed.ram) != 0 || failed.detached || failed.resets != 0 {
				t.Fatal("unclean bootstrap stage was modified")
			}
		})
	}
}

func TestBootstrapTFARequiresOnlyFIP(t *testing.T) {
	images, _, fip := bootstrapFixtures(t)
	delete(images, "tfa_usb")
	tfa := &bootstrapTarget{stage: stageTFA}
	uboot := &bootstrapTarget{stage: stageUBoot}
	s := bootstrapModelSession(tfa, uboot)
	if err := bootstrap(context.Background(), s, stageTFA, images); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(tfa.ram, fip) || s.stage != stageUBoot {
		t.Fatal("direct TF-A bootstrap did not deliver the raw FIP and arrive in U-Boot")
	}
}

func TestBootstrapPreflightsEveryRequiredFile(t *testing.T) {
	images, _, _ := bootstrapFixtures(t)
	if err := os.WriteFile(images["fip_optee"], []byte("not a FIP"), 0600); err != nil {
		t.Fatal(err)
	}
	rom := &bootstrapTarget{stage: stageROM}
	s := bootstrapModelSession(rom)
	if err := bootstrap(context.Background(), s, stageROM, images); err == nil || len(rom.ram) != 0 {
		t.Fatal("invalid second-stage artifact permitted ROM download")
	}
	if err := validateBootstrapImages(stageUBoot, nil); err != nil {
		t.Fatal("direct U-Boot unexpectedly requires bootstrap artifacts:", err)
	}
}

func TestBootstrapRejectsMalformedHeaders(t *testing.T) {
	for _, defect := range []string{"magic", "version", "length", "extensions", "empty", "oversize", "fip-magic", "fip-serial", "fip-oversize"} {
		t.Run(defect, func(t *testing.T) {
			images, loader, fip := bootstrapFixtures(t)
			key, data := "tfa_usb", loader
			switch defect {
			case "magic":
				data[0] = 0
			case "version":
				data[74] = 1
			case "length":
				data[76]++
			case "extensions":
				data[104] = 0
			case "empty":
				data = nil
			case "oversize":
				data = make([]byte, bootstrapROMCapacity+1)
			case "fip-magic":
				key, data = "fip_optee", fip
				data[0] = 0
			case "fip-serial":
				key, data = "fip_optee", fip
				clear(data[4:8])
			case "fip-oversize":
				key, data = "fip_optee", make([]byte, bootstrapFIPCapacity+1)
			}
			if err := os.WriteFile(images[key], data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := validateBootstrapImages(stageROM, images); err == nil {
				t.Fatal("malformed bootstrap image accepted")
			}
		})
	}
}

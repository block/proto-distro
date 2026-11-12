package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/google/gousb"
)

func TestUSBClassifiesOnlySupportedStageLayouts(t *testing.T) {
	for _, stage := range []bootStage{stageROM, stageTFA, stageUBoot} {
		descriptor := bootstrapModelDescriptor(stage)
		got, err := classifyUSB(descriptor.phases, nil)
		if err != nil || got != stage {
			t.Fatalf("classifying %s: got %s, %v", stage, got, err)
		}
	}
	for _, kind := range []string{"ssp", "ordinary-dfu", "mp15", "wrong-ram-size", "preloaded"} {
		t.Run(kind, func(t *testing.T) {
			phases := bootstrapModelDescriptor(stageROM).phases
			switch kind {
			case "ssp":
				phases[0xf3] = usbAlternate{alt: 2, phase: 0xf3, name: "SSP", capacity: 512, permissions: 'e'}
			case "ordinary-dfu":
				delete(phases, 0xf1)
			case "mp15":
				phases[1] = usbAlternate{alt: 1, phase: 1, name: "FSBL", capacity: 1024 * 1024, permissions: 'e'}
				phases[3] = usbAlternate{alt: 3, phase: 3, name: "SSBL", capacity: 16 * 1024 * 1024, permissions: 'e'}
				phases[0xf1] = usbAlternate{alt: 4, phase: 0xf1, name: "virtual", capacity: 512, permissions: 'a'}
			case "wrong-ram-size":
				alt := phases[1]
				alt.capacity *= 2
				phases[1] = alt
			case "preloaded":
				phases = bootstrapModelDescriptor(stageUBoot).phases
				delete(phases, 0)
				phases[12] = usbAlternate{alt: 2, phase: 12, name: "rootfs_a", capacity: 1024 * 1024 * 1024, permissions: 'e'}
			}
			if _, err := classifyUSB(phases, nil); err == nil {
				t.Fatal("unsupported or already loaded layout accepted")
			}
		})
	}
}

func TestUSBInitializedLayoutMustMatchSubmittedPlan(t *testing.T) {
	phases := bootstrapModelDescriptor(stageUBoot).phases
	delete(phases, 0)
	phases[12] = usbAlternate{alt: 7, phase: 12, name: "rootfs_a", capacity: 1024 * 1024 * 1024, permissions: 'e'}
	phases[13] = usbAlternate{alt: 8, phase: 13, name: "rootfs_b", capacity: 1024 * 1024 * 1024, permissions: 'a'}
	layout := map[uint8]usbPhaseExpectation{12: {name: "rootfs_a", permissions: 'e'}, 13: {name: "rootfs_b", permissions: 'a'}}
	if stage, err := classifyUSB(phases, layout); err != nil || stage != stageUBoot {
		t.Fatalf("tool-owned initialized layout rejected: %v", err)
	}
	for _, defect := range []string{"missing", "name", "permission", "extra"} {
		t.Run(defect, func(t *testing.T) {
			changed := make(map[uint8]usbAlternate, len(phases))
			for phase, alt := range phases {
				changed[phase] = alt
			}
			alt := changed[13]
			switch defect {
			case "missing":
				delete(changed, 13)
			case "name":
				alt.name = "unexpected"
				changed[13] = alt
			case "permission":
				alt.permissions = 'e'
				changed[13] = alt
			case "extra":
				changed[14] = usbAlternate{alt: 9, phase: 14, name: "data", capacity: 512, permissions: 'e'}
			}
			if _, err := classifyUSB(changed, layout); err == nil {
				t.Fatal("contradictory initialized layout accepted")
			}
		})
	}
}

func TestUSBAlternateUsesParsedPhaseNotSelector(t *testing.T) {
	alt, err := parseUSBAlternate(7, "@rootfs_a/0x0c/1*1024Me")
	if err != nil || alt.alt != 7 || alt.phase != 12 || alt.capacity != 1024*1024*1024 {
		t.Fatalf("alternate selector/phase or capacity lost: %+v, %v", alt, err)
	}
	for _, name := range []string{"@Internal Flash /0x08000000/04*016Kg,01*064Kg,07*128Kg", "@FSBL /0x01/1*0Ke", "@FSBL /0x01/1*9223372036854775807Me", "@name-is-far-too-long/0x01/1*128Ke"} {
		if _, err := parseUSBAlternate(0, name); err == nil {
			t.Fatalf("unsupported alternate accepted: %s", name)
		}
	}
}

func rawBootstrapConfiguration() []byte {
	return []byte{
		9, 2, 27, 0, 1, 2, 0, 0x80, 50,
		9, 4, 4, 0, 0, 0xfe, 1, 2, 0,
		9, 0x21, 7, 0xe8, 3, 0, 4, 0x10, 1,
	}
}

func TestUSBFunctionalDescriptorBoundsAndCapabilities(t *testing.T) {
	for _, size := range []uint16{1024, 4096} {
		raw := rawBootstrapConfiguration()
		binary.LittleEndian.PutUint16(raw[23:25], size)
		descriptor := usbDescriptor{config: 2, interfaceNumber: 4}
		if err := parseUSBFunctional(raw, &descriptor); err != nil || descriptor.transferSize != int(size) || descriptor.version != 0x110 {
			t.Fatalf("valid functional descriptor: %+v, %v", descriptor, err)
		}
	}
	for _, defect := range []string{"config-header", "total-length", "config-number", "zero-length", "truncated", "interface-length", "wrong-interface", "mcu-version", "functional-length", "zero-transfer", "oversize-transfer", "no-upload", "no-download", "wrong-protocol", "conflicting"} {
		t.Run(defect, func(t *testing.T) {
			raw := rawBootstrapConfiguration()
			switch defect {
			case "config-header":
				raw = raw[:8]
			case "total-length":
				raw[2]--
			case "config-number":
				raw[5]++
			case "zero-length":
				raw[18] = 0
			case "truncated":
				raw = raw[:len(raw)-1]
				raw[2]--
			case "interface-length":
				raw[9] = 8
			case "wrong-interface":
				raw[11]++
			case "mcu-version":
				raw[25] = 0x1a
			case "functional-length":
				raw[18] = 8
			case "zero-transfer":
				raw[24] = 0
			case "oversize-transfer":
				raw[24] = 0x20
			case "no-upload":
				raw[20] &^= 2
			case "no-download":
				raw[20] &^= 1
			case "wrong-protocol":
				raw[16] = 1
			case "conflicting":
				raw = append(raw, raw[18:]...)
				raw[len(raw)-3] = 8
				raw[2] = byte(len(raw))
			}
			descriptor := usbDescriptor{config: 2, interfaceNumber: 4}
			if err := parseUSBFunctional(raw, &descriptor); err == nil {
				t.Fatal("malformed or unsupported functional descriptor accepted")
			}
		})
	}
}

func TestUSBIdentityRequiresPhysicalPortAndConsistentUID(t *testing.T) {
	desc := &gousb.DeviceDesc{Bus: 2, Path: []int{1, 3}}
	identity, err := physicalIdentity(desc)
	if err != nil {
		t.Fatal(err)
	}
	identity.serial = "device-uid"
	if !identity.matches(&gousb.DeviceDesc{Bus: 2, Path: []int{1, 3}, Address: 19}) {
		t.Fatal("same physical port rejected after USB address changed")
	}
	for _, other := range []*gousb.DeviceDesc{{Bus: 3, Path: []int{1, 3}}, {Bus: 2, Path: []int{1, 4}}, {Bus: 2, Path: []int{3}}} {
		if identity.matches(other) {
			t.Fatal("different physical port accepted")
		}
	}
	for _, missing := range []*gousb.DeviceDesc{{Bus: 2}, {Bus: -1, Path: []int{1}}, {Bus: 256, Path: []int{1}}, {Bus: 2, Path: []int{0}}} {
		if _, err := physicalIdentity(missing); err == nil {
			t.Fatal("unstable identity accepted")
		}
	}
	for _, stage := range []bootStage{stageROM, stageTFA, stageUBoot} {
		if err := identity.checkSerial("other-uid", stage); err == nil {
			t.Fatal("contradictory UID accepted")
		}
		if err := identity.checkSerial("device-uid", stage); err != nil {
			t.Fatal(err)
		}
		if err := identity.checkSerial("", stage); (err == nil) != (stage == stageUBoot) {
			t.Fatalf("unexpected missing-UID policy for %s: %v", stage, err)
		}
	}
}

func TestUSBIdentityAcceptsBusZeroAcrossAddressChanges(t *testing.T) {
	// A real macOS enumeration uses bus zero with this nonempty port chain.
	desc := &gousb.DeviceDesc{Bus: 0, Path: []int{1, 2, 4, 3, 1}, Address: 18}
	identity, err := physicalIdentity(desc)
	if err != nil {
		t.Fatal(err)
	}
	desc.Address = 23
	if !identity.matches(desc) {
		t.Fatal("bus-zero device lost identity after re-enumeration")
	}
	desc.Path = []int{1, 2, 4, 3, 2}
	if identity.matches(desc) {
		t.Fatal("different physical port accepted on bus zero")
	}
	if _, err := physicalIdentity(&gousb.DeviceDesc{Bus: 0}); err == nil {
		t.Fatal("bus zero accepted without a physical port path")
	}
}

// Models a stale Linux node that disappears while OpenDevices is opening it,
// followed by a usable enumeration. No device handle or DFU operation is used.
type reconnectUSBContext struct {
	descriptors []*gousb.DeviceDesc
	failure     error
	scans       int
}

func (c *reconnectUSBContext) OpenDevices(match func(*gousb.DeviceDesc) bool) ([]*gousb.Device, error) {
	c.scans++
	var devices []*gousb.Device
	for _, desc := range c.descriptors {
		if match(desc) && c.scans > 1 {
			devices = append(devices, &gousb.Device{Desc: desc})
		}
	}
	if c.scans == 1 {
		return nil, c.failure
	}
	return devices, nil
}

func (*reconnectUSBContext) Close() error { return nil }

func TestUSBDiscoveryOpenFailureRetryScope(t *testing.T) {
	for _, tc := range []struct {
		name         string
		reconnecting bool
		matches      int
		failure      error
		retry        bool
	}{
		{"reconnect-single-no-device", true, 1, gousb.ErrorNoDevice, true},
		{"initial-no-device", false, 1, gousb.ErrorNoDevice, false},
		{"multiple-no-device", true, 2, gousb.ErrorNoDevice, false},
		{"zero-no-device", true, 0, gousb.ErrorNoDevice, false},
		{"access", true, 1, gousb.ErrorAccess, false},
		{"io", true, 1, gousb.ErrorIO, false},
		{"pipe", true, 1, gousb.ErrorPipe, false},
		{"timeout", true, 1, gousb.ErrorTimeout, false},
		{"other", true, 1, gousb.ErrorOther, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usb := &reconnectUSBContext{failure: tc.failure}
			for i := range tc.matches {
				usb.descriptors = append(usb.descriptors, &gousb.DeviceDesc{
					Vendor: 0x0483, Product: 0xdf11, Bus: 1, Path: []int{2 + i},
				})
			}
			session := &usbSession{usb: usb, identity: usbIdentity{bus: 1, path: []int{2}}}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var found *gousb.Device
			err := waitForUSB(ctx, func() (bool, error) {
				var err error
				found, err = session.findDevice(tc.reconnecting)
				return found != nil, err
			})
			if tc.retry {
				if err != nil || usb.scans != 2 || found == nil || found.Desc != usb.descriptors[0] {
					t.Fatalf("did not rediscover the selected device: scans=%d device=%v error=%v", usb.scans, found, err)
				}
			} else if !errors.Is(err, tc.failure) || usb.scans != 1 || found != nil {
				t.Fatalf("fatal discovery failure was not preserved: scans=%d device=%v error=%v", usb.scans, found, err)
			}
		})
	}
}

// Models an alternate string that is unavailable while the gadget rebuilds
// its descriptor table, then becomes readable. No DFU operation is supported.
type reconnectStringTarget struct {
	failures []error
	reads    int
	data     []byte
}

func (t *reconnectStringTarget) Control(rt, request uint8, value, index uint16, data []byte) (int, error) {
	if rt != 0x80 || request != 6 || value != 0x0306 || index != 0x0409 {
		return 0, errors.New("unexpected request: discovery must only read the selected string")
	}
	t.reads++
	if len(t.failures) > 0 {
		err := t.failures[0]
		t.failures = t.failures[1:]
		return 0, err
	}
	return copy(data, t.data), nil
}
func (*reconnectStringTarget) SetAlt(int) error { return errors.New("discovery selected an alternate") }
func (*reconnectStringTarget) Reset() error     { return errors.New("discovery reset the target") }
func (*reconnectStringTarget) Close() error     { return nil }

func TestUSBReconnectWaitsForReadableAlternateStrings(t *testing.T) {
	units := utf16.Encode([]rune("@fsbl2/0x05/1*4Me"))
	rawString := make([]byte, 2+len(units)*2)
	rawString[0], rawString[1] = byte(len(rawString)), 3
	for i, unit := range units {
		binary.LittleEndian.PutUint16(rawString[2+i*2:], unit)
	}
	target := &reconnectStringTarget{failures: []error{gousb.ErrorPipe, gousb.ErrorIO, gousb.ErrorOther}, data: rawString}
	rawConfig := rawBootstrapConfiguration()
	rawConfig[12], rawConfig[17] = 1, 6 // Alternate 1 names string descriptor 6.
	descriptor := usbDescriptor{config: 2, interfaceNumber: 4}
	if err := parseUSBFunctional(rawConfig, &descriptor); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var found usbAlternate
	err := waitForUSB(ctx, func() (bool, error) {
		text, err := readUSBString(target, descriptor.alternateStrings[1], 0x0409)
		if err != nil {
			return false, &usbDescriptorReadError{err}
		}
		found, err = parseUSBAlternate(1, text)
		return err == nil, err
	})
	if err != nil || found.phase != 5 || found.name != "fsbl2" || found.capacity != 4*1024*1024 {
		t.Fatalf("did not discover rebuilt alternate: %+v, %v", found, err)
	}
	if target.reads != 4 {
		t.Fatalf("expected three transient failures followed by a completed read, got %d reads", target.reads)
	}
}

// U-Boot's DFU_DETACH swaps in a single runtime interface without a
// functional descriptor before rebuilding the gadget's DFU alternates.
func rawRuntimeConfiguration() []byte {
	return []byte{
		9, 2, 18, 0, 1, 2, 0, 0x80, 50,
		9, 4, 4, 0, 0, 0xfe, 1, 1, 4,
	}
}

func TestUSBReconnectWaitsForRuntimeToDFUMode(t *testing.T) {
	runtime := rawRuntimeConfiguration()
	if err := parseUSBFunctional(runtime, &usbDescriptor{config: 2, interfaceNumber: 4}); err == nil {
		t.Fatal("runtime interface accepted for initial flashing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	configs := [][]byte{runtime, rawBootstrapConfiguration()}
	var found usbDescriptor
	err := waitForUSB(ctx, func() (bool, error) {
		if len(configs) == 0 {
			return false, errors.New("DFU-mode configuration was not accepted")
		}
		raw := configs[0]
		configs = configs[1:]
		found = usbDescriptor{config: 2, interfaceNumber: 4}
		if err := parseUSBFunctional(raw, &found); err != nil {
			return false, &usbDescriptorReadError{err}
		}
		return true, nil
	})
	if err != nil || len(configs) != 0 || found.transferSize != 1024 {
		t.Fatalf("did not reach usable DFU mode after runtime: %+v, %v", found, err)
	}
}

func TestUSBReconnectRuntimeRemainsBoundedAndCancellable(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelled=%t", cancelled), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			calls := 0
			var runtimeErr error
			err := waitForUSB(ctx, func() (bool, error) {
				calls++
				runtimeErr = parseUSBFunctional(rawRuntimeConfiguration(), &usbDescriptor{config: 2, interfaceNumber: 4})
				if runtimeErr == nil {
					t.Fatal("runtime interface accepted as ready")
				}
				if cancelled {
					cancel()
				}
				return false, &usbDescriptorReadError{runtimeErr}
			})
			want := context.DeadlineExceeded
			if cancelled {
				want = context.Canceled
			}
			if !errors.Is(err, want) || !errors.Is(err, runtimeErr) || calls != 1 {
				t.Fatalf("runtime wait lost cancellation/deadline or descriptor error: calls=%d, %v", calls, err)
			}
		})
	}
}

func TestUSBReconnectRejectsMalformedOrMixedRuntimeInterfaces(t *testing.T) {
	for _, kind := range []string{"runtime-first", "dfu-first", "nonzero-alternate", "endpoint", "malformed-tail"} {
		t.Run(kind, func(t *testing.T) {
			raw := rawRuntimeConfiguration()
			switch kind {
			case "runtime-first":
				raw = append(raw, 9, 4, 4, 1, 0, 0xfe, 1, 2, 5)
			case "dfu-first":
				raw = rawBootstrapConfiguration()
				raw[12] = 1
				raw = append(raw, rawRuntimeConfiguration()[9:]...)
			case "nonzero-alternate":
				raw[12] = 1
			case "endpoint":
				raw[13] = 1
			case "malformed-tail":
				raw = append(raw, 9, 4)
			}
			raw[2] = byte(len(raw))
			failure := parseUSBFunctional(raw, &usbDescriptor{config: 2, interfaceNumber: 4})
			if failure == nil {
				t.Fatal("malformed or mixed runtime interface accepted")
			}
			calls := 0
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := waitForUSB(ctx, func() (bool, error) {
				calls++
				return false, &usbDescriptorReadError{failure}
			})
			if !errors.Is(err, failure) || calls != 1 {
				t.Fatalf("permanent descriptor failure was retried: calls=%d, %v", calls, err)
			}
		})
	}
}

func TestUSBReconnectDoesNotRetryWritesOrPermanentDiscoveryErrors(t *testing.T) {
	for _, failure := range []error{
		fmt.Errorf("DFU download: %w", gousb.ErrorPipe),
		fmt.Errorf("DFU download: %w", gousb.ErrorIO),
		fmt.Errorf("DFU download: %w", gousb.ErrorOther),
		fmt.Errorf("configuration claim: %w", gousb.ErrorIO),
		fmt.Errorf("configuration claim: %w", gousb.ErrorOther),
		errUSBRuntimeMode, // Only descriptor discovery may wait through runtime mode.
		&usbDescriptorReadError{gousb.ErrorAccess},
		&usbDescriptorReadError{errors.New("unsupported device or layout")},
		errors.New("USB serial changed"),
	} {
		calls := 0
		err := waitForUSB(context.Background(), func() (bool, error) {
			calls++
			if calls > 1 {
				return true, nil
			}
			return false, failure
		})
		if !errors.Is(err, failure) || calls != 1 {
			t.Fatalf("retried a fatal error: %v (calls %d, result %v)", failure, calls, err)
		}
	}
}

func TestUSBReconnectDeadlinePreservesDescriptorFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := waitForUSB(ctx, func() (bool, error) {
		return false, &usbDescriptorReadError{fmt.Errorf("read USB string languages: %w", gousb.ErrorIO)}
	})
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, gousb.ErrorIO) {
		t.Fatalf("missing bounded wait or underlying descriptor error: %v", err)
	}
}

func TestUSBReconnectCancellationDoesNotContinueDiscovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	err := waitForUSB(ctx, func() (bool, error) {
		calls++
		cancel()
		return false, &usbDescriptorReadError{gousb.ErrorTimeout}
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("discovery continued after cancellation: calls=%d error=%v", calls, err)
	}
}

func TestUSBStringsRejectMalformedDescriptors(t *testing.T) {
	for _, data := range [][]byte{{}, {2}, {4, 3, 0}, {6, 3, 'a', 0}, {4, 2, 'a', 0}} {
		target := &reconnectStringTarget{data: data}
		if _, err := readUSBString(target, 6, 0x0409); err == nil {
			t.Fatalf("malformed string descriptor accepted: %x", data)
		}
	}
}

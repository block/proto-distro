package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/google/gousb"
)

const (
	usbControlTimeout   = 5 * time.Second
	usbReconnectTimeout = 120 * time.Second
)

type bootStage uint8

const (
	stageROM bootStage = iota
	stageTFA
	stageUBoot
)

func (s bootStage) String() string {
	switch s {
	case stageROM:
		return "STM32MP13 ROM"
	case stageTFA:
		return "TF-A"
	case stageUBoot:
		return "U-Boot stm32prog"
	default:
		return "unknown USB stage"
	}
}

type usbTransport interface {
	Control(requestType, request uint8, value, index uint16, data []byte) (int, error)
	SetAlt(alt int) error
	Reset() error
	Close() error
}

// Alternate numbers are USB selectors, not stm32prog phase IDs. Capacity is
// the advertised lower bound: U-Boot rounds its displayed sizes down.
type usbAlternate struct {
	alt         int
	phase       uint8
	name        string
	capacity    int64
	permissions byte
}

type usbDescriptor struct {
	config           int
	interfaceNumber  int
	transferSize     int
	attributes       uint8
	version          uint16
	detachTimeout    uint16
	phases           map[uint8]usbAlternate
	alternateStrings map[int]uint8
}

// The initialized map includes every submitted storage row, including
// unselected/reserved rows (permission 'a'); writable rows advertise 'e'.
// Virtual command/OTP/PMIC alternates are not part of this map.
type usbPhaseExpectation struct {
	name        string
	permissions byte
}

type usbIdentity struct {
	bus    int
	path   []int
	serial string
}

func physicalIdentity(desc *gousb.DeviceDesc) (usbIdentity, error) {
	// libusb bus numbers are unsigned bytes; zero is valid (including macOS).
	// The nonempty port chain, not a positive bus number or device address,
	// provides the physical identity used across re-enumeration.
	if desc.Bus < 0 || desc.Bus > 255 || len(desc.Path) == 0 {
		return usbIdentity{}, errors.New("USB device has no stable bus/port path; refusing to flash")
	}
	for _, port := range desc.Path {
		if port <= 0 {
			return usbIdentity{}, errors.New("USB device has an invalid physical port path")
		}
	}
	return usbIdentity{bus: desc.Bus, path: slices.Clone(desc.Path)}, nil
}

func (id usbIdentity) matches(desc *gousb.DeviceDesc) bool {
	return id.bus == desc.Bus && slices.Equal(id.path, desc.Path)
}

func (id usbIdentity) checkSerial(serial string, stage bootStage) error {
	if id.serial != "" && serial != "" && id.serial != serial {
		return errors.New("USB serial changed on the selected physical port; refusing a different device")
	}
	if id.serial != "" && serial == "" && stage != stageUBoot {
		return errors.New("ROM/TF-A USB UID disappeared during re-enumeration")
	}
	return nil
}

// listDevices deliberately has no single-device restriction. It reports all
// STM32 DFU candidates, including why an unsupported layout cannot be flashed.
func listDevices(ctx context.Context, out io.Writer) (result error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	usb := gousb.NewContext()
	devices, err := usb.OpenDevices(func(desc *gousb.DeviceDesc) bool {
		return ctx.Err() == nil && desc.Vendor == 0x0483 && desc.Product == 0xdf11
	})
	defer func() {
		for _, device := range devices {
			result = errors.Join(result, device.Close())
		}
		result = errors.Join(result, usb.Close())
	}()
	slices.SortFunc(devices, func(a, b *gousb.Device) int {
		if a.Desc.Bus != b.Desc.Bus {
			return a.Desc.Bus - b.Desc.Bus
		}
		return a.Desc.Address - b.Desc.Address
	})
	for _, device := range devices {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return errors.Join(err, ctxErr)
		}
		t := &gousbTransport{ctx: ctx, device: device}
		stage, describeErr := t.describe(nil)
		_, identityErr := physicalIdentity(device.Desc)
		status := stage.String() + " (descriptor match)"
		if describeErr != nil {
			status = "not flashable: " + describeErr.Error()
		}
		if identityErr != nil {
			status += "; " + identityErr.Error()
		}
		if _, writeErr := fmt.Fprintf(out,
			"%04x:%04x  bus=%d ports=%v address=%d\n  Product: %q\n  Serial:  %q\n  Status:  %s\n",
			uint16(device.Desc.Vendor), uint16(device.Desc.Product),
			device.Desc.Bus, device.Desc.Path, device.Desc.Address,
			t.product, t.serial, status); writeErr != nil {
			return errors.Join(err, writeErr)
		}
	}
	if len(devices) == 0 && err == nil && ctx.Err() == nil {
		_, err = fmt.Fprintln(out, "No STM32 DFU devices (0483:df11) found.")
	}
	return errors.Join(err, ctx.Err())
}

// Access is sequential. gousb's synchronous controls cannot be interrupted
// mid-call; cancellation is checked between calls and each call is bounded.
type gousbTransport struct {
	ctx        context.Context
	device     *gousb.Device
	config     *gousb.Config
	intf       *gousb.Interface
	descriptor usbDescriptor
	product    string
	serial     string
}

func (t *gousbTransport) Control(requestType, request uint8, value, index uint16, data []byte) (int, error) {
	if err := t.ctx.Err(); err != nil {
		return 0, err
	}
	if t.device == nil {
		return 0, errors.New("USB connection is closed")
	}
	timeout := usbControlTimeout
	if deadline, ok := t.ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, context.DeadlineExceeded
		}
		if remaining < timeout {
			timeout = remaining
		}
	}
	// libusb accepts milliseconds; a sub-millisecond duration would become
	// zero (infinite timeout) in gousb.
	if timeout < time.Millisecond {
		return 0, context.DeadlineExceeded
	}
	t.device.ControlTimeout = timeout
	n, err := t.device.Control(requestType, request, value, index, data)
	if err != nil {
		return n, err
	}
	return n, t.ctx.Err()
}

func (t *gousbTransport) SetAlt(alt int) error {
	if err := t.ctx.Err(); err != nil {
		return err
	}
	if t.config == nil {
		return errors.New("USB configuration is closed")
	}
	known := false
	for _, setting := range t.descriptor.phases {
		if setting.alt == alt {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("unknown DFU alternate %d", alt)
	}
	if t.intf != nil {
		if t.intf.Setting.Alternate == alt {
			return nil
		}
		t.intf.Close()
		t.intf = nil
	}
	intf, err := t.config.Interface(t.descriptor.interfaceNumber, alt)
	if err != nil {
		return fmt.Errorf("claim DFU alternate %d: %w", alt, err)
	}
	t.intf = intf
	return nil
}

func (t *gousbTransport) releaseConfig() error {
	if t.intf != nil {
		t.intf.Close()
		t.intf = nil
	}
	if t.config != nil {
		config := t.config
		t.config = nil
		return config.Close()
	}
	return nil
}

func (t *gousbTransport) Reset() error {
	if err := t.ctx.Err(); err != nil {
		return err
	}
	if t.device == nil {
		return errors.New("USB connection is closed")
	}
	if err := t.releaseConfig(); err != nil {
		return err
	}
	return t.device.Reset()
}

func (t *gousbTransport) Close() error {
	err := t.releaseConfig()
	if t.device != nil {
		device := t.device
		t.device = nil
		err = errors.Join(err, device.Close())
	}
	return err
}

type usbContext interface {
	OpenDevices(func(*gousb.DeviceDesc) bool) ([]*gousb.Device, error)
	Close() error
}

type usbSession struct {
	ctx         context.Context
	usb         usbContext
	transport   usbTransport
	identity    usbIdentity
	descriptor  usbDescriptor
	buffer      []byte
	stage       bootStage
	dfu         *dfuClient
	reconnectFn func(context.Context, bootStage, map[uint8]usbPhaseExpectation) error
}

func openSession(ctx context.Context) (*usbSession, bootStage, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	s := &usbSession{ctx: ctx, usb: gousb.NewContext()}
	s.reconnectFn = s.reconnect
	device, err := s.findDevice(false)
	if err == nil && device == nil {
		err = errors.New("no supported STM32MP13 USB device (0483:df11) found")
	}
	if err != nil {
		return nil, 0, errors.Join(err, s.Close())
	}
	identity, err := physicalIdentity(device.Desc)
	if err != nil {
		return nil, 0, errors.Join(err, device.Close(), s.Close())
	}
	t, stage, serial, err := inspectUSB(ctx, device, nil)
	if err != nil {
		return nil, 0, errors.Join(err, s.Close())
	}
	identity.serial = serial
	s.identity = identity
	s.adopt(t, stage)
	return s, stage, nil
}

func (s *usbSession) adopt(t *gousbTransport, stage bootStage) {
	if s.dfu != nil {
		s.dfu.fail(errors.New("USB connection replaced"))
		s.dfu = nil
	}
	// Discovery may use a short-lived reconnect deadline; normal transfers use
	// the caller's original CLI context instead.
	t.ctx = s.ctx
	s.transport = t
	s.descriptor = t.descriptor
	s.buffer = make([]byte, t.descriptor.transferSize)
	s.stage = stage
}

func (s *usbSession) Close() error {
	if s.dfu != nil {
		s.dfu.fail(errors.New("USB session closed"))
		s.dfu = nil
	}
	var err error
	if s.transport != nil {
		transport := s.transport
		s.transport = nil
		err = transport.Close()
	}
	if s.usb != nil {
		usb := s.usb
		s.usb = nil
		err = errors.Join(err, usb.Close())
	}
	return err
}

// findDevice counts all VID/PID matches, including ones that cannot be opened.
// A reconnect never opens an unrelated port, even if it is the only match.
func (s *usbSession) findDevice(reconnecting bool) (*gousb.Device, error) {
	count := 0
	devices, err := s.usb.OpenDevices(func(desc *gousb.DeviceDesc) bool {
		if desc.Vendor != 0x0483 || desc.Product != 0xdf11 {
			return false
		}
		count++
		return !reconnecting || s.identity.matches(desc)
	})
	if count > 1 {
		err = errors.Join(err, fmt.Errorf("found %d STM32 DFU devices; connect exactly one", count))
	}
	if err != nil {
		for _, device := range devices {
			err = errors.Join(err, device.Close())
		}
		err = fmt.Errorf("enumerate USB devices: %w", err)
		// During an expected re-enumeration Linux can still list the node the
		// gadget is tearing down; opening it fails with NO_DEVICE until the
		// kernel removes it. Nothing was claimed, so retry the read-only scan.
		if reconnecting && count == 1 && errors.Is(err, gousb.ErrorNoDevice) {
			return nil, &usbDescriptorReadError{err}
		}
		return nil, err
	}
	if len(devices) == 0 {
		if count != 0 {
			return nil, errors.New("STM32 USB device appeared on a different physical port; refusing to reconnect")
		}
		return nil, nil
	}
	return devices[0], nil
}

// reconnect is used only after an intentional stage/layout transition. It
// closes old handles before discovery and never retries a write. A nil layout
// requires fresh-stage descriptors; only a tool-submitted map permits an
// initialized U-Boot layout. The caller must still validate GetPhase afterwards.
func (s *usbSession) reconnect(ctx context.Context, expected bootStage, layout map[uint8]usbPhaseExpectation) error {
	if s.usb == nil {
		return errors.New("USB session is closed")
	}
	validTransition := (s.stage == stageROM && expected == stageTFA && layout == nil) ||
		(s.stage == stageTFA && expected == stageUBoot && layout == nil) ||
		(s.stage == stageUBoot && expected == stageUBoot && len(layout) > 0)
	if !validTransition {
		return fmt.Errorf("unsupported USB transition from %s to %s", s.stage, expected)
	}
	if s.dfu != nil {
		s.dfu.fail(errors.New("USB connection reconnecting"))
		s.dfu = nil
	}
	if s.transport != nil {
		if err := s.transport.Close(); err != nil {
			return fmt.Errorf("close old USB connection: %w", err)
		}
		s.transport = nil
	}
	ctx, cancel := context.WithTimeout(ctx, usbReconnectTimeout)
	defer cancel()
	err := waitForUSB(ctx, func() (bool, error) {
		device, err := s.findDevice(true)
		if err != nil || device == nil {
			return false, err
		}
		t, stage, serial, inspectErr := inspectUSB(ctx, device, layout)
		// Even a partly read enumeration cannot contradict the selected UID.
		if serial != "" {
			if err := s.identity.checkSerial(serial, expected); err != nil {
				if inspectErr == nil {
					err = errors.Join(err, t.Close())
				}
				return false, err
			}
		}
		if inspectErr != nil {
			// A string/configuration read can race gadget teardown/rebuild.
			// inspectUSB has already closed the failed candidate. Retry only
			// typed read failures, never a configuration claim or DFU request.
			if retryUSBDiscovery(inspectErr) {
				return false, inspectErr
			}
			if t == nil || !sameUSBDescriptor(t.descriptor, s.descriptor) {
				return false, inspectErr
			}
			return false, s.identity.checkSerial(serial, s.stage)
		}
		if err := s.identity.checkSerial(serial, stage); err != nil {
			return false, errors.Join(err, t.Close())
		}
		if stage == expected {
			if s.identity.serial == "" {
				s.identity.serial = serial
			}
			s.adopt(t, stage)
			return true, nil
		}
		if err := t.Close(); err != nil {
			return false, err
		}
		if stage != s.stage || !sameUSBDescriptor(t.descriptor, s.descriptor) {
			return false, fmt.Errorf("expected %s after re-enumeration, found %s", expected, stage)
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("waiting for %s at USB bus %d port %v: %w", expected, s.identity.bus, s.identity.path, err)
	}
	return nil
}

func sameUSBDescriptor(a, b usbDescriptor) bool {
	if a.config != b.config || a.interfaceNumber != b.interfaceNumber || a.transferSize != b.transferSize || a.version != b.version || a.attributes != b.attributes || len(a.phases) != len(b.phases) {
		return false
	}
	for phase, alt := range a.phases {
		if other, ok := b.phases[phase]; !ok || alt != other {
			return false
		}
	}
	return true
}

var usbProductID = regexp.MustCompile(`(?i)Device ID\s*/\s*0x([0-9a-f]+)(?:\s|,|$)`)
var usbAltPattern = regexp.MustCompile(`^@([^/]+)/0[xX]([0-9a-fA-F]{2})/1\*([0-9]+)([BKM])([a-g])$`)

func parseUSBAlternate(alt int, text string) (usbAlternate, error) {
	match := usbAltPattern.FindStringSubmatch(strings.TrimSpace(text))
	if match == nil {
		return usbAlternate{}, fmt.Errorf("unsupported DFU alternate %d: %q", alt, text)
	}
	phase, _ := strconv.ParseUint(match[2], 16, 8)
	size, err := strconv.ParseInt(match[3], 10, 64)
	multiplier := int64(1)
	switch match[4] {
	case "K":
		multiplier = 1024
	case "M":
		multiplier = 1024 * 1024
	}
	if err != nil || size <= 0 || size > (1<<63-1)/multiplier {
		return usbAlternate{}, fmt.Errorf("invalid capacity in DFU alternate %q", text)
	}
	name := strings.TrimSpace(match[1])
	if name == "" || len(name) > 16 {
		return usbAlternate{}, fmt.Errorf("invalid name in DFU alternate %q", text)
	}
	return usbAlternate{alt: alt, phase: uint8(phase), name: name, capacity: size * multiplier, permissions: match[5][0]}, nil
}

func classifyUSB(phases map[uint8]usbAlternate, layout map[uint8]usbPhaseExpectation) (bootStage, error) {
	virtual, ok := phases[0xf1]
	if !ok || virtual.name != "virtual" || virtual.capacity != 512 || (virtual.permissions != 'a' && virtual.permissions != 'e') {
		return 0, errors.New("device is not a supported stm32prog service: missing virtual F1 alternate")
	}
	if _, ok := phases[0xf3]; ok {
		return 0, errors.New("SSP provisioning mode is unsupported")
	}
	if layout == nil && len(phases) == 2 && virtual.alt == 1 {
		if fsbl, ok := phases[1]; ok && fsbl.alt == 0 && fsbl.name == "FSBL" && fsbl.capacity == 128*1024 && fsbl.permissions == 'e' {
			return stageROM, nil
		}
		if ssbl, ok := phases[3]; ok && ssbl.alt == 0 && ssbl.name == "SSBL" && ssbl.capacity == 16*1024*1024 && ssbl.permissions == 'e' {
			return stageTFA, nil
		}
	}
	if virtual.permissions != 'e' {
		return 0, errors.New("unsupported ROM/TF-A alternate layout")
	}
	storageCount := 0
	for phase, alt := range phases {
		switch phase {
		case 0xf1:
			continue
		case 0xf2:
			if alt.name != "OTP" || alt.permissions != 'e' {
				return 0, errors.New("unknown virtual OTP alternate")
			}
			continue
		case 0xf4:
			if alt.name != "PMIC" || alt.permissions != 'e' {
				return 0, errors.New("unknown virtual PMIC alternate")
			}
			continue
		}
		storageCount++
		if layout == nil {
			if phase != 0 || alt.name != "FlashLayout" || alt.capacity != 256*1024 || alt.permissions != 'e' {
				return 0, errors.New("unknown or already initialized DFU layout; restart a fresh stm32prog service")
			}
		} else {
			want, ok := layout[phase]
			if !ok || phase == 0 || phase > 0xf0 || alt.name != want.name || alt.permissions != want.permissions {
				return 0, fmt.Errorf("initialized USB phase %02x does not match submitted layout", phase)
			}
		}
	}
	if (layout == nil && storageCount != 1) || (layout != nil && storageCount != len(layout)) {
		return 0, errors.New("USB phase set does not match the expected stm32prog layout")
	}
	return stageUBoot, nil
}

// inspectUSB owns device even on failure. Cached descriptors on the returned
// transport let reconnect distinguish a lingering old enumeration.
func inspectUSB(ctx context.Context, device *gousb.Device, layout map[uint8]usbPhaseExpectation) (*gousbTransport, bootStage, string, error) {
	t := &gousbTransport{ctx: ctx, device: device}
	stage, err := t.describe(layout)
	if err != nil {
		if closeErr := t.Close(); closeErr != nil {
			return nil, stage, t.serial, fmt.Errorf("USB discovery failed (%v); closing device: %w", err, closeErr)
		}
		return t, stage, t.serial, &usbDescriptorReadError{err}
	}
	if err = ctx.Err(); err == nil {
		t.config, err = device.Config(t.descriptor.config)
	}
	if err != nil {
		return nil, stage, t.serial, errors.Join(fmt.Errorf("open USB configuration: %w", err), t.Close())
	}
	return t, stage, t.serial, nil
}

// describe only reads standard USB descriptors. It deliberately does not
// configure/claim interfaces or issue DFU requests, so listing devices cannot
// disturb an existing programming transaction.
func (t *gousbTransport) describe(layout map[uint8]usbPhaseExpectation) (bootStage, error) {
	if err := t.ctx.Err(); err != nil {
		return 0, err
	}
	device := t.device
	device.ControlTimeout = usbControlTimeout
	var rawDevice [18]byte
	n, err := t.Control(0x80, 6, 0x0100, 0, rawDevice[:])
	if err != nil {
		return 0, fmt.Errorf("read USB device descriptor: %w", err)
	}
	if n != len(rawDevice) || rawDevice[0] != 18 || rawDevice[1] != 1 ||
		binary.LittleEndian.Uint16(rawDevice[8:10]) != 0x0483 ||
		binary.LittleEndian.Uint16(rawDevice[10:12]) != 0xdf11 {
		return 0, errors.New("invalid STM32 USB device descriptor")
	}
	if int(rawDevice[17]) != len(device.Desc.Configs) {
		return 0, errUSBEnumerationChanged
	}
	var languages [255]byte
	n, err = t.Control(0x80, 6, 0x0300, 0, languages[:])
	if err != nil {
		return 0, fmt.Errorf("read USB string languages: %w", err)
	}
	if n < 4 || n > len(languages) || n%2 != 0 || int(languages[0]) != n || languages[1] != 3 {
		return 0, errors.New("invalid USB string language descriptor")
	}
	language := binary.LittleEndian.Uint16(languages[2:4])
	t.product, err = readUSBString(t, rawDevice[15], language)
	if err != nil {
		return 0, fmt.Errorf("read USB product: %w", err)
	}
	if strings.Contains(strings.ToLower(t.product), "device id") {
		match := usbProductID.FindStringSubmatch(t.product)
		if match == nil {
			return 0, fmt.Errorf("unrecognized USB product device ID: %q", t.product)
		}
		id, parseErr := strconv.ParseUint(match[1], 16, 32)
		if parseErr != nil || id != 0x501 {
			return 0, fmt.Errorf("unsupported USB product %q; STM32MP13 device ID 0x501 required", t.product)
		}
	}
	if err := t.ctx.Err(); err != nil {
		return 0, err
	}
	t.serial, err = readUSBString(t, rawDevice[16], language)
	if err != nil {
		return 0, fmt.Errorf("read USB serial: %w", err)
	}
	t.serial = strings.TrimSpace(t.serial)
	var selected *gousb.InterfaceDesc
	// Runtime mode is a discovery candidate, never a usable DFU session.
	// U-Boot exposes it between DETACH and rebuilding the DFU gadget.
	for _, config := range device.Desc.Configs {
		for _, intf := range config.Interfaces {
			for _, alt := range intf.AltSettings {
				if alt.Class != 0xfe || alt.SubClass != 1 || (alt.Protocol != 1 && alt.Protocol != 2) {
					continue
				}
				if selected != nil {
					return 0, errors.New("ambiguous device: multiple DFU interfaces/configurations")
				}
				copy := intf
				selected = &copy
				t.descriptor.config = config.Number
				t.descriptor.interfaceNumber = intf.Number
				break
			}
		}
	}
	if selected == nil {
		return 0, errors.New("USB device has no DFU mode interface FE/01/02")
	}
	if err := readUSBFunctional(t, len(device.Desc.Configs)); err != nil {
		return 0, err
	}
	if len(t.descriptor.alternateStrings) != len(selected.AltSettings) {
		return 0, errUSBEnumerationChanged
	}
	t.descriptor.phases = make(map[uint8]usbAlternate, len(selected.AltSettings))
	for _, alt := range selected.AltSettings {
		if err := t.ctx.Err(); err != nil {
			return 0, err
		}
		if alt.Class == 0xfe && alt.SubClass == 1 && alt.Protocol == 1 {
			// The live configuration passed DFU-mode validation above.
			return 0, fmt.Errorf("%w: cached interface %d alternate %d is still in DFU runtime mode",
				errUSBEnumerationChanged, selected.Number, alt.Alternate)
		}
		if alt.Class != 0xfe || alt.SubClass != 1 || alt.Protocol != 2 {
			return 0, fmt.Errorf("mixed protocols in DFU interface: cached configuration %d interface %d alternate %d has %02x/%02x/%02x; require fe/01/02",
				t.descriptor.config, selected.Number, alt.Alternate, uint8(alt.Class), uint8(alt.SubClass), uint8(alt.Protocol))
		}
		stringIndex, ok := t.descriptor.alternateStrings[alt.Alternate]
		if !ok {
			return 0, errUSBEnumerationChanged
		}
		text, err := readUSBString(t, stringIndex, language)
		if err != nil {
			return 0, fmt.Errorf("read DFU alternate string: %w", err)
		}
		entry, err := parseUSBAlternate(alt.Alternate, text)
		if err != nil {
			return 0, err
		}
		if _, exists := t.descriptor.phases[entry.phase]; exists {
			return 0, fmt.Errorf("duplicate USB phase %02x", entry.phase)
		}
		t.descriptor.phases[entry.phase] = entry
	}
	stage, err := classifyUSB(t.descriptor.phases, layout)
	if err != nil {
		return 0, err
	}
	// gousb v1.1.3 indexes Config.Desc.Interfaces by interface number while
	// claiming; reject sparse numbering instead of risking an indexing panic.
	configDesc := device.Desc.Configs[t.descriptor.config]
	if selected.Number < 0 || selected.Number >= len(configDesc.Interfaces) || configDesc.Interfaces[selected.Number].Number != selected.Number {
		return 0, errors.New("unsupported sparse USB interface numbering")
	}
	return stage, t.ctx.Err()
}

func readUSBFunctional(t *gousbTransport, configCount int) error {
	for index := range configCount {
		var header [9]byte
		n, err := t.Control(0x80, 6, 0x0200|uint16(index), 0, header[:])
		if err != nil {
			return fmt.Errorf("read USB configuration header: %w", err)
		}
		if n != len(header) || header[0] != 9 || header[1] != 2 {
			return errors.New("invalid USB configuration header")
		}
		total := int(binary.LittleEndian.Uint16(header[2:4]))
		if total < len(header) {
			return errors.New("invalid USB configuration length")
		}
		if int(header[5]) != t.descriptor.config {
			continue
		}
		data := make([]byte, total)
		n, err = t.Control(0x80, 6, 0x0200|uint16(index), 0, data)
		if err != nil {
			return fmt.Errorf("read USB configuration: %w", err)
		}
		if n != total {
			return errors.New("truncated USB configuration descriptor")
		}
		return parseUSBFunctional(data, &t.descriptor)
	}
	return errors.New("selected USB configuration descriptor not found")
}

func parseUSBFunctional(data []byte, descriptor *usbDescriptor) error {
	if len(data) < 9 || data[0] != 9 || data[1] != 2 || int(data[5]) != descriptor.config || int(binary.LittleEndian.Uint16(data[2:4])) != len(data) {
		return errors.New("invalid USB configuration descriptor")
	}
	currentInterface := -1
	isDFU := false
	found := false
	var selectedProtocol uint8
	descriptor.alternateStrings = make(map[int]uint8)
	var functional [7]byte
	for offset := 9; offset < len(data); {
		if len(data)-offset < 2 {
			return errors.New("truncated USB descriptor header")
		}
		length := int(data[offset])
		if length < 2 || length > len(data)-offset {
			return errors.New("invalid USB descriptor length")
		}
		entry := data[offset : offset+length]
		switch entry[1] {
		case 4:
			if length != 9 {
				return errors.New("invalid USB interface descriptor length")
			}
			currentInterface = int(entry[2])
			isDFU = entry[5] == 0xfe && entry[6] == 1 && (entry[7] == 1 || entry[7] == 2)
			if currentInterface == descriptor.interfaceNumber {
				if !isDFU || (selectedProtocol != 0 && selectedProtocol != entry[7]) {
					return fmt.Errorf("mixed protocols in DFU interface: live configuration %d interface %d alternate %d has %02x/%02x/%02x; require fe/01/02",
						descriptor.config, currentInterface, entry[3], entry[5], entry[6], entry[7])
				}
				if entry[7] == 1 && (entry[3] != 0 || entry[4] != 0) {
					return errors.New("unsupported DFU runtime interface: requires alternate zero without endpoints")
				}
				selectedProtocol = entry[7]
				alt := int(entry[3])
				if _, exists := descriptor.alternateStrings[alt]; exists {
					return errors.New("duplicate DFU alternate descriptor")
				}
				descriptor.alternateStrings[alt] = entry[8]
			}
		case 0x21:
			if currentInterface == descriptor.interfaceNumber && isDFU {
				if length != 9 {
					return errors.New("invalid DFU functional descriptor length")
				}
				if found && !slices.Equal(functional[:], entry[2:]) {
					return errors.New("conflicting DFU functional descriptors")
				}
				copy(functional[:], entry[2:])
				found = true
			}
		}
		offset += length
	}
	// Validate the entire descriptor stream before treating runtime mode as
	// transient; malformed or genuinely mixed interfaces remain fatal.
	if selectedProtocol == 1 {
		return fmt.Errorf("configuration %d interface %d: %w", descriptor.config, descriptor.interfaceNumber, errUSBRuntimeMode)
	}
	if !found {
		return errors.New("DFU functional descriptor missing from selected interface")
	}
	descriptor.attributes = functional[0]
	descriptor.detachTimeout = binary.LittleEndian.Uint16(functional[1:3])
	descriptor.transferSize = int(binary.LittleEndian.Uint16(functional[3:5]))
	descriptor.version = binary.LittleEndian.Uint16(functional[5:7])
	if descriptor.version != 0x0110 || descriptor.transferSize == 0 || descriptor.transferSize > 4096 || descriptor.attributes&3 != 3 {
		return fmt.Errorf("unsupported DFU capabilities: version %04x, transfer size %d, attributes %02x", descriptor.version, descriptor.transferSize, descriptor.attributes)
	}
	return nil
}

// gousb v1.1.3's string helper formats libusb errors with %s, discarding
// their type, and uses its own timeout. Standard GET_DESCRIPTOR through our
// bounded transport preserves typed errors and live string indices.
func readUSBString(transport usbTransport, index uint8, language uint16) (string, error) {
	if index == 0 {
		return "", nil
	}
	var raw [255]byte
	n, err := transport.Control(0x80, 6, 0x0300|uint16(index), language, raw[:])
	if err != nil {
		return "", fmt.Errorf("read USB string descriptor %d: %w", index, err)
	}
	if n < 2 || n > len(raw) || n%2 != 0 || int(raw[0]) != n || raw[1] != 3 {
		return "", fmt.Errorf("invalid USB string descriptor %d", index)
	}
	var units [126]uint16
	for i := range (n - 2) / 2 {
		units[i] = binary.LittleEndian.Uint16(raw[2+i*2:])
	}
	return string(utf16.Decode(units[:(n-2)/2])), nil
}

var errUSBEnumerationChanged = errors.New("USB configuration changed during enumeration")
var errUSBRuntimeMode = errors.New("USB interface is in DFU runtime mode (FE/01/01), not DFU mode")

// Only failed descriptor reads or a single-node NO_DEVICE open failure during
// an expected reconnect are retryable read-only discovery.
// Configuration claims, DFU transfers and writes never use this error type.
type usbDescriptorReadError struct{ error }

func (e *usbDescriptorReadError) Unwrap() error { return e.error }

func retryUSBDiscovery(err error) bool {
	var descriptorErr *usbDescriptorReadError
	if !errors.As(err, &descriptorErr) {
		return false
	}
	// Darwin maps not-responding/aborted control requests to ErrorOther.
	// Gadget re-enumeration can surface that, I/O failure, or a stall.
	// Only read-only discovery retries; persistent faults hit the deadline.
	return errors.Is(err, gousb.ErrorPipe) ||
		errors.Is(err, gousb.ErrorIO) ||
		errors.Is(err, gousb.ErrorOther) ||
		errors.Is(err, gousb.ErrorNoDevice) ||
		errors.Is(err, gousb.ErrorTimeout) ||
		errors.Is(err, errUSBEnumerationChanged) ||
		errors.Is(err, errUSBRuntimeMode)
}

func waitForUSB(ctx context.Context, attempt func() (bool, error)) error {
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return errors.Join(err, lastErr)
		}
		ready, err := attempt()
		if err != nil {
			if !retryUSBDiscovery(err) {
				return err
			}
			lastErr = err
		} else if ready {
			return nil
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(ctx.Err(), lastErr)
		case <-timer.C:
		}
	}
}

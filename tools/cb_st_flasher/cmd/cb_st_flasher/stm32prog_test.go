package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"proto-distro/tools/flashimage"
	"strconv"
	"strings"
	"testing"

	"github.com/google/gousb"
)

// Models the pinned U-Boot shared transaction, deferred flush, next-phase and
// read-offset rules. It is not evidence of hardware compatibility.
type storageTarget struct {
	state                                     byte
	alt                                       int
	sequence                                  uint16
	pending                                   []byte
	live                                      bool
	phase                                     byte
	offset                                    uint32
	parsed, initialized                       bool
	plan                                      flashPlan
	storage                                   map[byte][]byte
	phases                                    map[byte]usbAlternate
	readOffset                                int
	layoutDetach, finalDetach, reset          bool
	payloadWrites                             int
	failStatus, failVirtual, mismatch, badGPT bool
	rejectReadback                            bool
	finalDetachError                          error
}

func (m *storageTarget) SetAlt(alt int) error {
	if m.live || m.state != 2 {
		return errors.New("alternate switched during live backend transaction")
	}
	m.alt = alt
	return nil
}
func (m *storageTarget) Close() error { return nil }
func (m *storageTarget) Reset() error { m.reset = true; return gousb.ErrorTimeout }
func (m *storageTarget) selected() byte {
	for phase, alt := range m.phases {
		if alt.alt == m.alt {
			return phase
		}
	}
	panic("unknown alternate")
}
func (m *storageTarget) flush() error {
	phase := m.selected()
	switch phase {
	case 0:
		lines := strings.Split(strings.TrimSpace(string(m.pending)), "\n")
		m.plan = flashPlan{}
		for _, line := range lines {
			columns := strings.Split(line, "\t")
			if len(columns) != 6 {
				return errors.New("target expected six layout columns")
			}
			id, err := strconv.ParseUint(columns[1], 0, 8)
			if err != nil {
				return err
			}
			row := flashimage.Partition{Phase: byte(id), Name: columns[2], DFUType: columns[3]}
			switch columns[5] {
			case "boot1":
				row.Area = flashimage.AreaBoot1
			case "boot2":
				row.Area = flashimage.AreaBoot2
			default:
				row.Offset, err = strconv.ParseInt(columns[5], 0, 64)
				if err != nil {
					return err
				}
			}
			m.plan.partitions = append(m.plan.partitions, partition{Partition: row, operation: columns[0], device: columns[4]})
		}
		m.parsed = true
	case 0xf1:
		if len(m.pending) != 5 {
			return errors.New("invalid SetPhase length")
		}
		m.phase = m.pending[0]
		m.offset = binary.BigEndian.Uint32(m.pending[1:])
	default:
		if phase != m.phase {
			return errors.New("wrong current partition")
		}
		data := m.storage[phase]
		if uint64(m.offset)+uint64(len(m.pending)) > uint64(len(data)) {
			return errors.New("target write out of bounds")
		}
		copy(data[int(m.offset):], m.pending)
		if m.mismatch {
			data[int(m.offset)] ^= 0xff
		}
		m.payloadWrites++
		m.phase = m.plan.nextPhase(phase)
	}
	m.pending = nil
	m.live = false
	m.sequence = 0
	return nil
}
func (m *storageTarget) Control(rt, request uint8, value, index uint16, data []byte) (int, error) {
	if index != 3 {
		return 0, errors.New("wrong actual interface index")
	}
	switch request {
	case 3:
		if rt != 0xa1 || len(data) != 6 {
			return 0, errors.New("bad GETSTATUS")
		}
		if m.failStatus && m.initialized && m.selected() != 0xf1 && m.live {
			return 0, errors.New("injected status failure")
		}
		switch m.state {
		case 3:
			m.state = 4
		case 4:
			m.state = 5
		case 6:
			m.state = 7
		case 7:
			if err := m.flush(); err != nil {
				return 0, err
			}
			m.state = 2
		}
		clear(data)
		data[4] = m.state
		return 6, nil
	case 5:
		data[0] = m.state
		return 1, nil
	case 1:
		if rt != 0x21 || (m.state != 2 && m.state != 5) || value != m.sequence {
			return 0, errors.New("invalid DNLOAD state/sequence")
		}
		if len(data) == 0 {
			if !m.live {
				return 0, errors.New("empty initial download")
			}
			m.state = 6
		} else {
			m.live = true
			m.pending = append(m.pending, data...)
			m.sequence++
			m.state = 3
		}
		return len(data), nil
	case 2:
		if rt != 0xa1 || (m.state != 2 && m.state != 9) || value != m.sequence {
			return 0, errors.New("invalid UPLOAD state/sequence")
		}
		phase := m.selected()
		if phase == 0xf1 {
			if m.live {
				return 0, errors.New("virtual request while entity live")
			}
			clear(data)
			data[0] = m.phase
			binary.LittleEndian.PutUint32(data[1:5], 0xffffffff)
			binary.LittleEndian.PutUint32(data[5:9], m.offset)
			if m.badGPT && m.initialized {
				data[0] = 0xff
				copy(data[9:], "GPT mismatch")
				return 21, nil
			}
			if m.failVirtual && m.payloadWrites > 0 {
				data[0] = 0xff
				copy(data[9:], "media failure")
				return 22, nil
			}
			if m.phase == 0 {
				if m.parsed {
					data[9] = 1
				}
				return 10, nil
			}
			return 9, nil
		}
		if m.rejectReadback {
			return 0, errors.New("partition readback unavailable")
		}
		if !m.live {
			m.live = true
			m.readOffset = 0
			if phase == m.phase {
				m.readOffset = int(m.offset)
			}
		}
		source := m.storage[phase]
		n := copy(data, source[m.readOffset:])
		m.readOffset += n
		m.sequence++
		if n < len(data) {
			m.live = false
			m.sequence = 0
			m.state = 2
		} else {
			m.state = 9
		}
		return n, nil
	case 0:
		if rt != 0x21 || m.live || m.state != 2 {
			return 0, errors.New("detach before clean completion")
		}
		if !m.initialized {
			m.layoutDetach = true
		} else {
			m.finalDetach = true
			return 0, m.finalDetachError
		}
		return 0, nil
	default:
		return 0, fmt.Errorf("unsupported DFU request %d (ABORT/recovery forbidden)", request)
	}
}

func newStorageSession(t *testing.T, capacities map[byte]int) (*usbSession, *storageTarget) {
	t.Helper()
	m := &storageTarget{state: 2, storage: make(map[byte][]byte), phases: map[byte]usbAlternate{0: {alt: 4, phase: 0, name: "FlashLayout", capacity: 256 * 1024, permissions: 'e'}, 0xf1: {alt: 7, phase: 0xf1, name: "virtual", capacity: 512, permissions: 'e'}}}
	for phase, size := range capacities {
		m.storage[phase] = bytes.Repeat([]byte{0xa5}, size)
	}
	s := &usbSession{ctx: context.Background(), transport: m, stage: stageUBoot, buffer: make([]byte, 4096), descriptor: usbDescriptor{interfaceNumber: 3, transferSize: 4096, phases: m.phases}}
	s.reconnectFn = func(ctx context.Context, stage bootStage, expected map[byte]usbPhaseExpectation) error {
		if stage != stageUBoot || !m.layoutDetach {
			return errors.New("unexpected reconnect")
		}
		m.initialized = true
		m.phases = map[byte]usbAlternate{0xf1: {alt: 23, phase: 0xf1, name: "virtual", capacity: 512, permissions: 'e'}}
		for i, p := range m.plan.partitions {
			want, ok := expected[p.Phase]
			if !ok || want.name != p.Name {
				return errors.New("different submitted partition")
			}
			m.phases[p.Phase] = usbAlternate{alt: i + 5, phase: p.Phase, name: p.Name, capacity: int64(len(m.storage[p.Phase])), permissions: want.permissions}
		}
		s.descriptor.phases = m.phases
		m.phase = m.plan.nextPhase(0)
		m.alt = 23
		s.dfu = nil
		return nil
	}
	if err := s.startDFU(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	return s, m
}

func testStoragePlan() flashPlan {
	return flashPlan{partitions: []partition{
		{Partition: flashimage.Partition{Phase: 0x0b, Name: "bootfs", DFUType: "System", Capacity: flashimage.CapacityBounded, ArtifactKey: "bootfs"}, device: "mmc1", operation: "P", capacity: 8192},
		{Partition: flashimage.Partition{Phase: 0x0c, Name: "rootfs_a", DFUType: "FileSystem", Offset: 0x2000, ArtifactKey: "rootfs_a", AVB: true}, device: "mmc1", operation: "P"},
	}}
}

func testPayload(p partition, source io.ReaderAt, size int64, ranges ...[2]int64) payload {
	total, _ := paddedSize(size)
	item := payload{part: p, size: size}
	for _, x := range ranges {
		item.extents = append(item.extents, extent{offset: x[0], length: x[1], source: paddedImage{source, size, total}, sourceOffset: x[0]})
	}
	return item
}

func TestWholeAndExtentWritesVerifyTrueEOF(t *testing.T) {
	for _, optimized := range []bool{false, true} {
		t.Run(fmt.Sprint(optimized), func(t *testing.T) {
			s, m := newStorageSession(t, map[byte]int{0x0b: 8192, 0x0c: 8192})
			plan := testStoragePlan()
			source := bytes.Repeat([]byte{0x4b}, 8192)
			clear(source[512:7168])
			ranges := [][2]int64{{0, 8192}}
			if optimized {
				ranges = [][2]int64{{0, 512}, {7168, 1024}}
			}
			// The second payload is deliberately not a sector multiple.
			second := bytes.Repeat([]byte{0x29}, 97)
			images := []payload{testPayload(plan.partitions[0], bytes.NewReader(source), int64(len(source)), ranges...), testPayload(plan.partitions[1], bytes.NewReader(second), 97, [2]int64{0, 512})}
			if err := (&stm32progClient{session: s}).program(context.Background(), plan, images); err != nil {
				t.Fatal(err)
			}
			expected := append([]byte(nil), source...)
			if optimized {
				copy(expected[512:7168], bytes.Repeat([]byte{0xa5}, 6656))
			}
			if !bytes.Equal(m.storage[0x0b], expected) {
				t.Fatal("whole/extent contents differ")
			}
			if !bytes.Equal(m.storage[0x0c][:97], second) || !bytes.Equal(m.storage[0x0c][97:512], make([]byte, 415)) {
				t.Fatal("following image did not start at zero with deterministic padding")
			}
			if m.live || !m.finalDetach || m.reset {
				t.Fatal("verification must reach terminal EOF and detach without resetting the departed gadget")
			}
		})
	}
}

func TestReselectPhaseAfterFE(t *testing.T) {
	s, m := newStorageSession(t, map[byte]int{0x0c: 8192})
	plan := flashPlan{partitions: []partition{testStoragePlan().partitions[1]}}
	image := bytes.Repeat([]byte{0x31}, 8192)
	if err := (&stm32progClient{session: s}).program(context.Background(), plan, []payload{testPayload(plan.partitions[0], bytes.NewReader(image), 8192, [2]int64{0, 512}, [2]int64{7680, 512})}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(m.storage[0x0c][:512], image[:512]) || !bytes.Equal(m.storage[0x0c][7680:], image[7680:]) || m.payloadWrites != 2 {
		t.Fatal("FE phase reselection failed")
	}
}

func TestStorageFailureStopsWritesAndFinalization(t *testing.T) {
	for _, failure := range []string{"status", "virtual", "mismatch", "gpt"} {
		t.Run(failure, func(t *testing.T) {
			s, m := newStorageSession(t, map[byte]int{0x0b: 8192, 0x0c: 8192})
			m.failStatus = failure == "status"
			m.failVirtual = failure == "virtual"
			m.mismatch = failure == "mismatch"
			m.badGPT = failure == "gpt"
			plan := testStoragePlan()
			images := []payload{}
			for _, p := range plan.partitions {
				images = append(images, testPayload(p, bytes.NewReader(make([]byte, 512)), 512, [2]int64{0, 512}))
			}
			if err := (&stm32progClient{session: s}).program(context.Background(), plan, images); err == nil {
				t.Fatal("failure accepted")
			}
			if m.finalDetach || m.reset || !bytes.Equal(m.storage[0x0c], bytes.Repeat([]byte{0xa5}, 8192)) {
				t.Fatal("failure continued to another partition or reset")
			}
			if failure == "gpt" && m.payloadWrites != 0 {
				t.Fatal("GPT failure written through")
			}
		})
	}
}

func TestUnselectedStorageAndReservedPhases(t *testing.T) {
	s, m := newStorageSession(t, map[byte]int{0x0b: 8192, 0x0c: 8192})
	plan := testStoragePlan()
	plan.partitions[0].operation = "-"
	item := testPayload(plan.partitions[1], bytes.NewReader(make([]byte, 512)), 512, [2]int64{0, 512})
	if err := (&stm32progClient{session: s}).program(context.Background(), plan, []payload{item}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(m.storage[0x0b], bytes.Repeat([]byte{0xa5}, 8192)) {
		t.Fatal("unselected partition modified")
	}
}

func TestPayloadBoundsBeforeLayout(t *testing.T) {
	s, m := newStorageSession(t, map[byte]int{0x0b: 8192, 0x0c: 8192})
	plan := testStoragePlan()
	images := []payload{testPayload(plan.partitions[0], bytes.NewReader(make([]byte, 8193)), 8193, [2]int64{0, 8704}), testPayload(plan.partitions[1], bytes.NewReader(make([]byte, 512)), 512, [2]int64{0, 512})}
	if err := (&stm32progClient{session: s}).program(context.Background(), plan, images); err == nil {
		t.Fatal("oversized payload accepted")
	}
	if m.parsed || m.layoutDetach || m.payloadWrites != 0 {
		t.Fatal("validation mutated target")
	}
}

func TestPaddedImageReadBoundary(t *testing.T) {
	image := paddedImage{bytes.NewReader([]byte{1, 2, 3}), 3, 512}
	output := make([]byte, 513)
	n, err := image.ReadAt(output, 0)
	if n != 512 || err != io.EOF || !bytes.Equal(output[:3], []byte{1, 2, 3}) || !bytes.Equal(output[3:], make([]byte, 510)) {
		t.Fatal("incorrect final sector/EOF")
	}
}

func TestEraseOnlyCompletionDoesNotAssumeZeroReadback(t *testing.T) {
	s, m := newStorageSession(t, map[byte]int{0x0a: 8192})
	plan := flashPlan{partitions: []partition{{Partition: flashimage.Partition{Phase: 0x0a, Name: "u-boot-env", DFUType: "Binary", Offset: 0x980000}, device: "mmc1", operation: "PDE"}}}
	if err := (&stm32progClient{session: s}).program(context.Background(), plan, nil); err != nil {
		t.Fatal(err)
	}
	if m.payloadWrites != 0 || !m.finalDetach || m.reset || m.readOffset != 0 {
		t.Fatal("erase-only completion performed payload IO or reset")
	}
}

func TestUnverifiedProgrammingWritesCompletePayloadsWithoutReadingStorage(t *testing.T) {
	s, m := newStorageSession(t, map[byte]int{0x0b: 8192, 0x0c: 8192})
	m.rejectReadback = true
	plan := testStoragePlan()
	first := bytes.Repeat([]byte{0x5a}, 8192)
	clear(first[512:7168])
	second := bytes.Repeat([]byte{0x39}, 97)
	images := []payload{
		testPayload(plan.partitions[0], bytes.NewReader(first), int64(len(first)), [2]int64{0, 8192}),
		testPayload(plan.partitions[1], bytes.NewReader(second), int64(len(second)), [2]int64{0, 512}),
	}
	if err := (&stm32progClient{session: s, skipVerify: true}).program(context.Background(), plan, images); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(m.storage[0x0b], first) || !bytes.Equal(m.storage[0x0c][:97], second) ||
		!bytes.Equal(m.storage[0x0c][97:512], make([]byte, 415)) {
		t.Fatal("disabling verification changed payload bytes or sector padding")
	}
	if m.live || m.phase != 0xfe || !m.finalDetach || m.reset {
		t.Fatal("unverified programming did not finish cleanly with DETACH only")
	}
}

func TestUnverifiedProgrammingStillStopsOnTargetFailure(t *testing.T) {
	s, m := newStorageSession(t, map[byte]int{0x0b: 8192, 0x0c: 8192})
	m.failStatus, m.rejectReadback = true, true
	plan := testStoragePlan()
	var images []payload
	for _, p := range plan.partitions {
		images = append(images, testPayload(p, bytes.NewReader(make([]byte, 512)), 512, [2]int64{0, 512}))
	}
	if err := (&stm32progClient{session: s, skipVerify: true}).program(context.Background(), plan, images); err == nil {
		t.Fatal("disabling readback ignored a target status failure")
	}
	if m.finalDetach || m.reset || !bytes.Equal(m.storage[0x0c], bytes.Repeat([]byte{0xa5}, 8192)) {
		t.Fatal("target failure continued into another partition or finalized")
	}
}

func TestFinalDetachTimeoutRemainsFatal(t *testing.T) {
	s, m := newStorageSession(t, map[byte]int{0x0b: 8192})
	m.finalDetachError = gousb.ErrorTimeout
	plan := flashPlan{partitions: testStoragePlan().partitions[:1]}
	source := bytes.Repeat([]byte{0x71}, 512)
	item := testPayload(plan.partitions[0], bytes.NewReader(source), 512, [2]int64{0, 512})
	err := (&stm32progClient{session: s}).program(context.Background(), plan, []payload{item})
	if !errors.Is(err, gousb.ErrorTimeout) || m.live || m.reset {
		t.Fatalf("final DETACH timeout was ignored or followed by reset: %v", err)
	}
}

func TestInvalidFileRangesFailBeforeLayout(t *testing.T) {
	for _, name := range []string{"missing source", "negative source offset", "source overflow", "zero length", "negative length", "negative offset", "unaligned offset", "unaligned length", "beyond partition", "overlap"} {
		t.Run(name, func(t *testing.T) {
			plan := testStoragePlan()
			plan.partitions[1].operation = "-"
			item := testPayload(plan.partitions[0], bytes.NewReader(make([]byte, 8192)), 8192, [2]int64{0, 512})
			x := &item.extents[0]
			switch name {
			case "missing source":
				x.source = nil
			case "negative source offset":
				x.sourceOffset = -1
			case "source overflow":
				x.sourceOffset = 1<<63 - 256
			case "zero length":
				x.length = 0
			case "negative length":
				x.length = -1 << 63
			case "negative offset":
				x.offset = -512
			case "unaligned offset":
				x.offset = 1
			case "unaligned length":
				x.length = 513
			case "beyond partition":
				x.offset = 8192
			case "overlap":
				item.extents = append(item.extents, *x)
			}
			session, target := newStorageSession(t, map[byte]int{0x0b: 8192, 0x0c: 8192})
			if err := (&stm32progClient{session: session}).program(context.Background(), plan, []payload{item}); err == nil {
				t.Fatal("invalid file range accepted")
			}
			if target.parsed || target.layoutDetach || target.payloadWrites != 0 {
				t.Fatal("validation mutated target")
			}
		})
	}
}

type shortReadbackSource struct{ reads int }

func (r *shortReadbackSource) ReadAt(p []byte, _ int64) (int, error) {
	clear(p)
	r.reads++
	if r.reads > 1 {
		return len(p) - 1, nil
	}
	return len(p), nil
}

func TestFileRangeSourceFailureStopsFinalization(t *testing.T) {
	for _, readback := range []bool{false, true} {
		t.Run(fmt.Sprint(readback), func(t *testing.T) {
			plan := testStoragePlan()
			plan.partitions[1].operation = "-"
			var source io.ReaderAt = bytes.NewReader(make([]byte, 511))
			if readback {
				source = &shortReadbackSource{}
			}
			item := payload{part: plan.partitions[0], size: 512, extents: []extent{{offset: 0, length: 512, source: source}}}
			session, target := newStorageSession(t, map[byte]int{0x0b: 8192, 0x0c: 8192})
			if err := (&stm32progClient{session: session}).program(context.Background(), plan, []payload{item}); !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("short source: %v", err)
			}
			if target.finalDetach || target.reset || (!readback && target.payloadWrites != 0) {
				t.Fatal("source failure programmed or finalized target")
			}
		})
	}
}

func TestStorageCapacityBounds(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		fixed, advertised      int64
		storage, payload       int
		wantError, beforeWrite bool
	}{
		{"rounded fixed capacity", 8704, 8192, 8704, 512, false, false},
		{"fixed descriptor disagreement", 8704, 7168, 8704, 512, true, true},
		{"fixed readback EOF", 8704, 8192, 8192, 512, true, false},
		{"unknown capacity bound", 0, 8192, 8704, 8704, true, true},
		{"unknown capacity readback", 0, 8192, 8192, 512, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := flashPlan{partitions: testStoragePlan().partitions[:1]}
			plan.partitions[0].capacity = tc.fixed
			if tc.fixed == 0 {
				plan.partitions[0].Capacity = flashimage.CapacityUnknown
			}
			session, target := newStorageSession(t, map[byte]int{0x0b: tc.storage})
			reconnect := session.reconnectFn
			session.reconnectFn = func(ctx context.Context, stage bootStage, expected map[byte]usbPhaseExpectation) error {
				if err := reconnect(ctx, stage, expected); err != nil {
					return err
				}
				alt := session.descriptor.phases[0x0b]
				alt.capacity = tc.advertised
				session.descriptor.phases[0x0b] = alt
				return nil
			}
			item := testPayload(plan.partitions[0], bytes.NewReader(make([]byte, tc.payload)), int64(tc.payload), [2]int64{0, int64(tc.payload)})
			err := (&stm32progClient{session: session}).program(context.Background(), plan, []payload{item})
			if (err != nil) != tc.wantError {
				t.Fatalf("capacity check: %v", err)
			}
			if tc.wantError && target.finalDetach {
				t.Fatal("capacity failure finalized target")
			}
			if tc.beforeWrite && target.payloadWrites != 0 {
				t.Fatal("capacity failure programmed target")
			}
		})
	}
}

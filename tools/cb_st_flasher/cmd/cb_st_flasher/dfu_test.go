package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"
)

// This model retains a live transaction until manifestation/true EOF, as in
// pinned U-Boot dfu.c. It does not implement ABORT as backend cleanup. It also
// models TF-A's two-query sync progression and one-ms manifestation delay.
type dfuTarget struct {
	state          byte
	status         byte
	stringIndex    byte
	delayMS        uint32
	alt            int
	block          uint16
	pending        []byte
	storage        []byte
	written        int64
	readOffset     int64
	streamSize     int64
	stream         bool
	manifesting    bool
	tfa            bool
	detached       bool
	closed         bool
	calls          int
	selections     int
	requests       [7]int
	failRequest    uint8
	failAt         int
	failErr        error
	shortRequest   uint8
	shortAt        int
	manifestStatus byte
	stall          bool
	statusObserved chan struct{}
}

func (m *dfuTarget) SetAlt(alt int) error {
	m.selections++
	if m.closed || m.detached || m.state != 2 {
		return errors.New("alternate switch during live/closed transaction")
	}
	m.alt = alt
	m.block = 0
	m.readOffset = 0
	return nil
}

func (m *dfuTarget) Control(direction, request uint8, value, index uint16, data []byte) (int, error) {
	m.calls++
	if m.closed || m.detached || index != 3 || int(request) >= len(m.requests) {
		return 0, errors.New("invalid control recipient")
	}
	m.requests[request]++
	wantDirection := uint8(0x21)
	if request == 2 || request == 3 {
		wantDirection = 0xa1
	}
	if direction != wantDirection {
		return 0, errors.New("wrong DFU direction")
	}
	// USB faults may occur after the target accepted a request. Faults are
	// returned below, after state/storage changes, so replay is unsafe.
	n := len(data)
	switch request {
	case 0:
		if m.state != 2 || len(data) != 0 {
			return 0, errors.New("detach before completion")
		}
		m.detached = true
	case 1:
		if (m.state != 2 && m.state != 5) || value != m.block {
			return 0, fmt.Errorf("download state=%d block=%d expected=%d", m.state, value, m.block)
		}
		m.block++
		if len(data) == 0 {
			if m.state != 5 {
				return 0, errors.New("empty transaction")
			}
			m.state = 6
			m.manifesting = true
		} else {
			if m.stream {
				for i, b := range data {
					if b != dfuPattern(m.written+int64(i)) {
						return 0, fmt.Errorf("wrong stream byte at %d", m.written+int64(i))
					}
				}
			} else {
				m.pending = append(m.pending, data...)
			}
			m.written += int64(len(data))
			m.state = 3
		}
	case 2:
		if (m.state != 2 && m.state != 9) || value != m.block {
			return 0, fmt.Errorf("upload state=%d block=%d expected=%d", m.state, value, m.block)
		}
		m.block++
		size := int64(len(m.storage))
		if m.stream {
			size = m.streamSize
		}
		n = int(min(int64(len(data)), size-m.readOffset))
		if m.stream {
			for i := range n {
				data[i] = dfuPattern(m.readOffset + int64(i))
			}
		} else {
			copy(data, m.storage[m.readOffset:m.readOffset+int64(n)])
		}
		m.readOffset += int64(n)
		m.state = 9
		if n < len(data) {
			m.state = 2
			m.block = 0
		}
	case 3:
		if len(data) != 6 {
			return 0, errors.New("GETSTATUS must request all six bytes")
		}
		state := m.state
		if !m.stall {
			switch m.state {
			case 3:
				m.state = 4
				if m.tfa {
					m.state = 5
				}
			case 4:
				m.state = 5
			case 6:
				m.state = 7
				if m.tfa {
					state = 7
					m.state = 2
				}
			case 7:
				m.state = 2
			case 2:
				if m.manifesting {
					m.storage = append(m.storage[:0], m.pending...)
					m.pending = nil
					m.manifesting = false
					m.block = 0
				}
			}
		}
		status := m.status
		if m.manifesting && m.manifestStatus != 0 {
			status = m.manifestStatus
		}
		delay := m.delayMS
		if m.tfa && state == 7 {
			delay = 1
		}
		copy(data, []byte{status, byte(delay), byte(delay >> 8), byte(delay >> 16), state, m.stringIndex})
		if state != 2 && m.statusObserved != nil {
			select {
			case m.statusObserved <- struct{}{}:
			default:
			}
		}
	case 4:
		if m.state != 10 || m.written != 0 {
			return 0, errors.New("CLRSTATUS outside fresh error state")
		}
		m.state, m.status = 2, 0
	default:
		return 0, errors.New("unsafe or unsupported DFU request")
	}
	if request == m.failRequest && m.requests[request] == m.failAt {
		return n, m.failErr
	}
	if request == m.shortRequest && m.requests[request] == m.shortAt {
		return n - 1, nil
	}
	return n, nil
}

func (m *dfuTarget) Reset() error { return errors.New("unexpected reset") }
func (m *dfuTarget) Close() error { m.closed = true; return nil }

func dfuPattern(offset int64) byte {
	return byte((offset*37 + offset/251) % 256)
}

type dfuGeneratedReader struct{ offset int64 }

func (r *dfuGeneratedReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = dfuPattern(r.offset + int64(i))
	}
	r.offset += int64(len(p))
	return len(p), nil
}

func dfuTestSession(m *dfuTarget, transferSize int) *usbSession {
	return &usbSession{transport: m, descriptor: usbDescriptor{interfaceNumber: 3, transferSize: transferSize}, buffer: make([]byte, transferSize)}
}

func startTestDFU(t *testing.T, m *dfuTarget, transferSize int) *dfuClient {
	t.Helper()
	s := dfuTestSession(m, transferSize)
	if err := s.startDFU(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	return s.dfu
}

func assertDFUPoisoned(t *testing.T, d *dfuClient, m *dfuTarget) {
	t.Helper()
	calls, selections := m.calls, m.selections
	ctx := context.Background()
	if err := d.download(ctx, 8, bytes.NewReader([]byte{1}), 1); err == nil {
		t.Fatal("poisoned download succeeded")
	}
	if _, err := d.uploadToEOF(ctx, 8, func(int64, []byte) error { return nil }); err == nil {
		t.Fatal("poisoned upload succeeded")
	}
	if _, err := d.uploadShort(ctx, 9, 7); err == nil {
		t.Fatal("poisoned phase query succeeded")
	}
	if err := d.detach(ctx, 0); err == nil {
		t.Fatal("poisoned detach succeeded")
	}
	if m.calls != calls || m.selections != selections {
		t.Fatal("poisoned client issued another USB request/alternate selection")
	}
}

func TestDFUDownloadManifestationAndReadback(t *testing.T) {
	for _, tfa := range []bool{false, true} {
		t.Run(fmt.Sprintf("TFA=%t", tfa), func(t *testing.T) {
			m := &dfuTarget{state: 2, tfa: tfa, storage: bytes.Repeat([]byte{0xa5}, 24)}
			d := startTestDFU(t, m, 8)
			// Interior zeroes must overwrite existing media, not be omitted.
			payload := []byte{1, 2, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 4, 5, 6, 7}
			if err := d.download(context.Background(), 7, bytes.NewReader(payload), int64(len(payload))); err != nil {
				t.Fatal(err)
			}
			if m.manifesting || !bytes.Equal(m.storage, payload) {
				t.Fatal("download returned before media manifestation")
			}
			total, err := d.uploadToEOF(context.Background(), 7, func(offset int64, data []byte) error {
				if !bytes.Equal(data, payload[offset:offset+int64(len(data))]) {
					return errors.New("readback mismatch")
				}
				return nil
			})
			if err != nil || total != int64(len(payload)) {
				t.Fatalf("readback: total=%d err=%v", total, err)
			}
			if m.requests[2] != 4 || m.state != 2 {
				t.Fatal("exact multiple upload did not consume terminal zero EOF")
			}
			if err := d.detach(context.Background(), 1000); err != nil || !m.detached {
				t.Fatalf("completed target could not detach: %v", err)
			}
		})
	}
}

func TestDFUBlockRolloverStreaming(t *testing.T) {
	const size = int64(65538*8 + 3)
	m := &dfuTarget{state: 2, stream: true, streamSize: size}
	d := startTestDFU(t, m, 8)
	if err := d.download(context.Background(), 7, &dfuGeneratedReader{}, size); err != nil {
		t.Fatal(err)
	}
	if m.written != size || m.manifesting {
		t.Fatalf("incomplete rollover transfer: %d bytes", m.written)
	}
	total, err := d.uploadToEOF(context.Background(), 7, func(offset int64, data []byte) error {
		for i, b := range data {
			if b != dfuPattern(offset+int64(i)) {
				return fmt.Errorf("rollover read mismatch at %d", offset+int64(i))
			}
		}
		return nil
	})
	if err != nil || total != size || m.state != 2 {
		t.Fatalf("rollover readback: total=%d err=%v state=%d", total, err, m.state)
	}
}

func TestDFUFailuresForbidReplayOrRecovery(t *testing.T) {
	usbErr := errors.New("lost USB completion")
	cases := []struct {
		name      string
		configure func(*dfuTarget)
	}{
		{"accepted download timeout", func(m *dfuTarget) { m.failRequest, m.failAt, m.failErr = 1, 1, usbErr }},
		{"short OUT", func(m *dfuTarget) { m.shortRequest, m.shortAt = 1, 1 }},
		{"status transport error", func(m *dfuTarget) { m.failRequest, m.failAt, m.failErr = 3, 2, usbErr }},
		{"truncated six byte status", func(m *dfuTarget) { m.shortRequest, m.shortAt = 3, 2 }},
		{"manifestation media error", func(m *dfuTarget) { m.manifestStatus = 3 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &dfuTarget{state: 2}
			d := startTestDFU(t, m, 8)
			tc.configure(m)
			if err := d.download(context.Background(), 7, bytes.NewReader([]byte{1, 2, 3}), 3); err == nil {
				t.Fatal("faulty download succeeded")
			}
			if !bytes.Equal(m.pending, []byte{1, 2, 3}) || m.written != 3 || m.detached {
				t.Fatal("failed write was replayed, flushed or detached")
			}
			assertDFUPoisoned(t, d, m)
		})
	}
}

func TestDFUUploadFailureAndMismatchPoison(t *testing.T) {
	for _, transportFailure := range []bool{false, true} {
		t.Run(fmt.Sprintf("transportFailure=%t", transportFailure), func(t *testing.T) {
			m := &dfuTarget{state: 2, storage: bytes.Repeat([]byte{0xa5}, 24)}
			d := startTestDFU(t, m, 8)
			if transportFailure {
				m.failRequest, m.failAt, m.failErr = 2, 1, io.ErrUnexpectedEOF
			}
			mismatch := errors.New("verified byte differs")
			_, err := d.uploadToEOF(context.Background(), 7, func(int64, []byte) error { return mismatch })
			if err == nil || (!transportFailure && !errors.Is(err, mismatch)) {
				t.Fatalf("upload lost error: %v", err)
			}
			if m.readOffset != 8 || m.state != 9 {
				t.Fatal("failed upload continued or concealed a live backend")
			}
			assertDFUPoisoned(t, d, m)
		})
	}
}

func TestDFUInitialRecoveryOnly(t *testing.T) {
	for _, recoverError := range []bool{false, true} {
		m := &dfuTarget{state: 10, status: 14}
		s := dfuTestSession(m, 8)
		err := s.startDFU(context.Background(), recoverError)
		if recoverError {
			if err != nil || m.requests[4] != 1 {
				t.Fatalf("fresh initial recovery: %v", err)
			}
			if err := s.dfu.download(context.Background(), 7, bytes.NewReader([]byte{42}), 1); err != nil {
				t.Fatal(err)
			}
		} else if err == nil || m.requests[4] != 0 {
			t.Fatal("initial ROM/TFA error was cleared")
		}
		calls := m.calls
		if err := s.startDFU(context.Background(), true); err == nil || m.calls != calls {
			t.Fatal("existing connection restarted instead of requiring re-entry")
		}
	}
	m := &dfuTarget{state: 5}
	s := dfuTestSession(m, 8)
	if err := s.startDFU(context.Background(), true); err == nil || m.requests[4] != 0 {
		t.Fatal("live transaction was treated as a fresh session")
	}
}

func TestDFURejectsEmptyAndTruncatedSource(t *testing.T) {
	for _, size := range []int64{0, 9} {
		m := &dfuTarget{state: 2}
		d := startTestDFU(t, m, 8)
		if err := d.download(context.Background(), 7, bytes.NewReader([]byte{1}), size); err == nil {
			t.Fatal("invalid source succeeded")
		}
		if m.requests[1] != 0 {
			t.Fatal("invalid first block was transmitted")
		}
		assertDFUPoisoned(t, d, m)
	}
}

func TestDFUVirtualUploadRequiresShortResponse(t *testing.T) {
	m := &dfuTarget{state: 2, storage: []byte{1, 2, 3}}
	d := startTestDFU(t, m, 8)
	data, err := d.uploadShort(context.Background(), 1, 7)
	if err != nil || !bytes.Equal(data, m.storage) || m.state != 2 {
		t.Fatalf("short query failed: %x %v", data, err)
	}
	m.storage = bytes.Repeat([]byte{7}, 8)
	if _, err := d.uploadShort(context.Background(), 1, 7); err == nil {
		t.Fatal("full virtual response incorrectly accepted")
	}
	assertDFUPoisoned(t, d, m)
}

func TestDFUStatusFieldsAndCancelledWait(t *testing.T) {
	m := &dfuTarget{state: 2, delayMS: 0x123456, stringIndex: 0x91}
	d := startTestDFU(t, m, 8)
	status, err := d.getStatus(context.Background())
	if err != nil || status.delay != 0x123456*time.Millisecond || status.stringIndex != 0x91 {
		t.Fatalf("status fields lost: %+v %v", status, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.wait(ctx, time.Hour, time.Now().Add(time.Hour)); !errors.Is(err, context.Canceled) {
		t.Fatalf("status wait did not honor cancellation: %v", err)
	}
	assertDFUPoisoned(t, d, m)
}

func TestDFUStatusWaitExpired(t *testing.T) {
	m := &dfuTarget{state: 2}
	d := startTestDFU(t, m, 8)
	if err := d.wait(context.Background(), time.Hour, time.Now().Add(-time.Second)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired wait succeeded: %v", err)
	}
	assertDFUPoisoned(t, d, m)
}

func TestDFUCancelledTransactionNeverSends(t *testing.T) {
	m := &dfuTarget{state: 2}
	d := startTestDFU(t, m, 8)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.download(ctx, 7, bytes.NewReader([]byte{1}), 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if m.requests[1] != 0 || m.selections != 0 {
		t.Fatal("cancelled transaction touched target")
	}
	assertDFUPoisoned(t, d, m)
}

func TestDFUCancelledDuringFinalReadbackFails(t *testing.T) {
	m := &dfuTarget{state: 2, storage: []byte{1, 2, 3}}
	d := startTestDFU(t, m, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := d.uploadToEOF(ctx, 7, func(int64, []byte) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("final consumer cancellation lost: %v", err)
	}
	assertDFUPoisoned(t, d, m)
}

func TestDFUMidTransactionSourceFailurePoisons(t *testing.T) {
	m := &dfuTarget{state: 2}
	d := startTestDFU(t, m, 8)
	err := d.download(context.Background(), 7, bytes.NewReader(make([]byte, 8)), 9)
	if !errors.Is(err, io.EOF) || m.written != 8 || m.requests[1] != 1 || m.state != 5 {
		t.Fatalf("source truncation did not preserve failed transaction: %v", err)
	}
	assertDFUPoisoned(t, d, m)
}

func TestDFUCancelledBusyWaitDoesNotContinue(t *testing.T) {
	m := &dfuTarget{state: 2, stall: true, delayMS: 0xffffff, statusObserved: make(chan struct{}, 1)}
	d := startTestDFU(t, m, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- d.download(ctx, 7, bytes.NewReader(make([]byte, 16)), 16)
	}()
	select {
	case <-m.statusObserved:
		cancel()
	case <-time.After(5 * time.Second):
		t.Fatal("download never reached busy status")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("busy-wait cancellation lost: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("device's long poll delay prevented cancellation")
	}
	if m.written != 8 || m.requests[1] != 1 || m.manifesting {
		t.Fatal("cancelled busy wait continued the write")
	}
	assertDFUPoisoned(t, d, m)
}

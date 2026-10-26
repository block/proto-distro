package fastboot

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"proto-distro/tools/go_fastboot/internal/fastboot/udp"
)

// newNativeTestPeer serves real UDP packets, replaying duplicate requests without
// repeating handler side effects. The handler runs serially and may inspect raw
// download packets as well as commands. A nil reply deliberately drops a packet.
func newNativeTestPeer(t *testing.T, handle func(*udp.Packet) *udp.Packet) int {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		type lastReply struct {
			id       udp.PacketID
			sequence uint16
			data     []byte
		}
		last := make(map[netip.AddrPort]lastReply)
		buf := make([]byte, udp.MaxPacketSize)
		for {
			n, addr, err := conn.ReadFromUDPAddrPort(buf)
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					t.Errorf("peer read: %v", err)
				}
				return
			}
			request, err := udp.DecodePacket(buf[:n])
			if err != nil {
				t.Errorf("peer decode: %v", err)
				return
			}
			previous, duplicate := last[addr]
			var reply []byte
			if duplicate && previous.id == request.ID && previous.sequence == request.Sequence {
				reply = previous.data
			} else {
				if response := handle(request); response != nil {
					reply = response.Encode()
				}
				last[addr] = lastReply{request.ID, request.Sequence, reply}
			}
			if reply != nil {
				if _, err := conn.WriteToUDPAddrPort(reply, addr); err != nil {
					if !errors.Is(err, net.ErrClosed) {
						t.Errorf("peer write: %v", err)
					}
					return
				}
			}
		}
	}()
	t.Cleanup(func() {
		conn.Close()
		<-done
	})
	return conn.LocalAddr().(*net.UDPAddr).Port
}

func nativeTestHandshake(request *udp.Packet) *udp.Packet {
	switch request.ID {
	case udp.PacketQuery:
		return &udp.Packet{ID: udp.PacketQuery, Sequence: request.Sequence, Data: []byte{0, 1}}
	case udp.PacketInit:
		return udp.NewInitPacket(request.Sequence, udp.ProtocolVersion, udp.DefaultPacketSize)
	default:
		return nil
	}
}

func testNativeRebootAcknowledgement(t *testing.T, command string, operation func(context.Context, *NativeClient) error) {
	t.Helper()
	for _, tt := range []struct {
		name      string
		replies   []string
		handshake string
		wantError bool
		deadline  bool
	}{
		{name: "okay", replies: []string{"OKAY"}},
		{name: "info then okay", replies: []string{"INFOrestarting", "TEXTbooting", "OKAY"}},
		{name: "empty ack then okay", replies: []string{"", "OKAY"}},
		{name: "fail", replies: []string{"FAILdenied"}, wantError: true},
		{name: "data", replies: []string{"DATA00000001"}, wantError: true},
		{name: "short response", replies: []string{"OK"}, wantError: true},
		{name: "unknown response", replies: []string{"NOPE"}, wantError: true},
		{name: "dropped command ack", wantError: true, deadline: true},
		{name: "empty ack then silence", replies: []string{""}, wantError: true, deadline: true},
		{name: "info then silence", replies: []string{"INFOrestarting"}, wantError: true, deadline: true},
		{name: "handshake failure", handshake: "reject", wantError: true},
		{name: "handshake silence", handshake: "silent", wantError: true, deadline: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			commands := make(chan string, 8)
			index := 0
			port := newNativeTestPeer(t, func(request *udp.Packet) *udp.Packet {
				if request.ID != udp.PacketFastboot {
					switch tt.handshake {
					case "reject":
						return &udp.Packet{ID: udp.PacketError, Sequence: request.Sequence, Data: []byte("denied")}
					case "silent":
						return nil
					default:
						return nativeTestHandshake(request)
					}
				}
				if len(request.Data) > 0 {
					commands <- string(request.Data)
				}
				if index == len(tt.replies) {
					return nil
				}
				reply := tt.replies[index]
				index++
				return udp.NewFastbootPacket(request.Sequence, []byte(reply), false)
			})
			client := NewNativeClient("127.0.0.1").WithPort(port).WithTimeout(time.Second)
			timeout := time.Second
			if tt.deadline {
				timeout = 100 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			err := operation(ctx, client)
			if (err != nil) != tt.wantError {
				t.Fatalf("operation error = %v, want error %v", err, tt.wantError)
			}
			if errors.Is(err, context.DeadlineExceeded) != tt.deadline {
				t.Fatalf("error = %v, want deadline exceeded %v", err, tt.deadline)
			}
			if tt.handshake == "" {
				select {
				case got := <-commands:
					if got != command {
						t.Fatalf("command = %q, want %q", got, command)
					}
				default:
					t.Fatal("command not received")
				}
			}
			select {
			case got := <-commands:
				t.Fatalf("unexpected command %q", got)
			default:
			}
		})
	}
}

func TestNativeRebootAcknowledgement(t *testing.T) {
	testNativeRebootAcknowledgement(t, "reboot", func(ctx context.Context, client *NativeClient) error {
		return client.Reboot(ctx)
	})
}

func TestNativeCheckResponsiveDeadline(t *testing.T) {
	for _, phase := range []string{"handshake", "product"} {
		for _, tt := range []struct {
			name   string
			client time.Duration
			parent time.Duration
		}{
			{"configured timeout", 50 * time.Millisecond, time.Minute},
			{"parent deadline", time.Second, 25 * time.Millisecond},
		} {
			t.Run(phase+"/"+tt.name, func(t *testing.T) {
				port := newNativeTestPeer(t, func(request *udp.Packet) *udp.Packet {
					if phase == "handshake" {
						return nil
					}
					if request.ID == udp.PacketFastboot && string(request.Data) != "getvar:product" {
						t.Errorf("unexpected command %q", request.Data)
					}
					return nativeTestHandshake(request)
				})
				client := NewNativeClient("127.0.0.1").WithPort(port).WithTimeout(tt.client)
				ctx, cancel := context.WithTimeout(context.Background(), tt.parent)
				defer cancel()
				start := time.Now()
				err := client.CheckResponsive(ctx)
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("error = %v, want deadline exceeded", err)
				}
				if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
					t.Fatalf("probe took %v", elapsed)
				}
			})
		}
	}
}

func TestNativeCancellationDuringRead(t *testing.T) {
	for name, operation := range map[string]func(context.Context, *NativeClient) error{
		"responsive": func(ctx context.Context, client *NativeClient) error { return client.CheckResponsive(ctx) },
		"reboot":     func(ctx context.Context, client *NativeClient) error { return client.Reboot(ctx) },
		"wait": func(ctx context.Context, client *NativeClient) error {
			return client.WaitForDevice(ctx, time.Minute)
		},
	} {
		t.Run(name, func(t *testing.T) {
			ready := make(chan struct{})
			port := newNativeTestPeer(t, func(request *udp.Packet) *udp.Packet {
				if request.ID == udp.PacketFastboot {
					close(ready)
					return nil
				}
				return nativeTestHandshake(request)
			})
			client := NewNativeClient("127.0.0.1").WithPort(port)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- operation(ctx, client) }()
			select {
			case <-ready:
			case <-ctx.Done():
				t.Fatal("command never reached peer")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error = %v, want canceled", err)
				}
			case <-time.After(400 * time.Millisecond):
				t.Fatal("cancellation did not unblock read")
			}
		})
	}
}

func TestNativeWaitForDeviceBudget(t *testing.T) {
	for _, phase := range []string{"probe", "poll delay"} {
		t.Run(phase, func(t *testing.T) {
			port := newNativeTestPeer(t, func(request *udp.Packet) *udp.Packet {
				if request.ID == udp.PacketFastboot && phase == "poll delay" {
					return udp.NewFastbootPacket(request.Sequence, []byte("FAILnot ready"), false)
				}
				return nativeTestHandshake(request)
			})
			client := NewNativeClient("127.0.0.1").WithPort(port)
			start := time.Now()
			err := client.WaitForDevice(context.Background(), 100*time.Millisecond)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error = %v, want deadline exceeded", err)
			}
			if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
				t.Fatalf("wait took %v, exceeded overall budget", elapsed)
			}
		})
	}
}

func TestNativeWaitForDeviceReady(t *testing.T) {
	port := newNativeTestPeer(t, func(request *udp.Packet) *udp.Packet {
		if request.ID == udp.PacketFastboot {
			if string(request.Data) != "getvar:product" {
				t.Errorf("unexpected command %q", request.Data)
			}
			return udp.NewFastbootPacket(request.Sequence, []byte("OKAYloopback"), false)
		}
		return nativeTestHandshake(request)
	})
	client := NewNativeClient("127.0.0.1").WithPort(port)
	if err := client.WaitForDevice(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
}

// nativeTransferPeer records real command/data exchanges. Its mutex also joins
// assertions with the UDP handler; failures never rely on timing sleeps.
type nativeTransferPeer struct {
	mu                                 sync.Mutex
	commands                           []string
	payloads                           [][]byte
	queries, inits, downloads, flashes int
	limit                              string
	failDownload, failFinal, failFlash int
	afterFlash                         func(int)
	stall                              bool
	rawStarted                         chan struct{}
	pending                            []byte
	remaining                          int
	awaitFinal                         bool
}

func (p *nativeTransferPeer) serve(t *testing.T) int {
	return newNativeTestPeer(t, func(request *udp.Packet) *udp.Packet {
		p.mu.Lock()
		defer p.mu.Unlock()
		if request.ID == udp.PacketQuery {
			p.queries++
			return nativeTestHandshake(request)
		}
		if request.ID == udp.PacketInit {
			p.inits++
			return nativeTestHandshake(request)
		}
		reply := func(text string) *udp.Packet { return udp.NewFastbootPacket(request.Sequence, []byte(text), false) }
		if p.remaining > 0 {
			if p.stall {
				if p.rawStarted != nil {
					close(p.rawStarted)
					p.rawStarted = nil
				}
				return nil
			}
			if len(request.Data) > p.remaining {
				t.Errorf("too much raw data")
				return reply("FAILoverflow")
			}
			p.pending = append(p.pending, request.Data...)
			p.remaining -= len(request.Data)
			if p.remaining == 0 {
				p.awaitFinal = true
			}
			return reply("")
		}
		if p.awaitFinal {
			if len(request.Data) != 0 {
				t.Errorf("expected final download poll")
			}
			p.awaitFinal = false
			p.payloads = append(p.payloads, p.pending)
			p.pending = nil
			if p.failFinal == p.downloads {
				return reply("FAILdownload final")
			}
			return reply("OKAY")
		}
		command := string(request.Data)
		p.commands = append(p.commands, command)
		switch {
		case command == "getvar:max-download-size":
			if p.limit == "FAIL" {
				return reply("FAILnot supported")
			}
			return reply("OKAY" + p.limit)
		case strings.HasPrefix(command, "download:"):
			p.downloads++
			if p.failDownload == p.downloads {
				return reply("FAILdownload rejected")
			}
			size, err := strconv.ParseUint(strings.TrimPrefix(command, "download:"), 16, 32)
			if err != nil || size == 0 {
				t.Errorf("invalid download: %q", command)
				return reply("FAILsize")
			}
			p.remaining = int(size)
			return reply(fmt.Sprintf("DATA%08x", size))
		case command == "flash:rootfs_a":
			p.flashes++
			if p.failFlash == p.flashes {
				return reply("FAILflash rejected")
			}
			if p.afterFlash != nil {
				p.afterFlash(p.flashes)
			}
			return reply("OKAY")
		default:
			t.Errorf("unexpected command %q", command)
			return reply("FAILunexpected")
		}
	})
}

func nativeTransferSource(t *testing.T, sparse bool) (string, []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "image")
	raw := make([]byte, 3*4096+17)
	for i := range raw {
		raw[i] = byte(i*13 + 7)
	}
	if !sparse {
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		return path, append(raw, make([]byte, 4096-17)...)
	}
	// Leading/trailing gaps remain sentinel-filled; literal RAW zeros overwrite it.
	body := make([]byte, 4*4096)
	copy(body, raw)
	expected := bytes.Repeat([]byte{0xa5}, 6*4096)
	copy(expected[4096:], body)
	var wire bytes.Buffer
	for _, value := range []any{
		SparseHeader{Magic: sparseHeaderMagic, MajorVersion: 1, FileHdrSize: 28, ChunkHdrSize: 12, BlockSize: 4096, TotalBlocks: 6, TotalChunks: 3},
		ChunkHeader{ChunkType: chunkTypeDontCare, ChunkBlocks: 1, TotalSize: 12},
		ChunkHeader{ChunkType: chunkTypeRaw, ChunkBlocks: 4, TotalSize: 12 + 4*4096},
		body,
		ChunkHeader{ChunkType: chunkTypeDontCare, ChunkBlocks: 1, TotalSize: 12},
	} {
		if err := binary.Write(&wire, binary.LittleEndian, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, wire.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path, expected
}

func applyNativeTransfer(t *testing.T, target, wire []byte) {
	t.Helper()
	if len(wire) < 28 || binary.LittleEndian.Uint32(wire[:4]) != 0xed26ff3a {
		t.Fatal("invalid sparse transfer")
	}
	blockSize := int(binary.LittleEndian.Uint32(wire[12:16]))
	if int(binary.LittleEndian.Uint32(wire[16:20]))*blockSize != len(target) {
		t.Fatal("changed logical image size")
	}
	count := int(binary.LittleEndian.Uint32(wire[20:24]))
	wire = wire[28:]
	offset := 0
	for range count {
		if len(wire) < 12 {
			t.Fatal("truncated chunk")
		}
		kind := binary.LittleEndian.Uint16(wire[:2])
		length := int(binary.LittleEndian.Uint32(wire[4:8])) * blockSize
		size := int(binary.LittleEndian.Uint32(wire[8:12]))
		if size < 12 || size > len(wire) || offset+length > len(target) {
			t.Fatal("invalid chunk bounds")
		}
		switch kind {
		case 0xcac1:
			if size != 12+length {
				t.Fatal("invalid RAW length")
			}
			copy(target[offset:offset+length], wire[12:size])
		case 0xcac3:
			if size != 12 {
				t.Fatal("DONT_CARE payload")
			}
		default:
			t.Fatalf("unexpected chunk type %x", kind)
		}
		offset += length
		wire = wire[size:]
	}
	if len(wire) != 0 || offset != len(target) {
		t.Fatal("incomplete sparse transfer")
	}
}

func TestNativeBoundedTransfers(t *testing.T) {
	for _, sparse := range []bool{false, true} {
		for _, scenario := range []string{"success", "second download", "second final", "first flash", "truncate", "cancel"} {
			t.Run(fmt.Sprintf("sparse=%v/%s", sparse, scenario), func(t *testing.T) {
				path, expected := nativeTransferSource(t, sparse)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				peer := &nativeTransferPeer{limit: "1388"}
				switch scenario {
				case "second download":
					peer.failDownload = 2
				case "second final":
					peer.failFinal = 2
				case "first flash":
					peer.failFlash = 1
				case "truncate":
					peer.afterFlash = func(n int) {
						if n == 1 {
							if err := os.Truncate(path, 0); err != nil {
								t.Error(err)
							}
						}
					}
				case "cancel":
					peer.afterFlash = func(n int) {
						if n == 1 {
							cancel()
						}
					}
				}
				client := NewNativeClient("127.0.0.1").WithPort(peer.serve(t))
				var messages []string
				result, err := client.Flash(ctx, "rootfs_a", path, 3*time.Second, func(message string) { messages = append(messages, message) })
				peer.mu.Lock()
				defer peer.mu.Unlock()
				if result == nil || result.Duration <= 0 || result.Partition != "rootfs_a" || result.ImagePath != path {
					t.Fatalf("bad result: %+v", result)
				}
				if peer.queries != 1 || peer.inits != 1 {
					t.Fatalf("reconnected: queries=%d inits=%d", peer.queries, peer.inits)
				}
				if scenario == "success" {
					if err != nil || result.Error != nil {
						t.Fatalf("flash: %v, %+v", err, result)
					}
					if peer.downloads < 4 || peer.flashes != peer.downloads {
						t.Fatalf("parts: %d/%d", peer.downloads, peer.flashes)
					}
					actual := bytes.Repeat([]byte{0xa5}, len(expected))
					for _, payload := range peer.payloads {
						if len(payload) > 5000 {
							t.Fatalf("oversized transfer: %d", len(payload))
						}
						applyNativeTransfer(t, actual, payload)
					}
					if !bytes.Equal(actual, expected) {
						t.Fatal("reconstructed bytes differ")
					}
					if len(messages) == 0 || !strings.HasPrefix(messages[len(messages)-1], "Flash complete") {
						t.Fatal("missing success progress")
					}
				} else {
					if err == nil || result.Error == nil {
						t.Fatalf("accepted %s", scenario)
					}
					wantDownloads := 1
					if strings.HasPrefix(scenario, "second") {
						wantDownloads = 2
					}
					if peer.downloads != wantDownloads || peer.flashes != 1 {
						t.Fatalf("not fail-stop: downloads=%d flashes=%d", peer.downloads, peer.flashes)
					}
					if scenario == "cancel" && (!errors.Is(err, context.Canceled) || !errors.Is(result.Error, context.Canceled)) {
						t.Fatalf("lost cancellation: %v", err)
					}
					for _, message := range messages {
						if strings.HasPrefix(message, "Flash complete") {
							t.Fatal("reported success after failure")
						}
					}
				}
				last := -1
				for _, message := range messages {
					var percent int
					if _, scanErr := fmt.Sscanf(message, "\rProgress (%d%%)", &percent); scanErr == nil {
						if percent < last || percent > 100 {
							t.Fatalf("bad progress %d after %d", percent, last)
						}
						last = percent
					}
				}
				if scenario == "success" && last != 100 {
					t.Fatalf("final progress %d", last)
				}
				for i, command := range peer.commands[1:] {
					prefix := "download:"
					if i%2 == 1 {
						prefix = "flash:"
					}
					if !strings.HasPrefix(command, prefix) {
						t.Fatalf("non-alternating commands: %v", peer.commands)
					}
				}
			})
		}
	}
}

func TestNativeDownloadLimits(t *testing.T) {
	for _, limit := range []string{"", "0", "0x0", "FAIL", "1junk", " 1000", "+1000", "-1", "1_000", "0x", "10000000000000000"} {
		t.Run(limit, func(t *testing.T) {
			path, _ := nativeTransferSource(t, false)
			peer := &nativeTransferPeer{limit: limit}
			client := NewNativeClient("127.0.0.1").WithPort(peer.serve(t))
			result, err := client.Flash(context.Background(), "rootfs_a", path, time.Second, nil)
			peer.mu.Lock()
			defer peer.mu.Unlock()
			if err == nil || result.Error == nil || result.Duration <= 0 {
				t.Fatalf("accepted limit %q: %+v %v", limit, result, err)
			}
			if peer.downloads != 0 || peer.flashes != 0 {
				t.Fatal("invalid limit wrote device")
			}
		})
	}
	for _, advertised := range []uint64{1, 5000, 256 * MB, math.MaxUint32, math.MaxUint64} {
		limit, err := effectiveDownloadSize(advertised)
		if err != nil || limit != int64(min(advertised, uint64(256*MB))) {
			t.Fatalf("effective limit %x: %d %v", advertised, limit, err)
		}
	}
	if _, err := effectiveDownloadSize(0); err == nil {
		t.Fatal("accepted zero")
	}
	for _, value := range []string{"ffffffffffffffff", "0XFFFFFFFFFFFFFFFF"} {
		got, err := parseHexSize(value)
		if err != nil || got != math.MaxUint64 {
			t.Fatalf("parse %s: %x %v", value, got, err)
		}
	}
	if got := nativeProgressPercent(math.MaxInt64-1, math.MaxInt64); got != 99 {
		t.Fatalf("overflow progress: %d", got)
	}
}

func TestNativeDirectRawAndLargeLimit(t *testing.T) {
	for _, size := range []int{1, 4095, 4096, 4097} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "raw")
			data := bytes.Repeat([]byte{0x5a}, size)
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			peer := &nativeTransferPeer{limit: "ffffffffffffffff"}
			client := NewNativeClient("127.0.0.1").WithPort(peer.serve(t))
			result, err := client.Flash(context.Background(), "rootfs_a", path, time.Second, nil)
			if err != nil || result.Error != nil {
				t.Fatalf("flash: %v", err)
			}
			peer.mu.Lock()
			defer peer.mu.Unlock()
			if len(peer.payloads) != 1 || !bytes.Equal(peer.payloads[0], data) {
				t.Fatal("direct raw bytes changed")
			}
		})
	}
}

func TestNativeValidateImages(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(fmt.Sprintf("invalid=%v", invalid), func(t *testing.T) {
			raw, _ := nativeTransferSource(t, false)
			sparse, _ := nativeTransferSource(t, true)
			if invalid {
				if err := os.Truncate(sparse, 28); err != nil {
					t.Fatal(err)
				}
			}
			peer := &nativeTransferPeer{limit: "1388"}
			client := NewNativeClient("127.0.0.1").WithPort(peer.serve(t))
			err := client.ValidateImages(context.Background(), []string{raw, sparse})
			if (err != nil) != invalid {
				t.Fatalf("validation: %v", err)
			}
			peer.mu.Lock()
			defer peer.mu.Unlock()
			if len(peer.commands) != 1 || peer.commands[0] != "getvar:max-download-size" || peer.queries != 1 || client.transport != nil {
				t.Fatalf("validation side effects: %v", peer.commands)
			}
		})
	}
}

func TestNativeTransferCancellationDuringPayload(t *testing.T) {
	for _, sparse := range []bool{false, true} {
		t.Run(fmt.Sprintf("sparse=%v", sparse), func(t *testing.T) {
			path, _ := nativeTransferSource(t, sparse)
			started := make(chan struct{})
			peer := &nativeTransferPeer{limit: "1388", stall: true, rawStarted: started}
			client := NewNativeClient("127.0.0.1").WithPort(peer.serve(t))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				result, err := client.Flash(ctx, "rootfs_a", path, 3*time.Second, nil)
				if result == nil || result.Duration <= 0 || !errors.Is(result.Error, context.Canceled) {
					t.Errorf("bad canceled result: %+v", result)
				}
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("no raw transfer")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("lost cancellation: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("cancellation blocked")
			}
			peer.mu.Lock()
			defer peer.mu.Unlock()
			if peer.downloads != 1 || peer.flashes != 0 {
				t.Fatal("canceled transfer continued")
			}
		})
	}
}

func TestNativePlanningRejectsBeforeDownload(t *testing.T) {
	for _, sparse := range []bool{false, true} {
		t.Run(fmt.Sprintf("sparse=%v", sparse), func(t *testing.T) {
			path, _ := nativeTransferSource(t, sparse)
			// First part could fit. A later middle part needs 4160 bytes.
			peer := &nativeTransferPeer{limit: "103f"}
			client := NewNativeClient("127.0.0.1").WithPort(peer.serve(t))
			result, err := client.Flash(context.Background(), "rootfs_a", path, time.Second, nil)
			if err == nil || result.Error == nil || result.Duration <= 0 {
				t.Fatalf("accepted impossible later part: %+v %v", result, err)
			}
			peer.mu.Lock()
			defer peer.mu.Unlock()
			if peer.downloads != 0 || peer.flashes != 0 {
				t.Fatal("planning failure followed earlier writes")
			}
		})
	}
}

func TestNativeAdvertisedLimitClamp(t *testing.T) {
	peer := &nativeTransferPeer{limit: "ffffffffffffffff"}
	client := NewNativeClient("127.0.0.1").WithPort(peer.serve(t))
	defer client.disconnect()
	limit, err := client.getMaxDownloadSize(context.Background())
	if err != nil || limit != 256*MB {
		t.Fatalf("queried limit: %d %v", limit, err)
	}
}

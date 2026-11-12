package udp

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// UDP peers bind before returning and join on cleanup; no hardware or startup sleeps.
func newUDPTestPeer(t *testing.T, reply func(*Packet) []*Packet) *Transport {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, MaxPacketSize)
		for {
			n, addr, err := conn.ReadFromUDP(buf)
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					t.Errorf("peer read: %v", err)
				}
				return
			}
			packet, err := DecodePacket(buf[:n])
			if err != nil {
				t.Errorf("peer decode: %v", err)
				return
			}
			for _, response := range reply(packet) {
				if _, err := conn.WriteToUDP(response.Encode(), addr); err != nil {
					if !errors.Is(err, net.ErrClosed) {
						t.Errorf("peer write: %v", err)
					}
					return
				}
			}
		}
	}()
	transport := NewTransport("127.0.0.1").WithPort(conn.LocalAddr().(*net.UDPAddr).Port)
	t.Cleanup(func() {
		transport.Close()
		conn.Close()
		<-done
	})
	return transport
}

func udpTestReply(packet *Packet) []*Packet {
	switch packet.ID {
	case PacketQuery:
		return []*Packet{{ID: PacketQuery, Sequence: packet.Sequence, Data: []byte{0, 42}}}
	case PacketInit:
		return []*Packet{NewInitPacket(packet.Sequence, ProtocolVersion, DefaultPacketSize)}
	default:
		return []*Packet{NewFastbootPacket(packet.Sequence, []byte("OKAY"), false)}
	}
}

func connectUDPTestPeer(t *testing.T, transport *Transport) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := transport.Connect(ctx); err != nil {
		t.Fatal(err)
	}
}

var udpOperations = []struct {
	name string
	run  func(context.Context, *Transport) error
}{
	{"Connect", func(ctx context.Context, transport *Transport) error { return transport.Connect(ctx) }},
	{"Send", func(ctx context.Context, transport *Transport) error {
		_, err := transport.Send(ctx, []byte("getvar:product"))
		return err
	}},
	{"Receive", func(ctx context.Context, transport *Transport) error {
		_, err := transport.Receive(ctx)
		return err
	}},
	{"SendRaw", func(ctx context.Context, transport *Transport) error {
		return transport.SendRaw(ctx, []byte("payload"), nil)
	}},
}

func TestTransportCancellationClosesBlockedSocket(t *testing.T) {
	for _, operation := range udpOperations {
		t.Run(operation.name, func(t *testing.T) {
			seen := make(chan struct{}, 1)
			transport := newUDPTestPeer(t, func(packet *Packet) []*Packet {
				if operation.name == "Connect" || packet.ID == PacketFastboot {
					select {
					case seen <- struct{}{}:
					default:
					}
					return nil
				}
				return udpTestReply(packet)
			})
			if operation.name != "Connect" {
				connectUDPTestPeer(t, transport)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- operation.run(ctx, transport) }()
			select {
			case <-seen:
			case <-time.After(2 * time.Second):
				t.Fatal("peer never received operation")
			}
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("operation error = %v, want cancellation", err)
				}
			case <-time.After(300 * time.Millisecond):
				t.Fatal("cancellation waited for retransmit interval")
			}
			if operation.name == "Connect" {
				if transport.IsConnected() {
					t.Fatal("failed handshake retained connection")
				}
			} else if err := transport.conn.SetDeadline(time.Now()); !errors.Is(err, net.ErrClosed) {
				t.Fatalf("canceled socket remained open: %v", err)
			}
		})
	}
}

func TestTransportCallerDeadline(t *testing.T) {
	for _, operation := range udpOperations {
		t.Run(operation.name, func(t *testing.T) {
			transport := newUDPTestPeer(t, func(packet *Packet) []*Packet {
				if operation.name == "Connect" || packet.ID == PacketFastboot {
					return nil
				}
				return udpTestReply(packet)
			})
			if operation.name != "Connect" {
				connectUDPTestPeer(t, transport)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			start := time.Now()
			if err := operation.run(ctx, transport); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("operation error = %v, want deadline", err)
			}
			if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
				t.Fatalf("deadline took %s", elapsed)
			}
		})
	}
}

func TestTransportStopsCancellationAfterSuccess(t *testing.T) {
	for _, operation := range udpOperations {
		t.Run(operation.name, func(t *testing.T) {
			transport := newUDPTestPeer(t, udpTestReply)
			if operation.name != "Connect" {
				connectUDPTestPeer(t, transport)
			}
			ctx, cancel := context.WithCancel(context.Background())
			if err := operation.run(ctx, transport); err != nil {
				cancel()
				t.Fatal(err)
			}
			cancel()
			for range 3 {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				_, err := transport.Send(ctx, []byte("getvar:product"))
				cancel()
				if err != nil {
					t.Fatalf("completed operation closed reusable socket: %v", err)
				}
			}
		})
	}
}

func TestTransportCanceledBeforeOperation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, operation := range udpOperations {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.run(ctx, NewTransport("127.0.0.1")); !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want cancellation", err)
			}
		})
	}
	if err := NewTransport("127.0.0.1").SendRaw(ctx, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("empty raw error = %v, want cancellation", err)
	}
}

func TestTransportHandshakeValidation(t *testing.T) {
	tests := []struct {
		name    string
		phase   PacketID
		reply   *Packet
		wantErr bool
	}{
		{"query wrong kind", PacketQuery, NewFastbootPacket(0, []byte{0, 42}, false), true},
		{"query truncated", PacketQuery, &Packet{ID: PacketQuery, Data: []byte{0}}, true},
		{"query device error", PacketQuery, &Packet{ID: PacketError, Data: []byte("rejected")}, true},
		{"init wrong kind", PacketInit, NewFastbootPacket(0, []byte{0, 1, 4, 0}, false), true},
		{"init truncated", PacketInit, &Packet{ID: PacketInit, Data: []byte{0, 1, 4}}, true},
		{"zero version", PacketInit, NewInitPacket(0, 0, DefaultPacketSize), true},
		{"zero size", PacketInit, NewInitPacket(0, 1, 0), true},
		{"small size", PacketInit, NewInitPacket(0, 1, MinPacketSize-1), true},
		{"minimum size", PacketInit, NewInitPacket(0, 1, MinPacketSize), false},
		{"newer version larger size", PacketInit, NewInitPacket(0, 2, 1500), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := newUDPTestPeer(t, func(packet *Packet) []*Packet {
				if packet.ID == test.phase {
					response := *test.reply
					response.Sequence = packet.Sequence
					return []*Packet{&response}
				}
				return udpTestReply(packet)
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := transport.Connect(ctx)
			if (err != nil) != test.wantErr {
				t.Fatalf("Connect = %v, want error %t", err, test.wantErr)
			}
			if test.wantErr {
				if transport.IsConnected() {
					t.Fatal("failed negotiation retained socket")
				}
			} else {
				_, size, err := ParseInitResponse(test.reply.Data)
				if err != nil {
					t.Fatal(err)
				}
				if transport.maxPacketSize != min(size, DefaultPacketSize) {
					t.Fatal("incorrect negotiated limits")
				}
			}
		})
	}
}

func TestTransportRejectsWrongKind(t *testing.T) {
	for _, operation := range udpOperations[1:] {
		t.Run(operation.name, func(t *testing.T) {
			transport := newUDPTestPeer(t, func(packet *Packet) []*Packet {
				if packet.ID == PacketFastboot {
					return []*Packet{NewInitPacket(packet.Sequence, 1, 1024)}
				}
				return udpTestReply(packet)
			})
			connectUDPTestPeer(t, transport)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := operation.run(ctx, transport); err == nil || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("wrong packet kind error = %v", err)
			}
		})
	}
}

func TestTransportIgnoresStaleSequences(t *testing.T) {
	transport := newUDPTestPeer(t, func(packet *Packet) []*Packet {
		valid := udpTestReply(packet)[0]
		stale := *valid
		stale.Sequence--
		stale.Data = []byte("stale")
		return []*Packet{&stale, valid}
	})
	connectUDPTestPeer(t, transport)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response, err := transport.Send(ctx, []byte("getvar:product"))
	if err != nil || !bytes.Equal(response, []byte("OKAY")) {
		t.Fatalf("response = %q, error = %v", response, err)
	}
}

func TestTransportRetransmitsSameSequence(t *testing.T) {
	var first []byte
	transport := newUDPTestPeer(t, func(packet *Packet) []*Packet {
		if packet.ID == PacketFastboot {
			if first == nil {
				first = packet.Encode()
				return nil
			}
			if !bytes.Equal(first, packet.Encode()) {
				t.Errorf("retransmission changed packet: %v versus %v", first, packet.Encode())
			}
		}
		return udpTestReply(packet)
	})
	connectUDPTestPeer(t, transport)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := transport.Send(ctx, []byte("getvar:product")); err != nil {
		t.Fatal(err)
	}
}

func TestTransportRetryExhaustionPreservesDeadline(t *testing.T) {
	transport := newUDPTestPeer(t, func(packet *Packet) []*Packet {
		if packet.ID == PacketFastboot {
			return nil
		}
		return udpTestReply(packet)
	})
	connectUDPTestPeer(t, transport)
	_, err := transport.sendAndReceive(context.Background(), NewFastbootPacket(transport.sequence, nil, false), 1)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("retry exhaustion = %v, want deadline", err)
	}
}

func TestTransportClosedSocketDeadlineError(t *testing.T) {
	transport := newUDPTestPeer(t, udpTestReply)
	connectUDPTestPeer(t, transport)
	transport.conn.Close()
	_, err := transport.Send(context.Background(), []byte("getvar:product"))
	if !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed socket error = %v", err)
	}
}

func TestTransportRawCancellationStopsNextPacket(t *testing.T) {
	transport := newUDPTestPeer(t, udpTestReply)
	connectUDPTestPeer(t, transport)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	err := transport.SendRaw(ctx, make([]byte, 3*DefaultPacketSize), func(sent, total int) {
		calls++
		if sent != DefaultPacketSize-HeaderSize || total != 3*DefaultPacketSize {
			t.Errorf("unexpected progress: %d/%d", sent, total)
		}
		cancel()
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("SendRaw = %v, callbacks = %d", err, calls)
	}
}

func TestTransportContinuationCancellation(t *testing.T) {
	for _, operation := range udpOperations[1:3] {
		t.Run(operation.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			packets := 0
			transport := newUDPTestPeer(t, func(packet *Packet) []*Packet {
				if packet.ID != PacketFastboot {
					return udpTestReply(packet)
				}
				packets++
				if packets == 1 {
					return []*Packet{NewFastbootPacket(packet.Sequence, []byte("partial"), true)}
				}
				cancel()
				return nil
			})
			connectUDPTestPeer(t, transport)
			start := time.Now()
			if err := operation.run(ctx, transport); !errors.Is(err, context.Canceled) {
				t.Fatalf("continuation error = %v", err)
			}
			if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
				t.Fatalf("continuation cancellation took %s", elapsed)
			}
		})
	}
}

func TestTransportStaleOnlyCannotSucceed(t *testing.T) {
	for _, phase := range []PacketID{PacketQuery, PacketInit, PacketFastboot} {
		t.Run((&Packet{ID: phase}).String(), func(t *testing.T) {
			transport := newUDPTestPeer(t, func(packet *Packet) []*Packet {
				response := udpTestReply(packet)[0]
				if packet.ID == phase {
					response.Sequence--
				}
				return []*Packet{response}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			err := transport.Connect(ctx)
			if phase == PacketFastboot && err == nil {
				_, err = transport.Send(ctx, []byte("getvar:product"))
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("stale response error = %v", err)
			}
		})
	}
}

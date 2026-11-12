package udp

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"
)

// Transport timing constants.
const (
	RetransmitInterval = 500 * time.Millisecond
	MaxRetries         = 120 // ~60 seconds at 500ms intervals
	QueryRetries       = 6   // Fewer retries for initial query
)

// Response size limits.
const (
	MaxResponseSize        = 1 * mb // 1 MB max total response size
	MaxContinuationPackets = 1024   // Max packet count (~1MB at 1020 bytes/pkt)
)

// Size unit constant (local to avoid import cycle).
const mb = 1 << 20

// Transport manages UDP communication with a fastboot device.
type Transport struct {
	ip            string
	port          int
	conn          *net.UDPConn
	sequence      uint16
	maxPacketSize uint16
}

// NewTransport creates a new UDP transport for the given IP.
func NewTransport(ip string) *Transport {
	return &Transport{
		ip:            ip,
		port:          DefaultPort,
		maxPacketSize: DefaultPacketSize,
	}
}

// WithPort sets a custom port (default 5554).
func (t *Transport) WithPort(port int) *Transport {
	t.port = port
	return t
}

// Connect establishes connection and performs protocol handshake.
func (t *Transport) Connect(ctx context.Context) (err error) {
	var dialer net.Dialer
	rawConn, err := dialer.DialContext(ctx, "udp", net.JoinHostPort(t.ip, strconv.Itoa(t.port)))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("dialing UDP: %w", err)
	}
	conn := rawConn.(*net.UDPConn)
	t.conn = conn
	finish := closeOnCancel(ctx, conn)
	defer func() {
		finish(&err)
		if err != nil {
			conn.Close()
			t.conn = nil
		}
	}()
	if err := t.handshake(ctx); err != nil {
		return fmt.Errorf("handshake failed: %w", err)
	}
	return nil
}

// closeOnCancel closes the captured socket, not mutable transport state. The
// returned function joins cancellation before the operation releases ownership.
func closeOnCancel(ctx context.Context, conn *net.UDPConn) func(*error) {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		conn.Close()
		close(done)
	})
	return func(err *error) {
		if !stop() {
			<-done
		}
		if ctx.Err() != nil {
			*err = ctx.Err()
		}
	}
}

func (t *Transport) handshake(ctx context.Context) error {
	queryPkt := NewQueryPacket(0)
	resp, err := t.sendAndReceive(ctx, queryPkt, QueryRetries)
	if err != nil {
		return fmt.Errorf("query failed: %w", err)
	}

	if resp.ID != PacketQuery {
		return fmt.Errorf("expected query response, got %s", resp)
	}

	nextSeq, err := ParseQueryResponse(resp.Data)
	if err != nil {
		return fmt.Errorf("parsing query response: %w", err)
	}
	t.sequence = nextSeq

	initPkt := NewInitPacket(t.sequence, ProtocolVersion, DefaultPacketSize)
	resp, err = t.sendAndReceive(ctx, initPkt, QueryRetries)
	if err != nil {
		return fmt.Errorf("init failed: %w", err)
	}

	if resp.ID != PacketInit {
		return fmt.Errorf("expected init response, got %s", resp)
	}

	version, maxPacket, err := ParseInitResponse(resp.Data)
	if err != nil {
		return fmt.Errorf("parsing init response: %w", err)
	}

	if version == 0 {
		return fmt.Errorf("unsupported protocol version: %d", version)
	}
	if maxPacket < MinPacketSize {
		return fmt.Errorf("peer packet size %d is below minimum %d", maxPacket, MinPacketSize)
	}
	t.maxPacketSize = min(maxPacket, DefaultPacketSize)

	t.sequence++
	return nil
}

// Send transmits a fastboot command and returns the response data.
func (t *Transport) Send(ctx context.Context, data []byte) (response []byte, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if t.conn == nil {
		return nil, fmt.Errorf("not connected")
	}
	defer closeOnCancel(ctx, t.conn)(&err)

	maxData := int(t.maxPacketSize) - HeaderSize
	offset := 0
	for {
		end := offset + maxData
		continuation := false
		if end < len(data) {
			continuation = true
		} else {
			end = len(data)
		}

		chunk := data[offset:end]
		pkt := NewFastbootPacket(t.sequence, chunk, continuation)

		resp, err := t.sendAndReceive(ctx, pkt, MaxRetries)
		if err != nil {
			return nil, err
		}

		if resp.ID == PacketError {
			return nil, fmt.Errorf("device error: %s", string(resp.Data))
		}

		t.sequence++
		offset = end

		if !continuation {
			return t.collectResponse(ctx, resp)
		}
	}
}

// Receive requests data from the device by sending an empty packet.
func (t *Transport) Receive(ctx context.Context) (response []byte, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if t.conn == nil {
		return nil, fmt.Errorf("not connected")
	}
	defer closeOnCancel(ctx, t.conn)(&err)

	pkt := NewFastbootPacket(t.sequence, nil, false)
	resp, err := t.sendAndReceive(ctx, pkt, MaxRetries)
	if err != nil {
		return nil, err
	}

	if resp.ID == PacketError {
		return nil, fmt.Errorf("device error: %s", string(resp.Data))
	}

	t.sequence++
	return t.collectResponse(ctx, resp)
}

// collectResponse gathers multi-packet responses using continuation flag.
func (t *Transport) collectResponse(ctx context.Context, first *Packet) ([]byte, error) {
	result := make([]byte, 0, len(first.Data))
	result = append(result, first.Data...)

	if !first.HasContinuation() {
		return result, nil
	}

	packetCount := 0
	for {
		packetCount++
		if packetCount > MaxContinuationPackets {
			return nil, fmt.Errorf("exceeded maximum continuation packets (%d)", MaxContinuationPackets)
		}

		pkt := NewFastbootPacket(t.sequence, nil, false)
		resp, err := t.sendAndReceive(ctx, pkt, MaxRetries)
		if err != nil {
			return nil, err
		}

		if resp.ID == PacketError {
			return nil, fmt.Errorf("device error: %s", string(resp.Data))
		}

		if len(result)+len(resp.Data) > MaxResponseSize {
			return nil, fmt.Errorf("exceeded maximum response size (%d bytes)", MaxResponseSize)
		}

		result = append(result, resp.Data...)
		t.sequence++

		if !resp.HasContinuation() {
			break
		}
	}

	return result, nil
}

// sendAndReceive transmits a packet and waits for response with retries.
func (t *Transport) sendAndReceive(ctx context.Context, pkt *Packet, maxRetries int) (*Packet, error) {
	encoded := pkt.Encode()
	recvBuf := make([]byte, MaxPacketSize)

	for attempt := 0; attempt < maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		deadline := time.Now().Add(RetransmitInterval)
		if parentDeadline, ok := ctx.Deadline(); ok && parentDeadline.Before(deadline) {
			deadline = parentDeadline
		}
		if err := t.conn.SetDeadline(deadline); err != nil {
			return nil, fmt.Errorf("setting packet deadline: %w", err)
		}
		if _, err := t.conn.Write(encoded); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				if parentDeadline, ok := ctx.Deadline(); ok && !time.Now().Before(parentDeadline) {
					return nil, context.DeadlineExceeded
				}
				continue
			}
			return nil, fmt.Errorf("sending packet: %w", err)
		}

		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			n, err := t.conn.Read(recvBuf)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					if parentDeadline, ok := ctx.Deadline(); ok && !time.Now().Before(parentDeadline) {
						return nil, context.DeadlineExceeded
					}
					break // retransmit the same sequence after its interval
				}
				return nil, fmt.Errorf("reading response: %w", err)
			}
			resp, err := DecodePacket(recvBuf[:n])
			if err != nil {
				return nil, fmt.Errorf("decoding response: %w", err)
			}
			if resp.Sequence != pkt.Sequence {
				continue // Ignore stale replies without extending the deadline.
			}
			if resp.ID != pkt.ID && resp.ID != PacketError {
				return nil, fmt.Errorf("unexpected response kind %s for %s", resp, pkt)
			}
			return resp, nil
		}
	}

	return nil, fmt.Errorf("no response after %d retries: %w", maxRetries, context.DeadlineExceeded)
}

// SendRaw sends raw data for download transfers.
func (t *Transport) SendRaw(ctx context.Context, data []byte, progress func(sent, total int)) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if t.conn == nil {
		return fmt.Errorf("not connected")
	}
	defer closeOnCancel(ctx, t.conn)(&err)

	maxData := int(t.maxPacketSize) - HeaderSize
	total := len(data)
	sent := 0

	for sent < total {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		end := sent + maxData
		continuation := true
		if end >= total {
			end = total
			continuation = false
		}

		chunk := data[sent:end]
		pkt := NewFastbootPacket(t.sequence, chunk, continuation)
		resp, err := t.sendAndReceive(ctx, pkt, MaxRetries)
		if err != nil {
			return err
		}

		if resp.ID == PacketError {
			return fmt.Errorf("device error during transfer: %s", string(resp.Data))
		}

		t.sequence++
		sent = end

		if progress != nil {
			progress(sent, total)
		}
	}

	return nil
}

// Close closes the UDP connection.
func (t *Transport) Close() error {
	if t.conn != nil {
		err := t.conn.Close()
		t.conn = nil
		return err
	}
	return nil
}

// IsConnected returns true if the transport has an active connection.
func (t *Transport) IsConnected() bool {
	return t.conn != nil
}

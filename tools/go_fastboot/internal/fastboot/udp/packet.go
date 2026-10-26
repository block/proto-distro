// Package udp implements the fastboot UDP transport protocol.
// It handles packet framing, sequence numbers, and retransmission.
package udp

import (
	"encoding/binary"
	"fmt"
)

// PacketID identifies the type of UDP fastboot packet.
type PacketID byte

const (
	PacketError    PacketID = 0x00 // Error response from device
	PacketQuery    PacketID = 0x01 // Initial sync query
	PacketInit     PacketID = 0x02 // Protocol/size negotiation
	PacketFastboot PacketID = 0x03 // Fastboot command/response
)

// Flag bits for packet flags byte.
const (
	FlagContinuation byte = 0x01 // More data follows
)

// Protocol constants.
const (
	HeaderSize        = 4    // Header size in bytes: ID(1) + Flags(1) + Seq(2)
	MinPacketSize     = 512  // Minimum supported packet size in bytes
	DefaultPacketSize = 1024 // Recommended packet size in bytes
	MaxPacketSize     = 1500 // Maximum reasonable UDP packet size in bytes
	ProtocolVersion   = 1    // Fastboot UDP protocol version
	DefaultPort       = 5554 // Default fastboot UDP port
)

// Packet represents a fastboot UDP packet.
type Packet struct {
	ID       PacketID
	Flags    byte
	Sequence uint16
	Data     []byte
}

// Encode serializes the packet to bytes for transmission.
func (p *Packet) Encode() []byte {
	buf := make([]byte, HeaderSize+len(p.Data))
	buf[0] = byte(p.ID)
	buf[1] = p.Flags
	binary.BigEndian.PutUint16(buf[2:4], p.Sequence)
	copy(buf[HeaderSize:], p.Data)
	return buf
}

// DecodePacket parses a received byte slice into a Packet.
func DecodePacket(data []byte) (*Packet, error) {
	if len(data) < HeaderSize {
		return nil, fmt.Errorf("packet too short: %d bytes (min %d)", len(data), HeaderSize)
	}

	return &Packet{
		ID:       PacketID(data[0]),
		Flags:    data[1],
		Sequence: binary.BigEndian.Uint16(data[2:4]),
		Data:     data[HeaderSize:],
	}, nil
}

// NewQueryPacket creates a query packet for initial synchronization.
func NewQueryPacket(seq uint16) *Packet {
	return &Packet{
		ID:       PacketQuery,
		Sequence: seq,
	}
}

// NewInitPacket creates an init packet for protocol negotiation.
func NewInitPacket(seq uint16, version, maxPacketSize uint16) *Packet {
	data := make([]byte, 4)
	binary.BigEndian.PutUint16(data[0:2], version)
	binary.BigEndian.PutUint16(data[2:4], maxPacketSize)
	return &Packet{
		ID:       PacketInit,
		Sequence: seq,
		Data:     data,
	}
}

// NewFastbootPacket creates a fastboot command/data packet.
func NewFastbootPacket(seq uint16, data []byte, continuation bool) *Packet {
	p := &Packet{
		ID:       PacketFastboot,
		Sequence: seq,
		Data:     data,
	}
	if continuation {
		p.Flags = FlagContinuation
	}
	return p
}

// ParseInitResponse extracts version and max packet size from init response data.
func ParseInitResponse(data []byte) (version, maxPacketSize uint16, err error) {
	if len(data) < 4 {
		return 0, 0, fmt.Errorf("init response too short: %d bytes", len(data))
	}
	version = binary.BigEndian.Uint16(data[0:2])
	maxPacketSize = binary.BigEndian.Uint16(data[2:4])
	return version, maxPacketSize, nil
}

// ParseQueryResponse extracts the next expected sequence number from query response.
func ParseQueryResponse(data []byte) (nextSeq uint16, err error) {
	if len(data) < 2 {
		return 0, fmt.Errorf("query response too short: %d bytes", len(data))
	}
	return binary.BigEndian.Uint16(data[0:2]), nil
}

// HasContinuation returns true if the continuation flag is set.
func (p *Packet) HasContinuation() bool {
	return p.Flags&FlagContinuation != 0
}

// String returns a debug representation of the packet.
func (p *Packet) String() string {
	idStr := "UNKNOWN"
	switch p.ID {
	case PacketError:
		idStr = "ERROR"
	case PacketQuery:
		idStr = "QUERY"
	case PacketInit:
		idStr = "INIT"
	case PacketFastboot:
		idStr = "FASTBOOT"
	}
	return fmt.Sprintf("Packet{ID=%s, Flags=0x%02x, Seq=%d, Data=%d bytes}",
		idStr, p.Flags, p.Sequence, len(p.Data))
}

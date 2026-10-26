package udp

import (
	"bytes"
	"testing"
)

func TestPacketEncodeDecode(t *testing.T) {
	tests := []struct {
		name   string
		packet *Packet
	}{
		{
			name:   "query packet",
			packet: NewQueryPacket(0),
		},
		{
			name:   "query packet with seq",
			packet: NewQueryPacket(42),
		},
		{
			name:   "init packet",
			packet: NewInitPacket(1, ProtocolVersion, DefaultPacketSize),
		},
		{
			name:   "fastboot packet empty",
			packet: NewFastbootPacket(5, nil, false),
		},
		{
			name:   "fastboot packet with data",
			packet: NewFastbootPacket(10, []byte("getvar:product"), false),
		},
		{
			name:   "fastboot packet with continuation",
			packet: NewFastbootPacket(15, []byte("data chunk"), true),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := tt.packet.Encode()
			decoded, err := DecodePacket(encoded)
			if err != nil {
				t.Fatalf("decode failed: %v", err)
			}

			if decoded.ID != tt.packet.ID {
				t.Errorf("ID mismatch: got %v, want %v", decoded.ID, tt.packet.ID)
			}
			if decoded.Flags != tt.packet.Flags {
				t.Errorf("Flags mismatch: got %v, want %v", decoded.Flags, tt.packet.Flags)
			}
			if decoded.Sequence != tt.packet.Sequence {
				t.Errorf("Sequence mismatch: got %v, want %v", decoded.Sequence, tt.packet.Sequence)
			}
			if !bytes.Equal(decoded.Data, tt.packet.Data) {
				t.Errorf("Data mismatch: got %v, want %v", decoded.Data, tt.packet.Data)
			}
		})
	}
}

func TestDecodePacketTooShort(t *testing.T) {
	_, err := DecodePacket([]byte{0x01, 0x02})
	if err == nil {
		t.Error("expected error for short packet")
	}
}

func TestParseInitResponse(t *testing.T) {
	// Create init response data: version=1, maxPacket=1024
	data := []byte{0x00, 0x01, 0x04, 0x00}

	version, maxPacket, err := ParseInitResponse(data)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if version != 1 {
		t.Errorf("version: got %d, want 1", version)
	}
	if maxPacket != 1024 {
		t.Errorf("maxPacket: got %d, want 1024", maxPacket)
	}
}

func TestParseQueryResponse(t *testing.T) {
	// Next sequence = 5
	data := []byte{0x00, 0x05}

	seq, err := ParseQueryResponse(data)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if seq != 5 {
		t.Errorf("sequence: got %d, want 5", seq)
	}
}

func TestContinuationFlag(t *testing.T) {
	pkt := NewFastbootPacket(0, []byte("data"), true)
	if !pkt.HasContinuation() {
		t.Error("expected continuation flag to be set")
	}

	pkt2 := NewFastbootPacket(0, []byte("data"), false)
	if pkt2.HasContinuation() {
		t.Error("expected continuation flag to be unset")
	}
}

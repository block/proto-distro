package fwumeta

import (
	"encoding/binary"
	"hash/crc32"
	"testing"
)

func TestMetadataSizeAssumption(t *testing.T) {
	if MetaSize != 96 {
		t.Fatalf("MetaSize = %d, want 96", MetaSize)
	}
}

func TestRoundTrip(t *testing.T) {
	m := &Metadata{
		Version:             0,
		ActiveIndex:         0,
		PreviousActiveIndex: 1,
	}
	// fip-a UUID: 4fd84c93-54ef-463f-a7ef-ae25ff887087 (mixed-endian on disk)
	m.ImgEntry[0].ImgTypeUUID = [16]byte{
		0x83, 0xdf, 0xd5, 0x19, 0xb0, 0x11, 0x7b, 0x45,
		0xbe, 0x2c, 0x75, 0x59, 0xc1, 0x31, 0x42, 0xa5,
	}
	m.ImgEntry[0].Banks[0].ImgUUID = [16]byte{
		0x93, 0x4c, 0xd8, 0x4f, 0xef, 0x54, 0x3f, 0x46,
		0xa7, 0xef, 0xae, 0x25, 0xff, 0x88, 0x70, 0x87,
	}
	m.ImgEntry[0].Banks[0].Accepted = 1
	m.ImgEntry[0].Banks[1].ImgUUID = [16]byte{
		0x52, 0x49, 0xc5, 0x09, 0xbf, 0xd5, 0xaf, 0x45,
		0xac, 0xee, 0x33, 0x53, 0x03, 0x76, 0x6f, 0xb3,
	}
	m.ImgEntry[0].Banks[1].Accepted = 0

	m.CRC32 = ComputeCRC(m)

	buf := Marshal(m)
	if len(buf) != MetaSize {
		t.Fatalf("Marshal produced %d bytes, want %d", len(buf), MetaSize)
	}

	// Verify CRC in buffer
	storedCRC := binary.LittleEndian.Uint32(buf[0:4])
	computedCRC := crc32.ChecksumIEEE(buf[4:])
	if storedCRC != computedCRC {
		t.Fatalf("CRC mismatch in buffer: stored=0x%08x computed=0x%08x", storedCRC, computedCRC)
	}

	m2, err := Unmarshal(buf[:])
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if err := Validate(m2); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	if m2.ActiveIndex != 0 {
		t.Errorf("ActiveIndex = %d, want 0", m2.ActiveIndex)
	}
	if m2.PreviousActiveIndex != 1 {
		t.Errorf("PreviousActiveIndex = %d, want 1", m2.PreviousActiveIndex)
	}
	if m2.ImgEntry[0].Banks[0].Accepted != 1 {
		t.Errorf("Bank[0].Accepted = %d, want 1", m2.ImgEntry[0].Banks[0].Accepted)
	}
	if m2.ImgEntry[0].Banks[1].Accepted != 0 {
		t.Errorf("Bank[1].Accepted = %d, want 0", m2.ImgEntry[0].Banks[1].Accepted)
	}
}

func TestValidateRejectsBadCRC(t *testing.T) {
	m := &Metadata{ActiveIndex: 0, PreviousActiveIndex: 1}
	m.CRC32 = 0xDEADBEEF
	if err := Validate(m); err == nil {
		t.Fatal("expected CRC validation error")
	}
}

func TestValidateRejectsBadIndex(t *testing.T) {
	m := &Metadata{ActiveIndex: 5, PreviousActiveIndex: 0}
	m.CRC32 = ComputeCRC(m)
	if err := Validate(m); err == nil {
		t.Fatal("expected index validation error")
	}
}

func TestSetActive(t *testing.T) {
	m := &Metadata{ActiveIndex: 0, PreviousActiveIndex: 1}
	m.ImgEntry[0].Banks[0].Accepted = 1
	m.ImgEntry[0].Banks[1].Accepted = 0
	m.CRC32 = ComputeCRC(m)

	// Switch to bank 1
	m.PreviousActiveIndex = m.ActiveIndex
	m.ActiveIndex = 1
	m.ImgEntry[0].Banks[1].Accepted = 0
	m.CRC32 = ComputeCRC(m)

	if err := Validate(m); err != nil {
		t.Fatalf("Validate after set-active: %v", err)
	}
	if m.ActiveIndex != 1 {
		t.Errorf("ActiveIndex = %d, want 1", m.ActiveIndex)
	}
	if m.PreviousActiveIndex != 0 {
		t.Errorf("PreviousActiveIndex = %d, want 0", m.PreviousActiveIndex)
	}
}

func TestFormatUUID(t *testing.T) {
	// fip-a bank UUID on disk (little-endian first 3 groups):
	// 4fd84c93-54ef-463f-a7ef-ae25ff887087
	uuid := [16]byte{
		0x93, 0x4c, 0xd8, 0x4f, // LE: 4fd84c93
		0xef, 0x54,             // LE: 54ef
		0x3f, 0x46,             // LE: 463f
		0xa7, 0xef,             // BE: a7ef
		0xae, 0x25, 0xff, 0x88, 0x70, 0x87, // BE: ae25ff887087
	}
	got := FormatUUID(uuid)
	want := "4fd84c93-54ef-463f-a7ef-ae25ff887087"
	if got != want {
		t.Errorf("FormatUUID = %q, want %q", got, want)
	}
}

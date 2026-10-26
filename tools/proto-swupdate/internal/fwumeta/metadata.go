package fwumeta

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"unsafe"
)

const (
	NumBanks  = 2
	NumImages = 1
	MetaSize  = 96 // 4+4+4+4 + (16+16 + 2*(16+4+4))
)

// BankLabels maps FWU bank index to the GPT partition-label suffix used for
// FIP images: bank 0 → fip-a, bank 1 → fip-b.
var BankLabels = [NumBanks]string{"a", "b"}

type ImageProperties struct {
	ImgUUID  [16]byte
	Accepted uint32
	Reserved uint32
}

type ImageEntry struct {
	ImgTypeUUID  [16]byte
	LocationUUID [16]byte
	Banks        [NumBanks]ImageProperties
}

type Metadata struct {
	CRC32               uint32
	Version             uint32
	ActiveIndex         uint32
	PreviousActiveIndex uint32
	ImgEntry            [NumImages]ImageEntry
}

func init() {
	var m Metadata
	if unsafe.Sizeof(m) != MetaSize {
		panic(fmt.Sprintf("Metadata struct size %d != expected %d", unsafe.Sizeof(m), MetaSize))
	}
}

func ComputeCRC(m *Metadata) uint32 {
	var buf [MetaSize]byte
	binary.LittleEndian.PutUint32(buf[0:4], 0) // placeholder for CRC field
	binary.LittleEndian.PutUint32(buf[4:8], m.Version)
	binary.LittleEndian.PutUint32(buf[8:12], m.ActiveIndex)
	binary.LittleEndian.PutUint32(buf[12:16], m.PreviousActiveIndex)
	marshalImageEntry(&m.ImgEntry[0], buf[16:])
	return crc32.ChecksumIEEE(buf[4:])
}

func marshalImageEntry(e *ImageEntry, buf []byte) {
	copy(buf[0:16], e.ImgTypeUUID[:])
	copy(buf[16:32], e.LocationUUID[:])
	off := 32
	for i := range e.Banks {
		marshalImageProps(&e.Banks[i], buf[off:])
		off += 24 // 16 + 4 + 4
	}
}

func marshalImageProps(p *ImageProperties, buf []byte) {
	copy(buf[0:16], p.ImgUUID[:])
	binary.LittleEndian.PutUint32(buf[16:20], p.Accepted)
	binary.LittleEndian.PutUint32(buf[20:24], p.Reserved)
}

func Marshal(m *Metadata) [MetaSize]byte {
	var buf [MetaSize]byte
	binary.LittleEndian.PutUint32(buf[4:8], m.Version)
	binary.LittleEndian.PutUint32(buf[8:12], m.ActiveIndex)
	binary.LittleEndian.PutUint32(buf[12:16], m.PreviousActiveIndex)
	marshalImageEntry(&m.ImgEntry[0], buf[16:])
	crc := crc32.ChecksumIEEE(buf[4:])
	binary.LittleEndian.PutUint32(buf[0:4], crc)
	return buf
}

func Unmarshal(buf []byte) (*Metadata, error) {
	if len(buf) < MetaSize {
		return nil, fmt.Errorf("buffer too small: %d < %d", len(buf), MetaSize)
	}
	m := &Metadata{}
	m.CRC32 = binary.LittleEndian.Uint32(buf[0:4])
	m.Version = binary.LittleEndian.Uint32(buf[4:8])
	m.ActiveIndex = binary.LittleEndian.Uint32(buf[8:12])
	m.PreviousActiveIndex = binary.LittleEndian.Uint32(buf[12:16])
	unmarshalImageEntry(&m.ImgEntry[0], buf[16:])
	return m, nil
}

func unmarshalImageEntry(e *ImageEntry, buf []byte) {
	copy(e.ImgTypeUUID[:], buf[0:16])
	copy(e.LocationUUID[:], buf[16:32])
	off := 32
	for i := range e.Banks {
		unmarshalImageProps(&e.Banks[i], buf[off:])
		off += 24
	}
}

func unmarshalImageProps(p *ImageProperties, buf []byte) {
	copy(p.ImgUUID[:], buf[0:16])
	p.Accepted = binary.LittleEndian.Uint32(buf[16:20])
	p.Reserved = binary.LittleEndian.Uint32(buf[20:24])
}

func Validate(m *Metadata) error {
	if m.ActiveIndex >= NumBanks {
		return fmt.Errorf("invalid active_index %d", m.ActiveIndex)
	}
	if m.PreviousActiveIndex >= NumBanks {
		return fmt.Errorf("invalid previous_active_index %d", m.PreviousActiveIndex)
	}
	expected := ComputeCRC(m)
	if m.CRC32 != expected {
		return fmt.Errorf("CRC mismatch: stored=0x%08x computed=0x%08x", m.CRC32, expected)
	}
	return nil
}

// FindValid returns the path of the first device in devs whose metadata
// reads and CRC-validates, along with the parsed Metadata. Returns "" and
// nil if no device is readable.
func FindValid(devs []string) (string, *Metadata) {
	for _, dev := range devs {
		if m, err := ReadFromDevice(dev); err == nil {
			return dev, m
		}
	}
	return "", nil
}

func ReadFromDevice(path string) (*Metadata, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	buf := make([]byte, MetaSize)
	if _, err := readFull(f, buf, 0); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	m, err := Unmarshal(buf)
	if err != nil {
		return nil, fmt.Errorf("unmarshal %s: %w", path, err)
	}
	if err := Validate(m); err != nil {
		return nil, fmt.Errorf("validate %s: %w", path, err)
	}
	return m, nil
}

func WriteToDevice(path string, m *Metadata) error {
	m.CRC32 = ComputeCRC(m)
	buf := Marshal(m)

	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	if _, err := f.WriteAt(buf[:], 0); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return f.Sync()
}

func FormatUUID(uuid [16]byte) string {
	// Mixed-endian (RFC 4122 / GPT): first 3 groups LE, last 2 BE
	return fmt.Sprintf("%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
		uuid[3], uuid[2], uuid[1], uuid[0],
		uuid[5], uuid[4],
		uuid[7], uuid[6],
		uuid[8], uuid[9],
		uuid[10], uuid[11], uuid[12], uuid[13], uuid[14], uuid[15])
}

func readFull(f *os.File, buf []byte, off int64) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := f.ReadAt(buf[total:], off+int64(total))
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

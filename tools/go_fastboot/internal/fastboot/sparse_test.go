package fastboot

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// Fixtures and the decoder use wire offsets, not production header encoders.
type sparseFixtureChunk struct {
	kind   uint16
	blocks uint32
	data   []byte
}

func sparseFixture(blockSize uint32, fileHeader, chunkHeader uint16, chunks ...sparseFixtureChunk) []byte {
	wire := make([]byte, int(fileHeader))
	binary.LittleEndian.PutUint32(wire[0:4], 0xed26ff3a)
	binary.LittleEndian.PutUint16(wire[4:6], 1)
	binary.LittleEndian.PutUint16(wire[8:10], fileHeader)
	binary.LittleEndian.PutUint16(wire[10:12], chunkHeader)
	binary.LittleEndian.PutUint32(wire[12:16], blockSize)
	binary.LittleEndian.PutUint32(wire[20:24], uint32(len(chunks)))
	var blocks uint32
	for _, chunk := range chunks {
		header := make([]byte, int(chunkHeader))
		binary.LittleEndian.PutUint16(header[0:2], chunk.kind)
		binary.LittleEndian.PutUint32(header[4:8], chunk.blocks)
		binary.LittleEndian.PutUint32(header[8:12], uint32(len(header)+len(chunk.data)))
		for i := 12; i < len(header); i++ {
			header[i] = 0xe7
		}
		wire = append(wire, header...)
		wire = append(wire, chunk.data...)
		blocks += chunk.blocks
	}
	binary.LittleEndian.PutUint32(wire[16:20], blocks)
	for i := 28; i < int(fileHeader); i++ {
		wire[i] = 0xd6
	}
	return wire
}

func sparseTestPath(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "image")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func openSparseFixture(t *testing.T, wire []byte) *SparseImage {
	t.Helper()
	image, err := OpenSparseImage(sparseTestPath(t, wire))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := image.Close(); err != nil {
			t.Error(err)
		}
	})
	return image
}

// Apply each transfer independently, preserving existing bytes for DONT_CARE.
func applySparseWire(t *testing.T, target, wire []byte, blockSize uint32) {
	t.Helper()
	if len(wire) < 28 || binary.LittleEndian.Uint32(wire[:4]) != 0xed26ff3a ||
		binary.LittleEndian.Uint16(wire[4:6]) != 1 || binary.LittleEndian.Uint16(wire[6:8]) != 0 ||
		binary.LittleEndian.Uint16(wire[8:10]) != 28 || binary.LittleEndian.Uint16(wire[10:12]) != 12 ||
		binary.LittleEndian.Uint32(wire[12:16]) != blockSize ||
		uint64(binary.LittleEndian.Uint32(wire[16:20]))*uint64(blockSize) != uint64(len(target)) ||
		binary.LittleEndian.Uint32(wire[24:28]) != 0 {
		t.Fatalf("invalid normalized sparse header: %x", wire[:min(len(wire), 28)])
	}
	count := binary.LittleEndian.Uint32(wire[20:24])
	wire = wire[28:]
	var offset uint64
	for range count {
		if len(wire) < 12 {
			t.Fatal("short chunk header")
		}
		kind := binary.LittleEndian.Uint16(wire[:2])
		length := uint64(binary.LittleEndian.Uint32(wire[4:8])) * uint64(blockSize)
		total := uint64(binary.LittleEndian.Uint32(wire[8:12]))
		if length == 0 || total < 12 || total > uint64(len(wire)) || offset+length > uint64(len(target)) {
			t.Fatal("invalid output chunk bounds")
		}
		switch kind {
		case 0xcac1:
			if total != length+12 {
				t.Fatal("RAW length mismatch")
			}
			copy(target[offset:offset+length], wire[12:total])
		case 0xcac2:
			if total != 16 {
				t.Fatal("FILL length mismatch")
			}
			for i := offset; i < offset+length; i += 4 {
				copy(target[i:i+4], wire[12:16])
			}
		case 0xcac3:
			if total != 12 {
				t.Fatal("DONT_CARE contains payload")
			}
		default:
			t.Fatalf("unexpected output chunk kind %x (CRC must be dropped)", kind)
		}
		offset += length
		wire = wire[total:]
	}
	if offset != uint64(len(target)) || len(wire) != 0 {
		t.Fatal("output extent or chunk count mismatch")
	}
}

func reconstructSparse(t *testing.T, image *SparseImage, limit int64, expected []byte) int {
	t.Helper()
	splits, err := image.CalculateSplits(limit)
	if err != nil {
		t.Fatal(err)
	}
	if len(splits) == 0 {
		t.Fatal("no splits")
	}
	target := bytes.Repeat([]byte{0xa5}, len(expected))
	var buffer []byte
	for _, split := range splits {
		// Dirty the reusable buffer so final-block padding cannot rely on fresh memory.
		for i := range buffer {
			buffer[i] = 0xee
		}
		buffer, err = image.BuildSplitImage(split, buffer)
		if err != nil {
			t.Fatal(err)
		}
		if int64(len(buffer)) != split.WireSize || int64(len(buffer)) > limit {
			t.Fatalf("wire size %d, planned %d, limit %d", len(buffer), split.WireSize, limit)
		}
		applySparseWire(t, target, buffer, image.Header.BlockSize)
	}
	if !bytes.Equal(target, expected) {
		t.Fatal("reconstructed bytes differ, including skips or padding")
	}
	return len(splits)
}

func TestIsSparseImage(t *testing.T) {
	valid := sparseFixture(4096, 28, 12, sparseFixtureChunk{0xcac1, 1, bytes.Repeat([]byte{0xaa}, 4096)})
	for _, tc := range []struct {
		name string
		data []byte
		want bool
	}{
		{"sparse", valid, true}, {"raw", []byte("not sparse"), false},
		{"empty", nil, false}, {"short magic", valid[:3], false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := IsSparseImage(sparseTestPath(t, tc.data))
			if err != nil || got != tc.want {
				t.Fatalf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	for _, path := range []string{t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		if _, err := IsSparseImage(path); err == nil {
			t.Errorf("expected I/O error for %s", path)
		}
	}
}

func TestOpenSparseImage(t *testing.T) {
	data := bytes.Repeat([]byte{0xaa}, 4096)
	image := openSparseFixture(t, sparseFixture(4096, 28, 12, sparseFixtureChunk{0xcac1, 1, data}))
	reconstructSparse(t, image, 4136, data)
}

func TestOpenSparseImageHeaderExtensions(t *testing.T) {
	chunks := []sparseFixtureChunk{
		{0xcac1, 1, bytes.Repeat([]byte{0x35}, 16)},
		{0xcac2, 2, []byte{1, 2, 3, 4}},
		{0xcac4, 0, []byte{0, 1, 2, 3}},
		{0xcac3, 1, nil},
		{0xcac1, 1, bytes.Repeat([]byte{0x76}, 16)},
	}
	wire := sparseFixture(16, 32, 16, chunks...)
	binary.LittleEndian.PutUint16(wire[6:8], 7)
	binary.LittleEndian.PutUint32(wire[24:28], 0x12345678)
	expected := append([]byte(nil), chunks[0].data...)
	expected = append(expected, bytes.Repeat(chunks[1].data, 8)...)
	expected = append(expected, bytes.Repeat([]byte{0xa5}, 16)...)
	expected = append(expected, chunks[4].data...)
	reconstructSparse(t, openSparseFixture(t, wire), 80, expected)
}

func TestOpenSparseImageMalformed(t *testing.T) {
	base := sparseFixture(4, 28, 12, sparseFixtureChunk{0xcac1, 1, []byte{1, 2, 3, 4}})
	set16 := func(offset int, value uint16) func([]byte) []byte {
		return func(b []byte) []byte { binary.LittleEndian.PutUint16(b[offset:], value); return b }
	}
	set32 := func(offset int, value uint32) func([]byte) []byte {
		return func(b []byte) []byte { binary.LittleEndian.PutUint32(b[offset:], value); return b }
	}
	for _, tc := range []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"empty", func([]byte) []byte { return nil }},
		{"short fixed header", func(b []byte) []byte { return b[:27] }},
		{"magic", set32(0, 0x12345678)}, {"major zero", set16(4, 0)}, {"major two", set16(4, 2)},
		{"file header undersized", set16(8, 27)}, {"file header zero", set16(8, 0)},
		{"chunk header undersized", set16(10, 11)}, {"chunk header zero", set16(10, 0)},
		{"truncated file extension", set16(8, 64)},
		{"truncated chunk extension", func([]byte) []byte {
			b := sparseFixture(4, 28, 16, sparseFixtureChunk{0xcac1, 1, []byte{1, 2, 3, 4}})
			return b[:42]
		}},
		{"total size underflow", set32(36, 11)}, {"total size zero", set32(36, 0)},
		{"truncated payload", func(b []byte) []byte { return b[:len(b)-1] }},
		{"total size exceeds file", set32(36, math.MaxUint32)},
		{"RAW short payload", func([]byte) []byte { return sparseFixture(4, 28, 12, sparseFixtureChunk{0xcac1, 1, []byte{1, 2, 3}}) }},
		{"RAW extra payload", func([]byte) []byte {
			return sparseFixture(4, 28, 12, sparseFixtureChunk{0xcac1, 1, []byte{1, 2, 3, 4, 5}})
		}},
		{"FILL short payload", func([]byte) []byte { return sparseFixture(4, 28, 12, sparseFixtureChunk{0xcac2, 1, []byte{1, 2, 3}}) }},
		{"FILL extra payload", func([]byte) []byte {
			return sparseFixture(4, 28, 12, sparseFixtureChunk{0xcac2, 1, []byte{1, 2, 3, 4, 5}})
		}},
		{"DONT_CARE payload", set16(28, 0xcac3)},
		{"CRC blocks", set16(28, 0xcac4)},
		{"CRC short payload", func([]byte) []byte {
			return sparseFixture(4, 28, 12, sparseFixtureChunk{0xcac3, 1, nil}, sparseFixtureChunk{0xcac4, 0, []byte{1, 2, 3}})
		}},
		{"CRC extra payload", func([]byte) []byte {
			return sparseFixture(4, 28, 12, sparseFixtureChunk{0xcac3, 1, nil}, sparseFixtureChunk{0xcac4, 0, []byte{1, 2, 3, 4, 5}})
		}},
		{"unknown type", set16(28, 0xcaff)},
		{"zero block size", set32(12, 0)}, {"unaligned block size", set32(12, 3)},
		{"zero total blocks", set32(16, 0)}, {"missing blocks", set32(16, 2)},
		{"too many blocks", set32(32, 2)},
		{"zero RAW blocks", set32(32, 0)},
		{"zero RAW blocks without payload", func([]byte) []byte {
			return sparseFixture(4, 28, 12, sparseFixtureChunk{0xcac3, 1, nil}, sparseFixtureChunk{0xcac1, 0, nil})
		}},
		{"zero FILL blocks", func([]byte) []byte {
			return sparseFixture(4, 28, 12, sparseFixtureChunk{0xcac3, 1, nil}, sparseFixtureChunk{0xcac2, 0, []byte{1, 2, 3, 4}})
		}},
		{"zero DONT_CARE blocks", func([]byte) []byte {
			return sparseFixture(4, 28, 12, sparseFixtureChunk{0xcac3, 1, nil}, sparseFixtureChunk{0xcac3, 0, nil})
		}},
		{"zero chunks", set32(20, 0)}, {"missing chunk", set32(20, 2)},
		{"untrusted huge chunk count", set32(20, math.MaxUint32)},
		{"undeclared trailing bytes", func(b []byte) []byte { return append(b, 0) }},
		{"undeclared extra chunk", func([]byte) []byte {
			b := sparseFixture(4, 28, 12, sparseFixtureChunk{0xcac3, 1, nil}, sparseFixtureChunk{0xcac4, 0, []byte{1, 2, 3, 4}})
			binary.LittleEndian.PutUint32(b[20:], 1)
			return b
		}},
		{"signed logical size overflow", func([]byte) []byte {
			return sparseFixture(math.MaxUint32-3, 28, 12, sparseFixtureChunk{0xcac3, math.MaxUint32, nil})
		}},
		{"RAW multiplication overflow", func(b []byte) []byte {
			binary.LittleEndian.PutUint32(b[12:], 1<<31)
			binary.LittleEndian.PutUint32(b[16:], 2)
			binary.LittleEndian.PutUint32(b[32:], 2)
			return b
		}},
		{"cumulative block overflow", func([]byte) []byte {
			return sparseFixture(4, 28, 12, sparseFixtureChunk{0xcac3, math.MaxUint32, nil}, sparseFixtureChunk{0xcac3, 2, nil})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			image, err := OpenSparseImage(sparseTestPath(t, tc.mutate(bytes.Clone(base))))
			if image != nil {
				image.Close()
			}
			if err == nil {
				t.Fatal("accepted malformed sparse image")
			}
		})
	}
	for _, path := range []string{t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		image, err := OpenSparseImage(path)
		if image != nil {
			image.Close()
		}
		if err == nil {
			t.Fatalf("accepted nonregular or missing source %s", path)
		}
	}
}

func TestCalculateSplitsBoundaries(t *testing.T) {
	for _, tc := range []struct {
		blocks    int
		limit     int64
		wantParts int
		wantErr   bool
	}{
		{1, 4136, 1, false}, {1, 4135, 0, true},
		{3, 4160, 3, false}, {3, 4159, 0, true},
		{10, 5000, 10, false},
		{1, -1, 0, true}, {1, 0, 0, true}, {1, 1, 0, true}, {1, 28, 0, true}, {1, 40, 0, true},
	} {
		t.Run(fmt.Sprintf("blocks%d_limit%d", tc.blocks, tc.limit), func(t *testing.T) {
			data := make([]byte, tc.blocks*4096)
			for i := range data {
				data[i] = byte(i/4096 + i%251)
			}
			image := openSparseFixture(t, sparseFixture(4096, 28, 12, sparseFixtureChunk{0xcac1, uint32(tc.blocks), data}))
			if tc.wantErr {
				if _, err := image.CalculateSplits(tc.limit); err == nil {
					t.Fatal("expected planning failure before building any part")
				}
				return
			}
			if got := reconstructSparse(t, image, tc.limit, data); got != tc.wantParts {
				t.Fatalf("parts %d, want %d", got, tc.wantParts)
			}
		})
	}
}

func TestCalculateSplitsFillOverhead(t *testing.T) {
	var chunks []sparseFixtureChunk
	var expected []byte
	for i := byte(1); i <= 10; i++ {
		pattern := []byte{i, i + 1, i + 2, i + 3}
		chunks = append(chunks, sparseFixtureChunk{0xcac2, 3, pattern})
		expected = append(expected, bytes.Repeat(pattern, 3*4096/4)...)
	}
	image := openSparseFixture(t, sparseFixture(4096, 28, 12, chunks...))
	if got := reconstructSparse(t, image, 84, expected); got != 5 {
		t.Fatalf("parts %d, want 5 including every FILL header", got)
	}
	if _, err := image.CalculateSplits(67); err == nil {
		t.Fatal("middle FILL requires 68 bytes")
	}
	reconstructSparse(t, image, 68, expected)
	// A huge logical FILL must remain four payload bytes, not become RAW blocks.
	large := openSparseFixture(t, sparseFixture(4096, 28, 12, sparseFixtureChunk{0xcac2, math.MaxUint32, []byte{1, 2, 3, 4}}))
	splits, err := large.CalculateSplits(44)
	if err != nil || len(splits) != 1 {
		t.Fatalf("large FILL: %v, parts %d", err, len(splits))
	}
	wire, err := large.BuildSplitImage(splits[0], nil)
	if err != nil || len(wire) != 44 || binary.LittleEndian.Uint16(wire[28:30]) != 0xcac2 ||
		binary.LittleEndian.Uint32(wire[32:36]) != math.MaxUint32 || !bytes.Equal(wire[40:], []byte{1, 2, 3, 4}) {
		t.Fatalf("large FILL expanded or changed: bytes=%d, error=%v", len(wire), err)
	}
}

func TestBuildSplitImageSkipsZerosAndCRC(t *testing.T) {
	const blockSize = 16
	chunks := []sparseFixtureChunk{
		{0xcac4, 0, []byte{1, 2, 3, 4}}, {0xcac3, 1, nil},
		{0xcac1, 1, make([]byte, blockSize)}, {0xcac3, 1, nil},
		{0xcac4, 0, []byte{5, 6, 7, 8}}, {0xcac2, 2, []byte{9, 10, 11, 12}},
		{0xcac1, 1, bytes.Repeat([]byte{0x42}, blockSize)}, {0xcac3, 1, nil},
		{0xcac4, 0, []byte{13, 14, 15, 16}},
	}
	expected := bytes.Repeat([]byte{0xa5}, 7*blockSize)
	clear(expected[blockSize : 2*blockSize])
	copy(expected[3*blockSize:5*blockSize], bytes.Repeat([]byte{9, 10, 11, 12}, 8))
	copy(expected[5*blockSize:6*blockSize], chunks[6].data)
	image := openSparseFixture(t, sparseFixture(blockSize, 28, 12, chunks...))
	if parts := reconstructSparse(t, image, 80, expected); parts < 2 {
		t.Fatal("fixture must split")
	}
	reconstructSparse(t, image, 1024, expected)
	allSkip := openSparseFixture(t, sparseFixture(blockSize, 28, 12, sparseFixtureChunk{0xcac3, 7, nil}))
	reconstructSparse(t, allSkip, 40, bytes.Repeat([]byte{0xa5}, 7*blockSize))
}

func TestOpenRawImagePadding(t *testing.T) {
	for _, size := range []int{1, 4095, 4096, 4097, 10*4096 + 37} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			data := make([]byte, size)
			for i := range data {
				data[i] = byte(i%251 + 1)
			}
			image, err := openRawImage(sparseTestPath(t, data))
			if err != nil {
				t.Fatal(err)
			}
			defer image.Close()
			expected := make([]byte, ((size-1)/4096+1)*4096)
			copy(expected, data)
			reconstructSparse(t, image, 5000, expected)
		})
	}
}

func TestOpenRawImageInvalid(t *testing.T) {
	for _, path := range []string{sparseTestPath(t, nil), t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		image, err := openRawImage(path)
		if image != nil {
			image.Close()
		}
		if err == nil {
			t.Fatalf("accepted invalid raw source %s", path)
		}
	}
	image, err := openRawImage(sparseTestPath(t, []byte{1}))
	if err != nil {
		t.Fatal(err)
	}
	defer image.Close()
	for _, limit := range []int64{-1, 0, 1, 28, 40, 4135} {
		if _, err := image.CalculateSplits(limit); err == nil {
			t.Errorf("accepted raw limit %d", limit)
		}
	}
}

func TestRawImageBlockCount(t *testing.T) {
	// Test representability without requiring a filesystem that supports 16 TiB files.
	const maximum = int64(math.MaxUint32) * 4096
	for _, tc := range []struct {
		size   int64
		blocks uint32
	}{
		{-1, 0}, {0, 0}, {1, 1}, {4095, 1}, {4096, 1}, {4097, 2},
		{maximum - 1, math.MaxUint32}, {maximum, math.MaxUint32},
		{maximum + 1, 0}, {math.MaxInt64, 0},
	} {
		blocks, err := rawImageBlockCount(tc.size)
		if blocks != tc.blocks || (err != nil) != (tc.blocks == 0) {
			t.Errorf("size %d: blocks=%d, err=%v; want blocks=%d", tc.size, blocks, err, tc.blocks)
		}
	}
}

func TestBuildSplitImageSourceTruncated(t *testing.T) {
	for _, raw := range []bool{false, true} {
		t.Run(fmt.Sprintf("raw%v", raw), func(t *testing.T) {
			data := bytes.Repeat([]byte{0x72}, 3*4096+1)
			if !raw {
				data = sparseFixture(4096, 28, 12, sparseFixtureChunk{0xcac1, 3, data[:3*4096]})
			}
			path := sparseTestPath(t, data)
			var image *SparseImage
			var err error
			if raw {
				image, err = openRawImage(path)
			} else {
				image, err = OpenSparseImage(path)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer image.Close()
			splits, err := image.CalculateSplits(5000)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := image.BuildSplitImage(splits[0], nil); err != nil {
				t.Fatal(err)
			}
			if err := os.Truncate(path, int64(len(data)-1)); err != nil {
				t.Fatal(err)
			}
			if _, err := image.BuildSplitImage(splits[len(splits)-1], nil); err == nil {
				t.Fatal("source EOF mistaken for virtual padding")
			}
		})
	}
}

func TestParseHexSize(t *testing.T) {
	for _, tc := range []struct {
		input    string
		expected uint64
	}{
		{"1", 1}, {"0001", 1}, {"10000000", 0x10000000}, {"0x10000000", 0x10000000},
		{"ff", 255}, {"0xFF", 255}, {"0XfF", 255},
		{"8000000000000000", 1 << 63}, {"ffffffffffffffff", math.MaxUint64}, {"0XFFFFFFFFFFFFFFFF", math.MaxUint64},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := parseHexSize(tc.input)
			if err != nil || got != tc.expected {
				t.Fatalf("got %d, %v; want %d", got, err, tc.expected)
			}
		})
	}
	for _, input := range []string{"", "0", "000", "0x0", "0X0", "0x", "0X", " 1", "1 ", "\t1", "1\n", "+1", "-1", "1_0", "0x_1", "ffjunk", "1g", "0x0x1", "10000000000000000", "0x10000000000000000", "１", "1\x00"} {
		t.Run(fmt.Sprintf("invalid_%q", input), func(t *testing.T) {
			if _, err := parseHexSize(input); err == nil {
				t.Fatalf("accepted %q", input)
			}
		})
	}
}

func TestEffectiveDownloadSize(t *testing.T) {
	for _, tc := range []struct {
		advertised uint64
		expected   int64
	}{
		{1, 1}, {4136, 4136}, {256<<20 - 1, 256<<20 - 1}, {256 << 20, 256 << 20},
		{256<<20 + 1, 256 << 20}, {math.MaxUint32, 256 << 20}, {1 << 63, 256 << 20}, {math.MaxUint64, 256 << 20},
	} {
		t.Run(fmt.Sprint(tc.advertised), func(t *testing.T) {
			got, err := effectiveDownloadSize(tc.advertised)
			if err != nil || got != tc.expected {
				t.Fatalf("got %d, %v; want %d", got, err, tc.expected)
			}
		})
	}
	if _, err := effectiveDownloadSize(0); err == nil {
		t.Fatal("accepted zero limit")
	}
}

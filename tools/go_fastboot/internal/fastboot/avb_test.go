package fastboot

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type avbQueryClient struct {
	values  map[string]string
	queries []string
	onQuery func(string) error
}

func (c *avbQueryClient) GetVar(ctx context.Context, name string) (string, error) {
	c.queries = append(c.queries, name)
	if c.onQuery != nil {
		if err := c.onQuery(name); err != nil {
			return "", err
		}
	}
	return c.values[name], nil
}

func avbClient(capacity int64) *avbQueryClient {
	return &avbQueryClient{values: map[string]string{
		"partition-size:rootfs_a": fmt.Sprintf("0x%x", capacity),
		"max-download-size":       "0X1000",
	}}
}

func avbSources(t *testing.T, rawSize int) (string, [3][]byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "miner-image-test.rootfs.ext4")
	var data [3][]byte
	for i, size := range []int{rawSize, 5632, 512} {
		data[i] = make([]byte, size)
		for j := range data[i] {
			data[i][j] = byte((j + i*31) % 251) // Includes literal zeros.
		}
		if err := os.WriteFile(path+[]string{"", ".avb.body", ".avb.tail"}[i], data[i], 0600); err != nil {
			t.Fatal(err)
		}
	}
	return path, data
}

// Decode wire bytes independently of production header types and sparse helpers.
// Return RAW intervals so tests also prove writes never touch the omitted gap.
func applyAVBSplit(t *testing.T, target *os.File, wire []byte, capacity int64) [][2]int64 {
	t.Helper()
	if len(wire) < 28 || binary.LittleEndian.Uint32(wire[:4]) != 0xed26ff3a ||
		binary.LittleEndian.Uint16(wire[4:6]) != 1 || binary.LittleEndian.Uint16(wire[6:8]) != 0 ||
		binary.LittleEndian.Uint16(wire[8:10]) != 28 || binary.LittleEndian.Uint16(wire[10:12]) != 12 ||
		binary.LittleEndian.Uint32(wire[12:16]) != 512 || int64(binary.LittleEndian.Uint32(wire[16:20])) != capacity/512 {
		t.Fatalf("invalid sparse header: %x", wire[:min(len(wire), 28)])
	}
	count := binary.LittleEndian.Uint32(wire[20:24])
	wire = wire[28:]
	var offset int64
	var writes [][2]int64
	for range count {
		if len(wire) < 12 {
			t.Fatal("short chunk header")
		}
		kind := binary.LittleEndian.Uint16(wire[:2])
		length := int64(binary.LittleEndian.Uint32(wire[4:8])) * 512
		total := int64(binary.LittleEndian.Uint32(wire[8:12]))
		if total < 12 || total > int64(len(wire)) || offset+length > capacity {
			t.Fatal("invalid chunk bounds")
		}
		switch kind {
		case 0xcac1:
			if total != length+12 {
				t.Fatal("RAW chunk length mismatch")
			}
			if _, err := target.WriteAt(wire[12:total], offset); err != nil {
				t.Fatal(err)
			}
			writes = append(writes, [2]int64{offset, offset + length})
		case 0xcac3:
			if total != 12 {
				t.Fatal("DONT_CARE contains data")
			}
		default:
			t.Fatalf("unexpected chunk type %x", kind)
		}
		offset += length
		wire = wire[total:]
	}
	if offset != capacity || len(wire) != 0 {
		t.Fatalf("split ends at %d, trailing bytes %d", offset, len(wire))
	}
	return writes
}

func TestPrepareAVBImageReconstructSplits(t *testing.T) {
	for _, geometry := range []struct{ capacity, tail, blocks int64 }{
		{1 << 30, 1073741312, 2097152}, {128 << 10, 130560, 256},
	} {
		for _, limits := range []struct{ rawSize, prepare, transfer int }{
			{4096, 576, 576},
			{4096, 4096, 4096}, {61952, 4096, 4096},
			{4096, 65536, 4096}, {61952, 65536, 4096},
			{114688, 262144, 4096}, // A RAW chunk larger than the 64 KiB copy buffer.
		} {
			rawSize := limits.rawSize
			t.Run(fmt.Sprintf("%d/%d/%d/%d", geometry.capacity, rawSize, limits.prepare, limits.transfer), func(t *testing.T) {
				path, sources := avbSources(t, rawSize)
				client := avbClient(geometry.capacity)
				client.values["max-download-size"] = fmt.Sprintf("%x", limits.prepare)
				prepared, missing, err := PrepareAVBImage(context.Background(), path, "rootfs_a", geometry.capacity, client.GetVar)
				if err != nil || prepared == "" || len(missing) != 0 {
					t.Fatalf("prepare = %q, %v, %v", prepared, missing, err)
				}
				defer os.Remove(prepared)
				info, err := os.Stat(prepared)
				if err != nil || info.Mode().Perm() != 0600 || info.Size() > int64(rawSize+5632+512+4096) {
					t.Fatalf("output not private or not sparse: %v, %v", info, err)
				}
				if !reflect.DeepEqual(client.queries, []string{"partition-size:rootfs_a", "max-download-size"}) {
					t.Fatalf("queries: %v", client.queries)
				}
				sparse, err := OpenSparseImage(prepared)
				if err != nil {
					t.Fatal(err)
				}
				defer sparse.Close()
				if int64(sparse.Header.TotalBlocks) != geometry.blocks {
					t.Fatal("wrong logical block count")
				}
				if limits.prepare > limits.transfer {
					var oversized bool
					for _, chunk := range sparse.Chunks {
						oversized = oversized || (chunk.Header.ChunkType == chunkTypeRaw && int64(chunk.Header.TotalSize) > int64(limits.transfer))
					}
					if !oversized {
						t.Fatal("fixture has no RAW chunk exceeding the later download limit")
					}
				}
				target, err := os.CreateTemp(t.TempDir(), "partition")
				if err != nil {
					t.Fatal(err)
				}
				defer target.Close()
				if err := target.Truncate(geometry.capacity); err != nil {
					t.Fatal(err)
				}
				gapStart := int64(rawSize + 5632)
				// Seed start, middle and end without allocating or writing a 1 GiB gap.
				seeds := []int64{gapStart, (gapStart + geometry.tail) / 2, geometry.tail - 512}
				sentinel := bytes.Repeat([]byte{0xa7}, 512)
				for _, offset := range seeds {
					if _, err := target.WriteAt(sentinel, offset); err != nil {
						t.Fatal(err)
					}
				}
				splits, err := sparse.CalculateSplits(int64(limits.transfer))
				if err != nil {
					t.Fatal(err)
				}
				var buffer []byte
				var rawTransfers, bodyTransfers int
				for _, split := range splits {
					wire, err := sparse.BuildSplitImage(split, buffer)
					if err != nil {
						t.Fatal(err)
					}
					buffer = wire
					if len(wire) > limits.transfer {
						t.Fatalf("download %d > %d", len(wire), limits.transfer)
					}
					var wroteRaw, wroteBody bool
					for _, span := range applyAVBSplit(t, target, wire, geometry.capacity) {
						if span[0] < geometry.tail && span[1] > gapStart {
							t.Fatalf("write in gap: %v", span)
						}
						wroteRaw = wroteRaw || span[0] < int64(rawSize)
						wroteBody = wroteBody || (span[0] >= int64(rawSize) && span[0] < gapStart)
					}
					if wroteRaw {
						rawTransfers++
					}
					if wroteBody {
						bodyTransfers++
					}
				}
				if rawTransfers < 2 || bodyTransfers < 2 {
					t.Fatalf("not split: raw %d, body %d", rawTransfers, bodyTransfers)
				}
				for i, offset := range []int64{0, int64(rawSize), geometry.tail} {
					got := make([]byte, len(sources[i]))
					if _, err := target.ReadAt(got, offset); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(got, sources[i]) {
						t.Fatalf("source %d differs at offset %d", i, offset)
					}
					unchanged, err := os.ReadFile(path + []string{"", ".avb.body", ".avb.tail"}[i])
					if err != nil || !bytes.Equal(unchanged, sources[i]) {
						t.Fatalf("source %d changed: %v", i, err)
					}
				}
				for _, offset := range seeds {
					got := make([]byte, 512)
					if _, err := target.ReadAt(got, offset); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(got, sentinel) {
						t.Fatalf("gap changed at %d", offset)
					}
				}
			})
		}
	}
}

func TestPrepareAVBImageMissingCompanions(t *testing.T) {
	for _, absent := range [][]string{{".avb.body"}, {".avb.tail"}, {".avb.body", ".avb.tail"}} {
		t.Run(fmt.Sprint(absent), func(t *testing.T) {
			path, _ := avbSources(t, 4096)
			var want []string
			for _, suffix := range absent {
				if err := os.Remove(path + suffix); err != nil {
					t.Fatal(err)
				}
				want = append(want, path+suffix)
			}
			// Missing metadata must not even require the raw file or usable TMPDIR.
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "absent"))
			client := avbClient(1 << 30)
			prepared, missing, err := PrepareAVBImage(context.Background(), path, "rootfs_a", 1<<30, client.GetVar)
			if err != nil || prepared != "" || !reflect.DeepEqual(missing, want) || len(client.queries) != 0 {
				t.Fatalf("prepare = %q %v %v, queries %v", prepared, missing, err, client.queries)
			}
		})
	}
}

func assertNoAVBOutput(t *testing.T, directory, path string, missing []string, err error) {
	t.Helper()
	if err == nil || path != "" || len(missing) != 0 {
		t.Fatalf("expected fatal error, got %q %v %v", path, missing, err)
	}
	files, readErr := os.ReadDir(directory)
	if readErr != nil || len(files) != 0 {
		t.Fatalf("output survived: %v %v", files, readErr)
	}
}

func TestPrepareAVBImageInvalidGeometry(t *testing.T) {
	for _, variable := range []string{"partition-size:rootfs_a", "max-download-size"} {
		for _, value := range []string{"", "0", "0x", "-1", "+1000", "1000garbage", "0x1000 trailing", " 1000", "1000\n", "1_000", "0x0X1000", "10000000000000000"} {
			t.Run(variable+"/"+value, func(t *testing.T) {
				path, _ := avbSources(t, 4096)
				output := t.TempDir()
				t.Setenv("TMPDIR", output)
				client := avbClient(128 << 10)
				client.values[variable] = value
				prepared, missing, err := PrepareAVBImage(context.Background(), path, "rootfs_a", 128<<10, client.GetVar)
				assertNoAVBOutput(t, output, prepared, missing, err)
			})
		}
	}
	for _, tt := range []struct{ name, variable, value string }{
		{"wrong capacity", "partition-size:rootfs_a", "40000000"},
		{"too small limit", "max-download-size", "23f"}, // 575 bytes, one below a sector plus worst-case headers.
		{"negative reserve", "max-download-size", "1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path, _ := avbSources(t, 4096)
			output := t.TempDir()
			t.Setenv("TMPDIR", output)
			client := avbClient(128 << 10)
			client.values[tt.variable] = tt.value
			prepared, missing, err := PrepareAVBImage(context.Background(), path, "rootfs_a", 128<<10, client.GetVar)
			assertNoAVBOutput(t, output, prepared, missing, err)
		})
	}
}

// Cancel only after a partial output exists, without timing or goroutine races.
type cancelOnAVBOutput struct {
	context.Context
	directory string
	observed  bool
}

func (c *cancelOnAVBOutput) Err() error {
	files, _ := filepath.Glob(filepath.Join(c.directory, "go-fastboot-avb-*.sparse"))
	if len(files) != 0 {
		c.observed = true
		return context.Canceled
	}
	return nil
}

func TestPrepareAVBImageFailuresCleanOutput(t *testing.T) {
	for _, name := range []string{"missing raw", "raw directory", "body directory", "tail directory", "empty raw", "empty body", "empty tail", "bad tail", "sparse raw", "stat error", "bad companion with missing pair", "unreadable", "query error", "download query error", "copy raw failure", "copy body failure", "copy tail failure", "canceled", "cancel partial", "temporary creation", "block overflow", "unknown capacity"} {
		t.Run(name, func(t *testing.T) {
			path, _ := avbSources(t, 61952)
			output := t.TempDir()
			t.Setenv("TMPDIR", output)
			client := avbClient(128 << 10)
			capacity := int64(128 << 10)
			ctx := context.Background()
			var cancelContext *cancelOnAVBOutput
			suffix := ""
			switch name {
			case "raw directory", "body directory", "tail directory", "bad companion with missing pair":
				if name == "body directory" || name == "bad companion with missing pair" {
					suffix = ".avb.body"
				}
				if name == "tail directory" {
					suffix = ".avb.tail"
				}
				if err := os.Remove(path + suffix); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path+suffix, 0700); err != nil {
					t.Fatal(err)
				}
				if name == "bad companion with missing pair" {
					if err := os.Remove(path + ".avb.tail"); err != nil {
						t.Fatal(err)
					}
				}
			case "missing raw":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "empty raw", "empty body", "empty tail", "bad tail":
				if name == "empty body" {
					suffix = ".avb.body"
				}
				if name == "empty tail" || name == "bad tail" {
					suffix = ".avb.tail"
				}
				size := int64(0)
				if name == "bad tail" {
					size = 511
				}
				if err := os.Truncate(path+suffix, size); err != nil {
					t.Fatal(err)
				}
			case "sparse raw":
				file, err := os.OpenFile(path, os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, err = file.Write([]byte{0x3a, 0xff, 0x26, 0xed})
				closeErr := file.Close()
				if err != nil || closeErr != nil {
					t.Fatal(err, closeErr)
				}
			case "stat error":
				if err := os.Remove(path + ".avb.body"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".avb.body", path+".avb.body"); err != nil {
					t.Fatal(err)
				}
			case "unreadable":
				if os.Getuid() == 0 {
					t.Skip("root bypasses file mode")
				}
				if err := os.Chmod(path+".avb.body", 0); err != nil {
					t.Fatal(err)
				}
			case "query error", "download query error":
				client.onQuery = func(variable string) error {
					if name == "query error" || variable == "max-download-size" {
						return errors.New("query failed")
					}
					return nil
				}
			case "copy raw failure", "copy body failure", "copy tail failure":
				if name == "copy body failure" {
					suffix = ".avb.body"
				}
				if name == "copy tail failure" {
					suffix = ".avb.tail"
				}
				client.onQuery = func(variable string) error {
					if variable == "max-download-size" {
						return os.Truncate(path+suffix, 0)
					}
					return nil
				}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "cancel partial":
				cancelContext = &cancelOnAVBOutput{Context: ctx, directory: output}
				ctx = cancelContext
			case "temporary creation":
				t.Setenv("TMPDIR", filepath.Join(output, "absent"))
			case "block overflow":
				capacity = (1 << 32) * 512
			case "unknown capacity":
				capacity = 0
			}
			prepared, missing, err := PrepareAVBImage(ctx, path, "rootfs_a", capacity, client.GetVar)
			assertNoAVBOutput(t, output, prepared, missing, err)
			if cancelContext != nil && !cancelContext.observed {
				t.Fatal("never reached partial output")
			}
			if (name == "canceled" || name == "cancel partial") && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
			if (name == "copy raw failure" || name == "copy body failure" || name == "copy tail failure") && !errors.Is(err, io.EOF) {
				t.Fatalf("did not fail copying: %v", err)
			}
		})
	}
}

func TestPrepareAVBImageNoGapAndLargeLimit(t *testing.T) {
	path, _ := avbSources(t, 4096)
	capacity := int64(4096 + 5632 + 512)
	client := avbClient(capacity)
	client.values["partition-size:rootfs_a"] = fmt.Sprintf("%X", capacity)
	client.values["max-download-size"] = "ffffffffffffffff"
	prepared, missing, err := PrepareAVBImage(context.Background(), path, "rootfs_a", capacity, client.GetVar)
	if err != nil || len(missing) != 0 {
		t.Fatalf("%v %v", missing, err)
	}
	defer os.Remove(prepared)
	sparse, err := OpenSparseImage(prepared)
	if err != nil {
		t.Fatal(err)
	}
	defer sparse.Close()
	if len(sparse.Chunks) != 3 {
		t.Fatalf("chunks = %d, want 3 RAW chunks", len(sparse.Chunks))
	}
	for _, chunk := range sparse.Chunks {
		if chunk.Header.ChunkType != chunkTypeRaw {
			t.Fatal("no-gap image contains non-RAW chunk")
		}
	}
}

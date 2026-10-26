package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"proto-distro/tools/flashimage"
	"testing"
)

func TestPayloadPreparationThroughExecution(t *testing.T) {
	for _, tc := range []struct {
		name, partition string
		verity          bool
	}{
		{"plain rootfs writes every byte", "rootfs_a", false},
		{"verity leaves other payloads unchanged", "bootfs", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			image := make([]byte, 65536)
			path := filepath.Join(t.TempDir(), "image")
			if err := os.WriteFile(path, image, 0600); err != nil {
				t.Fatal(err)
			}
			plan := flashPlan{partitions: []partition{
				{Partition: flashimage.Partition{Phase: 0x0c, Name: tc.partition, DFUType: "FileSystem", ArtifactKey: tc.partition, Capacity: flashimage.CapacityBounded, AVB: tc.partition == "rootfs_a"}, device: "mmc1", operation: "P", capacity: 65536},
				{Partition: flashimage.Partition{Phase: 0x0d, Name: "rootfs_b", DFUType: "FileSystem", Offset: 0x10000, ArtifactKey: "rootfs_b"}, device: "mmc1", operation: "PE"},
			}}
			items, cleanup, err := preparePayloads(context.Background(), plan, map[string]string{tc.partition: path}, tc.verity)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := cleanup(); err != nil {
					t.Error(err)
				}
			}()
			session, target := newStorageSession(t, map[byte]int{0x0c: 65536, 0x0d: 512})
			if err := (&stm32progClient{session: session}).program(context.Background(), plan, items); err != nil {
				t.Fatal(err)
			}
			expected := append([]byte(nil), image...)
			if !bytes.Equal(target.storage[0x0c], expected) {
				t.Fatal("payload execution altered incorrect bytes")
			}
			if !bytes.Equal(target.storage[0x0d], bytes.Repeat([]byte{0xa5}, 512)) {
				t.Fatal("reserved slot modified")
			}
		})
	}
}

func TestArtifactPreflightRejectsEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	plan := testStoragePlan()
	if _, _, err := preparePayloads(context.Background(), plan, map[string]string{"bootfs": path, "rootfs_a": path}, false); err == nil {
		t.Fatal("empty payload accepted")
	}
}

func TestVerityPayloadPreservesOmittedBytes(t *testing.T) {
	for _, tc := range []struct{ raw, capacity int64 }{{4096, 1 << 30}, {61952, 1 << 30}, {4096, 128 << 10}, {61952, 128 << 10}} {
		t.Run(fmt.Sprintf("%d/%d", tc.raw, tc.capacity), func(t *testing.T) {
			original, capacity := tc.raw, tc.capacity
			dir := t.TempDir()
			t.Setenv("TMPDIR", filepath.Join(dir, "no-staging"))
			path := filepath.Join(dir, "miner-image-test.rootfs.ext4")
			raw := bytes.Repeat([]byte{0x31}, int(original))
			// Deliberately not AVB framing: consumers only copy ordinary files.
			body, tail := bytes.Repeat([]byte{0x42}, 5632), bytes.Repeat([]byte{0x53}, 512)
			for suffix, data := range map[string][]byte{"": raw, ".avb.body": body, ".avb.tail": tail} {
				if err := os.WriteFile(path+suffix, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			plan, err := buildFlashPlan(flashimage.C3(), flashOptions{mmcDev: 1, verity: true, partitions: []string{"rootfs_a", "rootfs_b"}})
			if err != nil {
				t.Fatal(err)
			}
			// Alternate geometry is a transport fixture, not a change to the C3 profile.
			for i := range plan.partitions {
				if plan.partitions[i].AVB {
					plan.partitions[i].capacity = capacity
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			items, cleanup, err := preparePayloads(ctx, plan, map[string]string{"rootfs_a": path, "rootfs_b": path, "avb_body": path + ".avb.body", "avb_tail": path + ".avb.tail"}, true)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			if len(items) != 2 {
				t.Fatalf("payloads: %d", len(items))
			}
			target, err := os.CreateTemp(dir, "slots-")
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			if err := target.Truncate(3 * capacity); err != nil {
				t.Fatal(err)
			}
			check := func(off int64, want []byte) {
				t.Helper()
				got := make([]byte, len(want))
				if _, err := target.ReadAt(got, off); err != nil || !bytes.Equal(got, want) {
					t.Fatalf("bytes at %d differ: %v", off, err)
				}
			}
			sentinel := []byte("keep omitted gap and slot")
			unselected := []int64{2 * capacity, 2*capacity + 123456, 3*capacity - 512}
			for _, off := range unselected {
				if _, err := target.WriteAt(sentinel, off); err != nil {
					t.Fatal(err)
				}
			}
			end := original + int64(len(body))
			for i, item := range items {
				base := int64(i) * capacity
				if item.size != capacity || len(item.extents) != 3 || item.extents[1].offset != original || item.extents[2].offset != capacity-512 {
					t.Fatalf("incorrect capacity-derived extents: %+v", item)
				}
				installed := bytes.Repeat([]byte{0xa5}, int(original))
				if _, err := target.WriteAt(installed, base); err != nil {
					t.Fatal(err)
				}
				for _, gap := range []int64{end, end + 12345, capacity - 1024} {
					if _, err := target.WriteAt(sentinel, base+gap); err != nil {
						t.Fatal(err)
					}
				}
				for _, x := range item.extents {
					if _, err := target.Seek(base+x.offset, io.SeekStart); err != nil {
						t.Fatal(err)
					}
					if _, err := io.CopyN(target, io.NewSectionReader(x.source, x.sourceOffset, x.length), x.length); err != nil {
						t.Fatal(err)
					}
				}
				check(base, raw)
				check(base+original, body)
				check(base+capacity-512, tail)
				for _, gap := range []int64{end, end + 12345, capacity - 1024} {
					check(base+gap, sentinel)
				}
			}
			for _, off := range unselected {
				check(off, sentinel)
			}
			cancel()
			for _, x := range items[0].extents {
				if _, err := x.source.ReadAt(make([]byte, 1), x.sourceOffset); !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation: %v", err)
				}
			}
			if err := cleanup(); err != nil {
				t.Fatal(err)
			}
			for suffix, want := range map[string][]byte{"": raw, ".avb.body": body, ".avb.tail": tail} {
				got, err := os.ReadFile(path + suffix)
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("source changed: %v", err)
				}
			}
		})
	}
}

func TestFileRangesPreserveUnselectedBytes(t *testing.T) {
	for _, skipVerify := range []bool{false, true} {
		t.Run(fmt.Sprint(skipVerify), func(t *testing.T) {
			plan := flashPlan{partitions: []partition{
				{Partition: flashimage.Partition{Phase: 0x0c, Name: "rootfs_a", DFUType: "FileSystem", ArtifactKey: "rootfs_a", AVB: true, Capacity: flashimage.CapacityBounded}, device: "mmc1", operation: "P", capacity: 16384},
				{Partition: flashimage.Partition{Phase: 0x0d, Name: "rootfs_b", DFUType: "FileSystem", Offset: 0x4000}, device: "mmc1", operation: "-"},
			}}
			root, body, tail := bytes.Repeat([]byte{0x21}, 512), bytes.Repeat([]byte{0x6b}, 1536), bytes.Repeat([]byte{0x37}, 512)
			clear(body[:512])
			item := payload{part: plan.partitions[0], size: 16384, extents: []extent{
				{offset: 0, length: 512, source: bytes.NewReader(root)},
				{offset: 512, length: 1024, source: bytes.NewReader(body), sourceOffset: 512},
				{offset: 15872, length: 512, source: bytes.NewReader(tail)},
			}}
			session, target := newStorageSession(t, map[byte]int{0x0c: 16384, 0x0d: 512})
			target.rejectReadback = skipVerify
			if err := (&stm32progClient{session: session, skipVerify: skipVerify}).program(context.Background(), plan, []payload{item}); err != nil {
				t.Fatal(err)
			}
			expected := bytes.Repeat([]byte{0xa5}, 16384)
			copy(expected, root)
			copy(expected[512:1536], body[512:])
			copy(expected[15872:], tail)
			if !bytes.Equal(target.storage[0x0c], expected) || !bytes.Equal(target.storage[0x0d], bytes.Repeat([]byte{0xa5}, 512)) {
				t.Fatal("programming changed omitted gap or unselected slot")
			}
			if target.live || !target.finalDetach || target.payloadWrites != 3 {
				t.Fatal("ranges did not finish one clean partition phase")
			}
		})
	}
}

func TestMetadataPreflightRejectsInvalidInputs(t *testing.T) {
	for _, name := range []string{"empty raw", "empty body", "empty tail", "short tail", "unknown capacity", "missing raw", "missing body", "missing tail", "wrong body basename", "wrong tail basename", "nonregular raw", "nonregular body", "nonregular tail", "missing later slot"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "miner-image-test.rootfs.ext4")
			paths := map[string]string{"rootfs_a": path, "rootfs_b": path, "avb_body": path + ".avb.body", "avb_tail": path + ".avb.tail"}
			for suffix, size := range map[string]int{"": 4096, ".avb.body": 1024, ".avb.tail": 512} {
				if err := os.WriteFile(path+suffix, make([]byte, size), 0600); err != nil {
					t.Fatal(err)
				}
			}
			resize := func(suffix string, size int64) {
				t.Helper()
				if err := os.Truncate(path+suffix, size); err != nil {
					t.Fatal(err)
				}
			}
			switch name {
			case "empty raw":
				resize("", 0)
			case "empty body":
				resize(".avb.body", 0)
			case "empty tail":
				resize(".avb.tail", 0)
			case "short tail":
				resize(".avb.tail", 511)
			case "missing raw":
				delete(paths, "rootfs_a")
			case "missing body":
				delete(paths, "avb_body")
			case "missing tail":
				delete(paths, "avb_tail")
			case "wrong body basename":
				paths["avb_body"] = path + ".other.avb.body"
			case "wrong tail basename":
				paths["avb_tail"] = path + ".other.avb.tail"
			case "missing later slot":
				delete(paths, "rootfs_b")
			case "nonregular raw", "nonregular body", "nonregular tail":
				suffix := ""
				if name == "nonregular body" {
					suffix = ".avb.body"
				}
				if name == "nonregular tail" {
					suffix = ".avb.tail"
				}
				if err := os.Remove(path + suffix); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path+suffix, 0700); err != nil {
					t.Fatal(err)
				}
			}
			plan, err := buildFlashPlan(flashimage.C3(), flashOptions{mmcDev: 1, verity: true, partitions: []string{"rootfs_a", "rootfs_b"}})
			if err != nil {
				t.Fatal(err)
			}
			if name == "unknown capacity" {
				for i := range plan.partitions {
					if plan.partitions[i].Name == "rootfs_b" {
						plan.partitions[i].capacity = 0
						plan.partitions[i].Capacity = flashimage.CapacityUnknown
					}
				}
			}
			if items, _, err := preparePayloads(context.Background(), plan, paths, true); err == nil || len(items) != 0 {
				t.Fatalf("invalid inputs prepared: %v %v", items, err)
			}
		})
	}
}

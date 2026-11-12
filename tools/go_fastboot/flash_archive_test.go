package gofastboot

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"proto-distro/tools/flashimage"
	"proto-distro/tools/go_fastboot/internal/ui"
)

func TestZIPPreservesSelectionAndExactCompanions(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "images.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	entries := []struct{ name, data string }{
		{"z/miner-image-test.rootfs.ext4", "first raw"},
		{"a/miner-image-test.rootfs.ext4", "second raw"},
		{"a/miner-image-test.rootfs.ext4.avb.body", "unrelated body"},
		{"z/miner-image-test.rootfs.ext4.avb.tail", "paired tail"},
		{"z/miner-image-old.rootfs.ext4.avb.body", "wrong version"},
	}
	for _, e := range entries {
		w, err := z.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(e.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	dir, images, err := extractZipImages(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	raw := images["rootfs"]
	if raw != filepath.Join(dir, entries[0].name) {
		t.Fatalf("selected %q", raw)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.name))
		if err != nil || string(data) != e.data {
			t.Fatalf("%s: %q %v", e.name, data, err)
		}
	}
	if _, err := os.Stat(raw + ".avb.body"); !os.IsNotExist(err) {
		t.Fatalf("unrelated body paired: %v", err)
	}
	data, err := os.ReadFile(raw + ".avb.tail")
	if err != nil || string(data) != "paired tail" {
		t.Fatalf("tail: %q %v", data, err)
	}
	profile := flashimage.C3()
	client := &geometryClient{}
	warnings := captureStatus(t)
	prepared, cleanup, err := prepareFlashImages(context.Background(), profile, images, sortPartitions(profile, images), client.GetVar, ui.NewStatusLine(), false)
	defer cleanup()
	if err != nil || len(prepared) != 0 || len(client.queries) != 0 || !strings.Contains(warnings(), filepath.Base(raw)+".avb.body") {
		t.Fatalf("unrelated companion consumed: %v %v %v %s", err, prepared, client.queries, warnings())
	}
}

func TestUnsafeZIPRemovesOwnedExtraction(t *testing.T) {
	root := t.TempDir()
	extraction := filepath.Join(root, "extraction")
	if err := os.Mkdir(extraction, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", extraction)
	t.Setenv("TMP", extraction)
	t.Setenv("TEMP", extraction)
	archive := filepath.Join(root, "unsafe.zip")
	writeTestZip(t, archive, []string{"nested/miner-image-test.rootfs.ext4", "../escape"})
	dir, images, err := extractZipImages(archive)
	if err == nil || dir != "" || images != nil {
		t.Fatalf("unsafe extraction: %q %v %v", dir, images, err)
	}
	entries, err := os.ReadDir(extraction)
	if err != nil || len(entries) != 0 {
		t.Fatalf("leftovers: %v %v", entries, err)
	}
	if _, err := os.Stat(filepath.Join(root, "escape")); !os.IsNotExist(err) {
		t.Fatalf("escaped file: %v", err)
	}
	if _, err := os.Stat(archive); err != nil {
		t.Fatal(err)
	}
}

func TestDirectAVBCompanionRejectedBeforeDeviceAccess(t *testing.T) {
	for _, suffix := range []string{".avb.body", ".avb.tail"} {
		t.Run(suffix, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "rootfs.ext4"+suffix)
			writeTestFile(t, path)
			err := Flash(context.Background(), FlashRequest{Target: "127.0.0.1", ImagePath: path, Partition: "rootfs_a"})
			if err == nil {
				t.Fatalf("metadata input: %v", err)
			}
		})
	}
}

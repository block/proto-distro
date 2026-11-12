package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"proto-distro/tools/flashimage"
	"strings"
	"testing"
)

func writeImageFixture(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(name), 0600); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestGetImageFilesRequiresOnlySelectedArtifacts(t *testing.T) {
	dir := t.TempDir()
	plain := writeImageFixture(t, dir, "miner-image-test.rootfs.ext4")
	writeImageFixture(t, dir, "miner-image-test.rootfs.ext4.avb")
	body := writeImageFixture(t, dir, "miner-image-test.rootfs.ext4.avb.body")
	tail := writeImageFixture(t, dir, "miner-image-test.rootfs.ext4.avb.tail")
	// Unselected artifacts may be absent or ambiguous, even with --verity.
	writeImageFixture(t, dir, "miner-one.bootfs.ext4")
	writeImageFixture(t, dir, "miner-two.bootfs.ext4")
	for _, useVerity := range []bool{false, true} {
		required := []string{"rootfs_a", "rootfs_b", "recovery"}
		if useVerity {
			required = append(required, "avb_body", "avb_tail")
		}
		files, err := getImageFiles(flashimage.C3(), dir, required, 1, true)
		if err != nil {
			t.Fatal(err)
		}
		want := plain
		if useVerity {
			if files["avb_body"] != body || files["avb_tail"] != tail {
				t.Fatal("missing paired metadata")
			}
		}
		if len(files) != len(required) || files["rootfs_a"] != want || files["rootfs_b"] != want || files["recovery"] != want {
			t.Fatalf("selected rootfs artifacts = %v, want all three slots using %q", files, want)
		}
	}
	files, err := getImageFiles(flashimage.C3(), t.TempDir(), nil, 1, false)
	if err != nil || len(files) != 0 {
		t.Fatalf("erase-only discovery = %v, %v", files, err)
	}
}

func TestGetImageFilesReportsAllMissingAndAmbiguousKeys(t *testing.T) {
	dir := t.TempDir()
	first := writeImageFixture(t, dir, "miner-image-first.rootfs.ext4")
	second := writeImageFixture(t, dir, "miner-image-second.rootfs.ext4")
	bootFirst := writeImageFixture(t, dir, "miner-first.bootfs.ext4")
	bootSecond := writeImageFixture(t, dir, "miner-second.bootfs.ext4")
	_, err := getImageFiles(flashimage.C3(), dir, []string{"rootfs_b", "tfa_usb", "bootfs", "datafs"}, 1, true)
	if err == nil {
		t.Fatal("ambiguous and missing required artifacts were accepted")
	}
	for _, detail := range []string{"rootfs_b", "tfa_usb", "bootfs", "datafs", first, second, bootFirst, bootSecond} {
		if !strings.Contains(err.Error(), detail) {
			t.Errorf("discovery error %q omits %q", err, detail)
		}
	}
}

func TestGetImageFilesSymlinkRootAndExcludedArtifacts(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, ".bundle")
	rootfs := writeImageFixture(t, dir, "miner-image-test.rootfs.ext4")
	tfa := writeImageFixture(t, dir, "arm-trusted-firmware/board-usb_Signed.stm32")
	metadata := writeImageFixture(t, dir, "arm-trusted-firmware/metadata.bin")
	writeImageFixture(t, dir, "arm-trusted-firmware/debug/board-usb_Signed.stm32")
	writeImageFixture(t, dir, "arm-trusted-firmware/debug/metadata.bin")
	writeImageFixture(t, dir, ".hidden/miner-image-extra.rootfs.ext4")
	writeImageFixture(t, dir, ".miner-image-hidden.rootfs.ext4")
	link := filepath.Join(parent, "image-link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	files, err := getImageFiles(flashimage.C3(), link, []string{"rootfs_a", "tfa_usb", "tfa_metadata"}, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if files["rootfs_a"] != rootfs || files["tfa_usb"] != tfa || files["tfa_metadata"] != metadata {
		t.Fatalf("resolved artifacts = %v", files)
	}
}

func TestGetImageFilesMMCAndSigningSelection(t *testing.T) {
	dir := t.TempDir()
	sd := writeImageFixture(t, dir, "arm-trusted-firmware/board-sdcard.stm32")
	emmc := writeImageFixture(t, dir, "arm-trusted-firmware/board-emmc.stm32")
	signedEMMC := writeImageFixture(t, dir, "arm-trusted-firmware/board-emmc_Signed.stm32")
	usb := writeImageFixture(t, dir, "arm-trusted-firmware/board-usb.stm32")
	signedUSB := writeImageFixture(t, dir, "arm-trusted-firmware/board-usb_Signed.stm32")
	for _, tc := range []struct {
		mmc      int
		signed   bool
		tfa, usb string
	}{
		{0, false, sd, usb},
		{0, true, sd, signedUSB},
		{1, false, emmc, usb},
		{1, true, signedEMMC, signedUSB},
	} {
		files, err := getImageFiles(flashimage.C3(), dir, []string{"tfa", "tfa_usb"}, tc.mmc, tc.signed)
		if err != nil {
			t.Fatal(err)
		}
		if files["tfa"] != tc.tfa || files["tfa_usb"] != tc.usb {
			t.Errorf("mmc=%d signed=%v selected %v", tc.mmc, tc.signed, files)
		}
	}
}

func TestGetImageFilesRejectsInvalidInputs(t *testing.T) {
	dir := t.TempDir()
	file := writeImageFixture(t, dir, "not-a-directory")
	for _, tc := range []struct {
		name, path string
		required   []string
		mmc        int
	}{
		{"unknown key", dir, []string{"not-an-artifact"}, 1},
		{"invalid MMC", dir, nil, 2},
		{"negative MMC", dir, nil, -1},
		{"empty path", "", nil, 1},
		{"file input", file, nil, 1},
		{"missing directory", filepath.Join(dir, "absent"), nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := getImageFiles(flashimage.C3(), tc.path, tc.required, tc.mmc, false); err == nil {
				t.Fatal("invalid discovery input accepted")
			}
		})
	}
}

type imageZIPEntry struct {
	name string
	mode os.FileMode
	data string
}

func writeImageZIP(t *testing.T, path string, entries []imageZIPEntry) {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Store}
		header.SetMode(entry.mode)
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(entry.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareImageOwnsOnlyTemporaryExtraction(t *testing.T) {
	parent := t.TempDir()
	adjacent := filepath.Join(parent, "image")
	marker := writeImageFixture(t, adjacent, "keep-me")
	archive := filepath.Join(parent, "image.zip")
	writeImageZIP(t, archive, []imageZIPEntry{
		{"nested/", os.ModeDir | 0755, ""},
		{"nested/miner-image-test.rootfs.ext4", 0644, "rootfs payload"},
	})
	dir, cleanup, err := prepareImage(archive)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	if dir == adjacent || filepath.Dir(dir) == parent {
		t.Fatalf("extracted beside source: %q", dir)
	}
	got, err := os.ReadFile(filepath.Join(dir, "nested/miner-image-test.rootfs.ext4"))
	if err != nil || string(got) != "rootfs payload" {
		t.Fatalf("extracted content = %q, %v", got, err)
	}
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("owned temporary directory remains after cleanup: %v", err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "keep-me" {
		t.Fatalf("adjacent directory changed: %q, %v", got, err)
	}
	inputDir, dirCleanup, err := prepareImage(adjacent)
	if err != nil || inputDir != adjacent {
		t.Fatalf("directory preparation = %q, %v", inputDir, err)
	}
	dirCleanup()
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("directory input removed: %v", err)
	}
}

func TestPrepareImageRejectsUnsafeZIPAndCleansFailures(t *testing.T) {
	parent := t.TempDir()
	tempRoot := filepath.Join(parent, "temporary")
	if err := os.Mkdir(tempRoot, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tempRoot)
	marker := writeImageFixture(t, filepath.Join(parent, "image"), "keep-me")
	archive := filepath.Join(parent, "image.zip")
	writeImageZIP(t, archive, []imageZIPEntry{
		{"already-extracted", 0600, "remove me"},
		{"../escaped", 0600, "bad"},
	})
	source, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	dir, cleanup, err := prepareImage(archive)
	defer cleanup()
	if err == nil {
		t.Fatal("unsafe ZIP accepted")
	}
	if dir != "" {
		t.Errorf("failed extraction returned directory %q", dir)
	}
	// Inspect before calling cleanup: failure must clean itself up.
	remaining, readErr := os.ReadDir(tempRoot)
	if readErr != nil || len(remaining) != 0 {
		t.Errorf("failed extraction leaked files: %v, %v", remaining, readErr)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "keep-me" {
		t.Fatalf("adjacent directory changed on failure: %q, %v", got, err)
	}
	if got, err := os.ReadFile(archive); err != nil || !bytes.Equal(got, source) {
		t.Fatalf("source archive changed on failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, "escaped")); !os.IsNotExist(err) {
		t.Fatalf("ZIP escaped extraction directory: %v", err)
	}
}

func TestPrepareImagePropagatesZIPReadFailure(t *testing.T) {
	parent := t.TempDir()
	archive := filepath.Join(t.TempDir(), "corrupt.zip")
	t.Setenv("TMPDIR", parent)
	payload := "unique stored payload to corrupt"
	writeImageZIP(t, archive, []imageZIPEntry{{"payload", 0600, payload}})
	encoded, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	offset := bytes.Index(encoded, []byte(payload))
	if offset < 0 {
		t.Fatal("stored payload not found")
	}
	encoded[offset] ^= 1
	if err := os.WriteFile(archive, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	_, cleanup, err := prepareImage(archive)
	defer cleanup()
	if err == nil {
		t.Fatal("ZIP checksum failure ignored")
	}
	remaining, readErr := os.ReadDir(parent)
	if readErr != nil || len(remaining) != 0 {
		t.Fatalf("checksum failure leaked extraction: %v, %v", remaining, readErr)
	}
}

func TestPairedDiscoveryDirectoryAndZIP(t *testing.T) {
	const root = "release/miner-image-test.rootfs.ext4"
	for _, tc := range []struct {
		name  string
		names []string
		valid bool
	}{
		{"files", []string{root, root + ".avb.body", root + ".avb.tail"}, true},
		{"missing body", []string{root, root + ".avb.tail"}, false},
		{"missing tail", []string{root, root + ".avb.body"}, false},
		{"missing raw", []string{root + ".avb.body", root + ".avb.tail"}, false},
		{"full only", []string{root + ".avb"}, false},
		{"compact only", []string{root, root + ".avb.footer"}, false},
		{"wrong body basename", []string{root, "release/miner-image-other.rootfs.ext4.avb.body", root + ".avb.tail"}, false},
		{"wrong tail basename", []string{root, root + ".avb.body", "release/miner-image-other.rootfs.ext4.avb.tail"}, false},
		{"ambiguous raw", []string{root, root + ".avb.body", root + ".avb.tail", "release/miner-image-other.rootfs.ext4"}, false},
		{"ambiguous body", []string{root, root + ".avb.body", root + ".avb.tail", "release/miner-image-other.rootfs.ext4.avb.body"}, false},
		{"ambiguous tail", []string{root, root + ".avb.body", root + ".avb.tail", "release/miner-image-other.rootfs.ext4.avb.tail"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			archive := filepath.Join(t.TempDir(), "release.zip")
			var entries []imageZIPEntry
			for _, name := range tc.names {
				writeImageFixture(t, dir, name)
				entries = append(entries, imageZIPEntry{name: name, data: name})
			}
			writeImageZIP(t, archive, entries)
			plan, err := buildFlashPlan(flashimage.C3(), flashOptions{verity: true, partitions: []string{"rootfs_a", "rootfs_b", "recovery"}})
			if err != nil {
				t.Fatal(err)
			}
			for _, input := range []string{dir, archive} {
				prepared, cleanup, err := prepareImage(input)
				if err != nil {
					t.Fatal(err)
				}
				files, err := getImageFiles(flashimage.C3(), prepared, requiredArtifacts(plan, stageUBoot, true), 1, true)
				cleanup()
				if (err == nil) != tc.valid {
					t.Fatalf("%s discovery: %v", input, err)
				}
				if tc.valid && (files["rootfs_a"] != files["rootfs_b"] || files["rootfs_a"] != files["recovery"] || files["avb_body"] != files["rootfs_a"]+".avb.body" || files["avb_tail"] != files["rootfs_a"]+".avb.tail") {
					t.Fatalf("wrong files: %v", files)
				}
			}
		})
	}
}

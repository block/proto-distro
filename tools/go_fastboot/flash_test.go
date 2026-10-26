package gofastboot

import (
	"archive/zip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"proto-distro/tools/flashimage"
	"proto-distro/tools/go_fastboot/internal/ui"
)

func TestDetectPartition(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		// Direct partition name matches
		{"miner-image.rootfs.ext4", "rootfs"},
		{"miner-image.bootfs.ext4", "bootfs"},
		{"miner-image.datafs.ext4", "datafs"},
		{"firmware-boot.ext4", "boot"},
		{"recovery.ext4", "recovery"},

		// Hyphenated names
		{"miner-rootfs-v1.0.ext4", "rootfs"},
		{"image-bootfs-latest.ext4", "bootfs"},
		{"system-datafs.ext4", "datafs"},

		// Dot-separated names
		{"firmware.rootfs.img", "rootfs"},
		{"backup.bootfs.tar", "bootfs"},

		// Keyword matching
		{"kernel-image.bin", "boot"},
		{"zImage", "boot"},
		{"board.dtb", "dtb"},
		{"DeviceTree.bin", "dtb"},
		{"KERNEL.DTB", ""},
		{"zImage-devicetree.bin", ""},
		{"boot-kernel.dtb", "boot"},

		// TFA (Trusted Firmware-A) matching - only eMMC variant
		{"tf-a-stm32mp131f-c3p1-emmc_Signed.stm32", "tfa"},
		{"tf-a-stm32mp131f-c3p1-emmc.stm32", "tfa"},
		{"TF-A-xxx-emmc.stm32", "tfa"}, // Case insensitive

		// TFA USB variant should NOT match (would brick eMMC device)
		{"tf-a-stm32mp131f-c3p1-usb_Signed.stm32", ""},
		{"tf-a-stm32mp131f-c3p1-usb.stm32", ""},

		// Generic TFA without variant should NOT match (ambiguous)
		{"tfa.bin", ""},
		{"TF-A.stm32", ""},

		// FIP (Firmware Image Package) matching
		{"fip-stm32mp131f-c3p1-optee_ProdSigned.bin", "fip"},
		{"fip.bin", "fip"},
		{"FIP-optee.bin", "fip"}, // Case insensitive

		// Edge cases that should NOT match (partial matches)
		{"not-a-rootfsystem.ext4", ""}, // "rootfs" is substring but not word
		{"myboot.img", ""},             // "boot" is suffix but no separator

		// No match
		{"random-file.txt", ""},
		{"image.ext4", ""},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			result := detectPartition(tt.filename)
			if result != tt.expected {
				t.Errorf("detectPartition(%q) = %q, want %q", tt.filename, result, tt.expected)
			}
		})
	}
}

func TestIsValidImageFile(t *testing.T) {
	tests := []struct {
		filename string
		expected bool
	}{
		// Valid image extensions
		{"miner-image.rootfs.ext4", true},
		{"firmware.img", true},
		{"u-boot.bin", true},
		{"disk.raw", true},
		{"system.sparse", true},
		{"tf-a-stm32mp131f-c3p1-emmc_Signed.stm32", true}, // STM32 TFA firmware

		// Case insensitive
		{"FIRMWARE.EXT4", true},
		{"Image.IMG", true},
		{"uboot.BIN", true},

		// Invalid extensions - should NOT be flashed
		{"miner-image.rootfs.manifest", false},
		{"miner-image-mfg-c3-p1-1.1.20.swu", false}, // SWUpdate package
		{"flashlayout_emmc.tsv", false},             // Flash layout file
		{"application_manifest.txt", false},         // Manifest file
		{"sha256.txt", false},                       // Checksum file
		{"readme.txt", false},
		{"notes.md", false},
		{"config.json", false},
		{"script.sh", false},
		{"archive.tar", false},
		{"archive.tar.gz", false},
		{"archive.zip", false},

		// Edge cases
		{"", false},
		{"noextension", false},
		{".ext4", true}, // Just extension is technically valid
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			result := isValidImageFile(tt.filename)
			if result != tt.expected {
				t.Errorf("isValidImageFile(%q) = %v, want %v", tt.filename, result, tt.expected)
			}
		})
	}
}

func TestScanDirImagesPrefersSignedTFA(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "tf-a-stm32mp131f-c3p1-emmc.stm32"))
	writeTestFile(t, filepath.Join(dir, "tf-a-stm32mp131f-c3p1-emmc_Signed.stm32"))

	images, err := scanDirImages(dir)
	if err != nil {
		t.Fatalf("scanDirImages() error = %v", err)
	}
	got := filepath.Base(images["tfa"])
	want := "tf-a-stm32mp131f-c3p1-emmc_Signed.stm32"
	if got != want {
		t.Fatalf("tfa image = %q, want %q", got, want)
	}
}

func TestExtractZipImagesPrefersSignedTFA(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "miner-image.zip")
	writeTestZip(t, zipPath, []string{
		"arm-trusted-firmware/tf-a-stm32mp131f-c3p1-emmc.stm32",
		"arm-trusted-firmware/tf-a-stm32mp131f-c3p1-emmc_Signed.stm32",
	})

	tempDir, images, err := extractZipImages(zipPath)
	if tempDir != "" {
		defer os.RemoveAll(tempDir)
	}
	if err != nil {
		t.Fatalf("extractZipImages() error = %v", err)
	}
	got := filepath.Base(images["tfa"])
	want := "tf-a-stm32mp131f-c3p1-emmc_Signed.stm32"
	if got != want {
		t.Fatalf("tfa image = %q, want %q", got, want)
	}
}

func TestShouldUseImageForPartitionKeepsFirstNonTFA(t *testing.T) {
	if shouldUseImageForPartition("fip", "new_Signed.bin", "old.bin") {
		t.Fatal("non-tfa partition replaced existing image")
	}
}

func TestSortPartitions(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]string
		expected []string
	}{
		{
			name: "all known partitions",
			input: map[string]string{
				"rootfs":   "/path/rootfs.ext4",
				"bootfs":   "/path/bootfs.ext4",
				"tfa":      "/path/tfa.bin",
				"fip":      "/path/fip.bin",
				"datafs":   "/path/datafs.ext4",
				"boot":     "/path/boot.img",
				"recovery": "/path/recovery.img",
			},
			expected: []string{"datafs", "rootfs", "bootfs", "boot", "recovery", "fip", "tfa"},
		},
		{
			name: "subset of partitions",
			input: map[string]string{
				"rootfs": "/path/rootfs.ext4",
				"bootfs": "/path/bootfs.ext4",
			},
			expected: []string{"rootfs", "bootfs"},
		},
		{
			name: "with unknown partition",
			input: map[string]string{
				"rootfs":  "/path/rootfs.ext4",
				"unknown": "/path/unknown.bin",
			},
			expected: []string{"rootfs", "unknown"},
		},
		{
			name: "multiple unknown partitions sorted alphabetically",
			input: map[string]string{
				"rootfs": "/path/rootfs.ext4",
				"zpart":  "/path/zpart.bin",
				"apart":  "/path/apart.bin",
			},
			expected: []string{"rootfs", "apart", "zpart"},
		},
		{
			name:     "single partition",
			input:    map[string]string{"rootfs": "/path/rootfs.ext4"},
			expected: []string{"rootfs"},
		},
		{
			name:     "empty map",
			input:    map[string]string{},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sortPartitions(flashimage.C3(), tt.input)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("sortPartitions() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func writeTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
}

func writeTestZip(t *testing.T, path string, names []string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	zw := zip.NewWriter(file)
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(strings.Repeat("x", 8))); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProfileSelectionUsesExactImageKeys(t *testing.T) {
	profile := flashimage.C3()
	images := map[string]string{
		"rootfs": "selected.ext4", "rootfs_a": "direct.ext4",
		"datafs": "data.ext4", "bootfs": "bootfs.ext4",
		"fip": "fip.bin", "tfa": "signed.stm32",
		"boot": "kernel.img", "recovery": "recovery.img", "userfs": "user.ext4", "dtb": "tree.bin",
	}
	status := ui.NewStatusLine()
	defaults := filterPartitions(profile, images, "", status)
	want := map[string]string{"rootfs": "selected.ext4", "datafs": "data.ext4", "bootfs": "bootfs.ext4", "fip": "fip.bin", "tfa": "signed.stm32"}
	if !reflect.DeepEqual(defaults, want) {
		t.Fatalf("defaults = %v", defaults)
	}
	selected := filterPartitions(profile, images, "rootfs_a", status)
	if !reflect.DeepEqual(selected, map[string]string{"rootfs_a": "direct.ext4"}) {
		t.Fatalf("direct selection = %v", selected)
	}
	order := sortPartitions(profile, map[string]string{"rootfs_a": "direct", "boot": "kernel", "rootfs": "alias", "userfs": "user", "dtb": "tree"})
	if !reflect.DeepEqual(order, []string{"rootfs", "boot", "dtb", "rootfs_a", "userfs"}) {
		t.Fatalf("order = %v", order)
	}
}

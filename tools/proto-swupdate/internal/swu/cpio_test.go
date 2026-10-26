package swu

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCPIO builds a minimal newc/crc CPIO archive with the given entries
// (name, data) and trailing TRAILER!!! sentinel. magic must be "070701" or
// "070702".
func writeCPIO(t *testing.T, magic string, entries []struct{ name, data string }) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.cpio")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	writeEntry := func(name, data string) {
		nameBytes := append([]byte(name), 0)
		hdr := make([]byte, cpioHeaderSize)
		copy(hdr[0:6], []byte(magic))
		// zero-pad all fields, then fill filesize and namesize
		for i := 6; i < cpioHeaderSize; i++ {
			hdr[i] = '0'
		}
		writeHex := func(off int, v uint32) {
			s := fmt.Sprintf("%08x", v)
			copy(hdr[off:off+8], []byte(s))
		}
		writeHex(54, uint32(len(data))) // filesize
		writeHex(94, uint32(len(nameBytes)))
		f.Write(hdr)
		f.Write(nameBytes)
		// pad after header+name to 4-byte boundary
		if pad := (4 - (cpioHeaderSize+len(nameBytes))%4) % 4; pad > 0 {
			f.Write(make([]byte, pad))
		}
		f.Write([]byte(data))
		if pad := (4 - len(data)%4) % 4; pad > 0 {
			f.Write(make([]byte, pad))
		}
	}

	for _, e := range entries {
		writeEntry(e.name, e.data)
	}
	writeEntry("TRAILER!!!", "")
	return path
}

func TestFindFile_NewcAndCRCMagics(t *testing.T) {
	for _, magic := range []string{cpioNewcMagic, cpioCRCMagic} {
		t.Run(magic, func(t *testing.T) {
			path := writeCPIO(t, magic, []struct{ name, data string }{
				{"sw-description", "fake"},
				{"miner-image-release-c3-p1-1.2.6.rootfs.ext4.gz", "rootfs-bytes"},
				{"fip-stm32mp.bin", "fip-bytes"},
			})
			entry, err := FindFile(path, "*.rootfs.ext4.gz")
			if err != nil {
				t.Fatalf("rootfs: %v", err)
			}
			if entry.Size != int64(len("rootfs-bytes")) {
				t.Fatalf("rootfs size: got %d", entry.Size)
			}
			data, _ := os.ReadFile(path)
			if string(data[entry.DataOffset:entry.DataOffset+entry.Size]) != "rootfs-bytes" {
				t.Fatalf("rootfs offset wrong")
			}
			if _, err := FindFile(path, "fip-*.bin"); err != nil {
				t.Fatalf("fip: %v", err)
			}
		})
	}
}

func TestFindFile_NotFoundSentinel(t *testing.T) {
	path := writeCPIO(t, cpioNewcMagic, []struct{ name, data string }{
		{"sw-description", "x"},
	})
	_, err := FindFile(path, "*.rootfs.ext4.gz")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestFindFile_BadMagic(t *testing.T) {
	path := writeCPIO(t, "070703", []struct{ name, data string }{
		{"x", ""},
	})
	_, err := FindFile(path, "x")
	if err == nil {
		t.Fatal("expected magic error")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("malformed archive should not be ErrNotFound, got %v", err)
	}
	if !strings.Contains(err.Error(), "magic") {
		t.Fatalf("expected magic complaint, got %v", err)
	}
}

func TestFindFile_PaddingAlignment(t *testing.T) {
	// Names of varying lengths exercise the (4 - (110+namesize)%4)%4 pad.
	for _, name := range []string{"a", "ab", "abc", "abcd", "abcde"} {
		path := writeCPIO(t, cpioNewcMagic, []struct{ name, data string }{
			{name, "data-" + name},
		})
		entry, err := FindFile(path, name)
		if err != nil {
			t.Fatalf("name=%q: %v", name, err)
		}
		raw, _ := os.ReadFile(path)
		got := string(raw[entry.DataOffset : entry.DataOffset+entry.Size])
		want := "data-" + name
		if got != want {
			t.Fatalf("name=%q: data %q != %q", name, got, want)
		}
	}
}

func TestFindFile_TruncatedHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trunc.cpio")
	if err := os.WriteFile(path, []byte("070701short"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := FindFile(path, "x")
	if err == nil {
		t.Fatal("expected error on truncated header")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("truncated archive should not be ErrNotFound, got %v", err)
	}
}

func TestFindFile_EmptyEntry(t *testing.T) {
	path := writeCPIO(t, cpioNewcMagic, []struct{ name, data string }{
		{"empty.bin", ""},
		{"after.bin", "x"},
	})
	entry, err := FindFile(path, "empty.bin")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Size != 0 {
		t.Fatalf("size: got %d want 0", entry.Size)
	}
	if _, err := FindFile(path, "after.bin"); err != nil {
		t.Fatalf("entry after empty body: %v", err)
	}
}

func TestFindFile_FirstMatchWins(t *testing.T) {
	path := writeCPIO(t, cpioNewcMagic, []struct{ name, data string }{
		{"a.rootfs.ext4.gz", "first"},
		{"b.rootfs.ext4.gz", "second"},
	})
	entry, err := FindFile(path, "*.rootfs.ext4.gz")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Name != "a.rootfs.ext4.gz" {
		t.Fatalf("first-match: got %q", entry.Name)
	}
}

func TestFindFile_QuestionMarkGlob(t *testing.T) {
	path := writeCPIO(t, cpioNewcMagic, []struct{ name, data string }{
		{"fip-a.bin", "x"},
	})
	if _, err := FindFile(path, "fip-?.bin"); err != nil {
		t.Fatalf("? glob: %v", err)
	}
}

func TestFindFile_BadPattern(t *testing.T) {
	path := writeCPIO(t, cpioNewcMagic, []struct{ name, data string }{
		{"a", ""},
	})
	_, err := FindFile(path, "[invalid")
	if err == nil {
		t.Fatal("expected pattern error")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("bad pattern should not be ErrNotFound, got %v", err)
	}
}

func TestHas(t *testing.T) {
	path := writeCPIO(t, cpioNewcMagic, []struct{ name, data string }{
		{"present.bin", "x"},
	})
	got, err := Has(path, "present.bin")
	if err != nil || !got {
		t.Fatalf("present: got=%v err=%v", got, err)
	}
	got, err = Has(path, "missing.bin")
	if err != nil || got {
		t.Fatalf("missing: got=%v err=%v want (false,nil)", got, err)
	}
	// Malformed archive should propagate as error, not silently report false.
	bad := filepath.Join(t.TempDir(), "bad.cpio")
	if err := os.WriteFile(bad, []byte("notcpio"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Has(bad, "anything")
	if err == nil {
		t.Fatal("expected error on malformed archive")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("malformed should not be ErrNotFound, got %v", err)
	}
}

func TestFindFile_OversizedNamesize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "huge.cpio")
	hdr := make([]byte, cpioHeaderSize)
	copy(hdr[0:6], []byte(cpioNewcMagic))
	for i := 6; i < cpioHeaderSize; i++ {
		hdr[i] = '0'
	}
	copy(hdr[94:102], []byte(fmt.Sprintf("%08x", uint32(maxNameSize+1))))
	if err := os.WriteFile(path, hdr, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := FindFile(path, "x")
	if err == nil || !strings.Contains(err.Error(), "namesize") {
		t.Fatalf("expected namesize error, got %v", err)
	}
}

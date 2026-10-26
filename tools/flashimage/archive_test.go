package flashimage

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type zipEntry struct {
	name string
	mode os.FileMode
	data string
}

func writeZIP(t *testing.T, path string, entries []zipEntry) {
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

func TestExtractZIPRejectsUnsafeEntries(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []zipEntry
	}{
		{"parent traversal", []zipEntry{{"../escaped", 0600, "bad"}}},
		{"nested traversal", []zipEntry{{"nested/../../escaped", 0600, "bad"}}},
		{"internal traversal", []zipEntry{{"nested/../escaped", 0600, "bad"}}},
		{"absolute", []zipEntry{{"/escaped", 0600, "bad"}}},
		{"backslash traversal", []zipEntry{{`..\escaped`, 0600, "bad"}}},
		{"backslash path", []zipEntry{{`nested\escaped`, 0600, "bad"}}},
		{"Windows drive", []zipEntry{{"C:/escaped", 0600, "bad"}}},
		{"colon", []zipEntry{{"file:stream", 0600, "bad"}}},
		{"NUL", []zipEntry{{"nul\x00name", 0600, "bad"}}},
		{"empty", []zipEntry{{"", 0600, "bad"}}},
		{"dot", []zipEntry{{"./", os.ModeDir | 0700, ""}}},
		{"duplicate file", []zipEntry{{"same", 0600, "first"}, {"same", 0600, "second"}}},
		{"normalized duplicate", []zipEntry{{"same", 0600, "first"}, {"./same", 0600, "second"}}},
		{"nested normalized duplicate", []zipEntry{{"nested/same", 0600, "first"}, {"nested//./same", 0600, "second"}}},
		{"duplicate directory", []zipEntry{{"dir/", os.ModeDir | 0700, ""}, {"./dir/", os.ModeDir | 0700, ""}}},
		{"case file alias", []zipEntry{{"same", 0600, "first"}, {"SAME", 0600, "second"}}},
		{"case parent alias", []zipEntry{{"dir/one", 0600, "first"}, {"DIR/two", 0600, "second"}}},
		{"case directory alias", []zipEntry{{"dir/", os.ModeDir | 0700, ""}, {"DIR/", os.ModeDir | 0700, ""}}},
		{"Unicode case alias", []zipEntry{{"σ/one", 0600, "first"}, {"ς/two", 0600, "second"}}},
		{"symlink", []zipEntry{{"link", os.ModeSymlink | 0777, "../escaped"}}},
		{"special file", []zipEntry{{"fifo", os.ModeNamedPipe | 0600, ""}}},
		{"socket", []zipEntry{{"socket", os.ModeSocket | 0600, ""}}},
		{"device", []zipEntry{{"device", os.ModeDevice | 0600, ""}}},
		{"file directory conflict", []zipEntry{{"node", 0600, "file"}, {"node/child", 0600, "bad"}}},
		{"directory file conflict", []zipEntry{{"node/", os.ModeDir | 0700, ""}, {"node", 0600, "bad"}}},
		{"implicit directory file conflict", []zipEntry{{"node/child", 0600, "first"}, {"node", 0600, "bad"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			destination := filepath.Join(parent, "output")
			if err := os.Mkdir(destination, 0700); err != nil {
				t.Fatal(err)
			}
			archive := filepath.Join(parent, "image.zip")
			entries := append([]zipEntry{{"already-extracted", 0600, "keep me"}}, tc.entries...)
			writeZIP(t, archive, entries)
			var notified []string
			err := ExtractZIP(archive, destination, func(path string) {
				notified = append(notified, path)
				if _, err := os.ReadFile(path); err != nil {
					t.Fatal(err)
				}
			})
			if err == nil {
				t.Fatal("unsafe ZIP accepted")
			}
			if len(notified) == 0 || notified[0] != filepath.Join(destination, "already-extracted") {
				t.Fatalf("valid first member not notified: %v", notified)
			}
			if got, err := os.ReadFile(filepath.Join(destination, "already-extracted")); err != nil || string(got) != "keep me" {
				t.Fatalf("extractor removed caller-owned output: %q, %v", got, err)
			}
			if _, err := os.Stat(filepath.Join(parent, "escaped")); !os.IsNotExist(err) {
				t.Fatalf("ZIP escaped destination: %v", err)
			}
			// The first file in a conflicting pair must never be overwritten.
			if first := tc.entries[0]; len(tc.entries) > 1 && first.mode.IsRegular() {
				if got, err := os.ReadFile(filepath.Join(destination, first.name)); err != nil || string(got) != first.data {
					t.Fatalf("first member changed: %q, %v", got, err)
				}
			}
		})
	}
}

func TestExtractZIPNestedBytesAndNotifications(t *testing.T) {
	entries := []zipEntry{
		{"nested/z-image", 0777, "last alphabetically, first in archive"},
		{"nested/", os.ModeDir | 0777, ""},
		{"nested/a-image", 0777, "second in archive"},
		{"nested/a-image.avb.body", 0777, "body bytes\x00"},
		{"other/a-image.avb.tail", 0777, "tail stays separate"},
	}
	archive := filepath.Join(t.TempDir(), "image.zip")
	writeZIP(t, archive, entries)
	for _, notify := range []bool{false, true} {
		destination := t.TempDir()
		var got []string
		var callback func(string)
		if notify {
			callback = func(path string) {
				if !filepath.IsAbs(path) {
					t.Fatalf("notification is not absolute: %q", path)
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, string(data))
			}
		}
		// Supply a relative destination and require absolute notification paths.
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		relative, err := filepath.Rel(cwd, destination)
		if err != nil {
			t.Fatal(err)
		}
		if err := ExtractZIP(archive, relative, callback); err != nil {
			t.Fatal(err)
		}
		var want []string
		for _, entry := range entries {
			path := filepath.Join(destination, entry.name)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if entry.mode.IsDir() {
				if info.Mode().Perm() != 0700 {
					t.Fatalf("directory permissions = %o", info.Mode().Perm())
				}
				continue
			}
			if info.Mode().Perm() != 0600 {
				t.Fatalf("file permissions = %o", info.Mode().Perm())
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != entry.data {
				t.Fatalf("nested member = %q, %v; want %q", data, err, entry.data)
			}
			want = append(want, entry.data)
		}
		if notify && !reflect.DeepEqual(got, want) {
			t.Fatalf("notifications = %q; want %q", got, want)
		}
	}
}

func TestExtractZIPRejectsInvalidArguments(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "empty.zip")
	writeZIP(t, archive, nil)
	for _, tc := range []struct{ archive, destination string }{
		{"", dir}, {archive, ""}, {archive, archive},
		{archive, filepath.Join(dir, "missing")}, {filepath.Join(dir, "missing.zip"), dir},
	} {
		if err := ExtractZIP(tc.archive, tc.destination, nil); err == nil {
			t.Fatalf("invalid arguments accepted: %+v", tc)
		}
	}
}

func TestExtractZIPPreservesExistingFiles(t *testing.T) {
	for _, name := range []string{"existing", "existing/child"} {
		destination := t.TempDir()
		path := filepath.Join(destination, "existing")
		if err := os.WriteFile(path, []byte("untouched"), 0600); err != nil {
			t.Fatal(err)
		}
		archive := filepath.Join(t.TempDir(), "image.zip")
		writeZIP(t, archive, []zipEntry{{name, 0600, "replacement"}})
		if err := ExtractZIP(archive, destination, nil); err == nil {
			t.Fatal("existing destination accepted")
		}
		if got, err := os.ReadFile(path); err != nil || string(got) != "untouched" {
			t.Fatalf("existing file changed: %q, %v", got, err)
		}
	}
}

func TestExtractZIPPropagatesChecksumFailure(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "corrupt.zip")
	const payload = "unique stored payload to corrupt"
	writeZIP(t, archive, []zipEntry{{"good", 0600, "good bytes"}, {"bad", 0600, payload}})
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
	destination := t.TempDir()
	var notified []string
	err = ExtractZIP(archive, destination, func(path string) { notified = append(notified, path) })
	if !errors.Is(err, zip.ErrChecksum) {
		t.Fatalf("checksum error not propagated: %v", err)
	}
	if !reflect.DeepEqual(notified, []string{filepath.Join(destination, "good")}) {
		t.Fatalf("failed member notified: %v", notified)
	}
	if got, err := os.ReadFile(filepath.Join(destination, "good")); err != nil || string(got) != "good bytes" {
		t.Fatalf("partial output lost: %q, %v", got, err)
	}
}

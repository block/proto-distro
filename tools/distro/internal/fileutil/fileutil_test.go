package fileutil

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateZipCompressesAndPreservesFiles(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	if err := os.MkdirAll(filepath.Join(source, "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("rootfs payload\x00"), 4096)
	if err := os.WriteFile(filepath.Join(source, "nested", "image.ext4"), payload, 0755); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "image.zip")
	if err := CreateZip(source, output); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(output)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	for _, member := range archive.File {
		if member.Name != "nested/image.ext4" {
			continue
		}
		if member.CompressedSize64 >= uint64(len(payload)/10) {
			t.Fatalf("payload not compressed: %d bytes from %d", member.CompressedSize64, len(payload))
		}
		if member.Mode().Perm() != 0755 {
			t.Fatalf("permissions changed: %v", member.Mode())
		}
		reader, err := member.Open()
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatal("payload changed")
		}
		return
	}
	t.Fatal("nested/image.ext4 missing from archive")
}

func TestUpdateApplicationManifestCreatesAndUpdatesEntries(t *testing.T) {
	dir := t.TempDir()

	manifestPath, changed, err := UpdateApplicationManifest(dir, []string{
		"/tmp/mcdd_armv7_1.2.3.ipk",
		"/tmp/miner-api-server_armv7_2.0.0.ipk",
	})
	if err != nil {
		t.Fatalf("UpdateApplicationManifest returned error: %v", err)
	}
	if !changed {
		t.Fatalf("UpdateApplicationManifest changed = false, want true")
	}
	if manifestPath != ApplicationManifestPath(dir) {
		t.Fatalf("manifest path = %q, want %q", manifestPath, ApplicationManifestPath(dir))
	}

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("reading manifest: %v", err)
	}
	got := string(data)
	for _, want := range []string{"mcdd 1.2.3", "miner-api-server 2.0.0"} {
		if !strings.Contains(got, want) {
			t.Fatalf("manifest = %q, want line containing %q", got, want)
		}
	}

	_, changed, err = UpdateApplicationManifest(dir, []string{"/tmp/mcdd_armv7_1.2.4.ipk"})
	if err != nil {
		t.Fatalf("UpdateApplicationManifest update returned error: %v", err)
	}
	if !changed {
		t.Fatalf("UpdateApplicationManifest update changed = false, want true")
	}
	data, err = os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("reading updated manifest: %v", err)
	}
	got = string(data)
	if !strings.Contains(got, "mcdd 1.2.4") {
		t.Fatalf("manifest = %q, want updated mcdd version", got)
	}
	if strings.Contains(got, "mcdd 1.2.3") {
		t.Fatalf("manifest = %q, still contains old mcdd version", got)
	}
}

func TestReadIPKControl(t *testing.T) {
	dir := t.TempDir()
	ipkPath := filepath.Join(dir, "mcdd_mfg_armv7_1.7.5.ipk")
	writeTestIPK(t, ipkPath, "mcdd", "1.7.5")

	name, version, err := ReadIPKControl(ipkPath)
	if err != nil {
		t.Fatalf("ReadIPKControl returned error: %v", err)
	}
	if name != "mcdd" {
		t.Fatalf("name = %q, want %q", name, "mcdd")
	}
	if version != "1.7.5" {
		t.Fatalf("version = %q, want %q", version, "1.7.5")
	}
}

func TestReadIPKControlArArchive(t *testing.T) {
	dir := t.TempDir()
	ipkPath := filepath.Join(dir, "hashboard-firmware_mfgtest_1.0.17.ipk")
	writeTestArIPK(t, ipkPath, "hashboard-firmware-mfgtest", "1.0.17")

	name, version, err := ReadIPKControl(ipkPath)
	if err != nil {
		t.Fatalf("ReadIPKControl returned error: %v", err)
	}
	if name != "hashboard-firmware-mfgtest" {
		t.Fatalf("name = %q, want %q", name, "hashboard-firmware-mfgtest")
	}
	if version != "1.0.17" {
		t.Fatalf("version = %q, want %q", version, "1.0.17")
	}
}

func TestReadIPKControlRejectsOversizedControl(t *testing.T) {
	dir := t.TempDir()
	ipkPath := filepath.Join(dir, "oversized_armv7_1.0.0.ipk")
	writeTestIPKWithControl(t, ipkPath, "Package: oversized\nVersion: 1.0.0\n"+strings.Repeat("x", maxIPKControlFileSize))

	if _, _, err := ReadIPKControl(ipkPath); err == nil {
		t.Fatalf("ReadIPKControl returned nil error for oversized control file")
	} else if !strings.Contains(err.Error(), "control file too large") {
		t.Fatalf("ReadIPKControl error = %v, want control file too large", err)
	}
}

func TestReadIPKControlMalformedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.ipk")
	if err := os.WriteFile(path, []byte("not an ipk"), 0644); err != nil {
		t.Fatalf("writing malformed fixture: %v", err)
	}

	if _, _, err := ReadIPKControl(path); err == nil {
		t.Fatalf("ReadIPKControl returned nil error for malformed file")
	}
}

func writeTestIPK(t *testing.T, path, pkg, version string) {
	t.Helper()

	writeTestIPKWithControl(t, path, "Package: "+pkg+"\n"+
		"Version: "+version+"\n"+
		"Architecture: armv7\n")
}

func writeTestIPKWithControl(t *testing.T, path, control string) {
	t.Helper()

	controlTarGz := writeTarGz(t, []testTarEntry{
		{
			name: "./control",
			body: control,
		},
	})
	dataTarGz := writeTarGz(t, nil)

	content := writeTarGz(t, []testTarEntry{
		{name: "debian-binary", body: "2.0\n"},
		{name: "./control.tar.gz", data: controlTarGz},
		{name: "data.tar.gz", data: dataTarGz},
	})
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("writing test IPK: %v", err)
	}
}

func writeTestArIPK(t *testing.T, path, pkg, version string) {
	t.Helper()

	controlTarGz := writeTarGz(t, []testTarEntry{
		{
			name: "control",
			body: "Package: " + pkg + "\n" +
				"Version: " + version + "\n" +
				"Architecture: all\n",
		},
	})
	dataTarGz := writeTarGz(t, nil)

	var buf bytes.Buffer
	writeArEntry(t, &buf, "debian-binary", []byte("2.0\n"))
	writeArEntry(t, &buf, "control.tar.gz", controlTarGz)
	writeArEntry(t, &buf, "data.tar.gz", dataTarGz)

	if err := os.WriteFile(path, append([]byte(arGlobalHeader), buf.Bytes()...), 0644); err != nil {
		t.Fatalf("writing test ar IPK: %v", err)
	}
}

func writeArEntry(t *testing.T, buf *bytes.Buffer, name string, data []byte) {
	t.Helper()

	if len(name) > 15 {
		t.Fatalf("test ar entry name %q too long", name)
	}
	if _, err := buf.WriteString(name + strings.Repeat(" ", 16-len(name))); err != nil {
		t.Fatalf("writing ar name: %v", err)
	}
	if _, err := buf.WriteString("0           "); err != nil {
		t.Fatalf("writing ar mtime: %v", err)
	}
	if _, err := buf.WriteString("0     "); err != nil {
		t.Fatalf("writing ar owner: %v", err)
	}
	if _, err := buf.WriteString("0     "); err != nil {
		t.Fatalf("writing ar group: %v", err)
	}
	if _, err := buf.WriteString("100644  "); err != nil {
		t.Fatalf("writing ar mode: %v", err)
	}
	if _, err := buf.WriteString(fmt.Sprintf("%-10d", len(data))); err != nil {
		t.Fatalf("writing ar size: %v", err)
	}
	if _, err := buf.WriteString("`\n"); err != nil {
		t.Fatalf("writing ar trailer: %v", err)
	}
	if _, err := buf.Write(data); err != nil {
		t.Fatalf("writing ar data: %v", err)
	}
	if len(data)%2 != 0 {
		if err := buf.WriteByte('\n'); err != nil {
			t.Fatalf("writing ar padding: %v", err)
		}
	}
}

type testTarEntry struct {
	name string
	body string
	data []byte
}

func writeTarGz(t *testing.T, entries []testTarEntry) []byte {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		data := entry.data
		if data == nil {
			data = []byte(entry.body)
		}
		if err := tw.WriteHeader(&tar.Header{
			Name: entry.name,
			Mode: 0644,
			Size: int64(len(data)),
		}); err != nil {
			t.Fatalf("writing tar header: %v", err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatalf("writing tar body: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("closing tar writer: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("closing gzip writer: %v", err)
	}
	return buf.Bytes()
}

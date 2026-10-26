package distroinject

import (
	"archive/zip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplaceZipFromDirRebuildsZip(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "artifact.zip")
	if err := createTestZip(zipPath, map[string]string{
		"existing.txt": "stale",
		"removed.txt":  "removed",
	}); err != nil {
		t.Fatalf("creating test zip: %v", err)
	}

	baseDir := filepath.Join(dir, "base")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatalf("creating base dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "existing.txt"), []byte("new"), 0o644); err != nil {
		t.Fatalf("writing existing file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "application_manifest.txt"), []byte("mcdd 1.2.3\n"), 0o644); err != nil {
		t.Fatalf("writing new file: %v", err)
	}

	if err := replaceZipFromDir(baseDir, zipPath); err != nil {
		t.Fatalf("replaceZipFromDir returned error: %v", err)
	}

	files, err := readTestZip(zipPath)
	if err != nil {
		t.Fatalf("reading updated zip: %v", err)
	}
	if got := files["existing.txt"]; got != "new" {
		t.Fatalf("existing.txt = %q, want new", got)
	}
	if got := files["application_manifest.txt"]; got != "mcdd 1.2.3\n" {
		t.Fatalf("application_manifest.txt = %q, want manifest content", got)
	}
	if _, ok := files["removed.txt"]; ok {
		t.Fatal("removed.txt still exists in rebuilt zip")
	}
}

func TestDiscoverArtifactsMultipleMatchesFormatsError(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "a.rootfs.ext4")
	second := filepath.Join(dir, "b.rootfs.ext4")

	if err := os.WriteFile(first, []byte("first"), 0o644); err != nil {
		t.Fatalf("writing first rootfs: %v", err)
	}
	if err := os.WriteFile(second, []byte("second"), 0o644); err != nil {
		t.Fatalf("writing second rootfs: %v", err)
	}

	_, _, err := discoverArtifacts(dir)
	want := "more than 1 rootfs file found:\n" + first + "\n" + second
	if err == nil || err.Error() != want {
		t.Fatalf("discoverArtifacts error = %v, want %q", err, want)
	}
}

func TestDiscoverArtifactsNested(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "nested")
	if err := os.MkdirAll(filepath.Join(nested, "ignored.rootfs.ext4"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(nested, "ignored.swu"), 0o755); err != nil {
		t.Fatal(err)
	}
	rootfs := filepath.Join(nested, "image.rootfs.ext4")
	swuFiles := []string{filepath.Join(dir, "a.swu"), filepath.Join(nested, "image.swu"), filepath.Join(dir, "z.swu")}
	for _, path := range append([]string{rootfs}, swuFiles...) {
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gotRootfs, gotSWUs, err := discoverArtifacts(dir)
	if err != nil {
		t.Fatal(err)
	}
	if gotRootfs != rootfs || strings.Join(gotSWUs, "\n") != strings.Join(swuFiles, "\n") {
		t.Fatalf("discoverArtifacts = (%q, %q), want (%q, %q)", gotRootfs, gotSWUs, rootfs, swuFiles)
	}
}

func TestDiscoverArtifactsMissingRootfs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "image.swu"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := discoverArtifacts(dir)
	want := "no rootfs file matching *.rootfs.ext4 found"
	if err == nil || err.Error() != want {
		t.Fatalf("discoverArtifacts error = %v, want %q", err, want)
	}
}

func TestWriteSHA256UpdatesShaFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "artifact.bin"), []byte("artifact"), 0o644); err != nil {
		t.Fatalf("writing artifact: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sha.txt"), []byte("stale\n"), 0o644); err != nil {
		t.Fatalf("writing stale sha.txt: %v", err)
	}

	if err := writeSHA256(dir); err != nil {
		t.Fatalf("writeSHA256 returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "sha.txt"))
	if err != nil {
		t.Fatalf("reading sha.txt: %v", err)
	}
	sum := sha256.Sum256([]byte("artifact"))
	want := fmt.Sprintf("%x  artifact.bin\n", sum)
	if got := string(data); got != want {
		t.Fatalf("sha.txt = %q, want %q", got, want)
	}
}

func createTestZip(path string, files map[string]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := zip.NewWriter(f)
	defer w.Close()
	for name, content := range files {
		writer, err := w.Create(name)
		if err != nil {
			return err
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			return err
		}
	}
	return nil
}

func readTestZip(path string) (map[string]string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	files := make(map[string]string)
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(rc)
		closeErr := rc.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		files[f.Name] = string(data)
	}
	return files, nil
}

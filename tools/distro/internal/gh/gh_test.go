package gh

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestShouldDownloadAsset_NotFound(t *testing.T) {
	asset := releaseAsset{Name: "a.ipk", Size: 5}
	decision, err := shouldDownloadAsset(filepath.Join(t.TempDir(), asset.Name), asset)
	if err != nil {
		t.Fatalf("shouldDownloadAsset() error = %v", err)
	}
	if !decision.Download {
		t.Fatalf("shouldDownloadAsset() Download = false, want true")
	}
}

func TestShouldDownloadAsset_SizeMatchNoDigest_Skip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.ipk")
	content := []byte("hello")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	asset := releaseAsset{Name: "a.ipk", Size: int64(len(content))}
	decision, err := shouldDownloadAsset(path, asset)
	if err != nil {
		t.Fatalf("shouldDownloadAsset() error = %v", err)
	}
	if decision.Download {
		t.Fatalf("shouldDownloadAsset() Download = true, want false")
	}
}

func TestShouldDownloadAsset_SizeMatchDigestMatch_Skip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.ipk")
	content := []byte("hello")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	asset := releaseAsset{
		Name:   "a.ipk",
		Size:   int64(len(content)),
		Digest: fmt.Sprintf("sha256:%x", sum),
	}

	decision, err := shouldDownloadAsset(path, asset)
	if err != nil {
		t.Fatalf("shouldDownloadAsset() error = %v", err)
	}
	if decision.Download {
		t.Fatalf("shouldDownloadAsset() Download = true, want false")
	}
}

func TestShouldDownloadAsset_SizeMatchDigestMismatch_Download(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.ipk")
	content := []byte("hello")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	asset := releaseAsset{
		Name:   "a.ipk",
		Size:   int64(len(content)),
		Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}

	decision, err := shouldDownloadAsset(path, asset)
	if err != nil {
		t.Fatalf("shouldDownloadAsset() error = %v", err)
	}
	if !decision.Download {
		t.Fatalf("shouldDownloadAsset() Download = false, want true")
	}
}

func TestShouldDownloadAsset_SizeMismatch_Download(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.ipk")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	asset := releaseAsset{Name: "a.ipk", Size: 1}

	decision, err := shouldDownloadAsset(path, asset)
	if err != nil {
		t.Fatalf("shouldDownloadAsset() error = %v", err)
	}
	if !decision.Download {
		t.Fatalf("shouldDownloadAsset() Download = false, want true")
	}
}

package postbuild

import (
	"archive/zip"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"proto-distro/tools/distro/internal/config"
)

func TestFinalizeArtifactOutputCreatesLinksAndZip(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "distro.yaml")
	artifactsDir := filepath.Join(dir, "artifacts")
	folderName := "miner-image-release-c3-p1-1.2.3"
	folderPath := filepath.Join(artifactsDir, folderName)

	if err := os.MkdirAll(folderPath, 0o755); err != nil {
		t.Fatalf("creating artifact folder: %v", err)
	}
	if err := os.WriteFile(filepath.Join(folderPath, "artifact.txt"), []byte("artifact"), 0o644); err != nil {
		t.Fatalf("writing artifact: %v", err)
	}

	cfg := &config.Config{
		Artifacts: config.Artifacts{RootDir: "artifacts"},
	}
	if err := FinalizeArtifactOutput(cfg, configPath, "c3-p1", "release", folderPath); err != nil {
		t.Fatalf("FinalizeArtifactOutput returned error: %v", err)
	}

	assertSymlink(t, filepath.Join(artifactsDir, "miner-image-release-c3-p1-latest"), folderName)
	assertSymlink(t, filepath.Join(artifactsDir, "miner-image-latest"), folderName)
	assertSymlink(t, filepath.Join(artifactsDir, "miner-image-latest.zip"), folderName+".zip")

	zipPath := folderPath + ".zip"
	if _, err := os.Stat(zipPath); err != nil {
		t.Fatalf("zip was not created: %v", err)
	}
	assertZipContains(t, zipPath, "artifact.txt")
	wantChecksum := fmt.Sprintf("%x  artifact.txt\n", sha256.Sum256([]byte("artifact")))
	if got, err := os.ReadFile(filepath.Join(folderPath, "sha.txt")); err != nil || string(got) != wantChecksum {
		t.Fatalf("checksums without local exports = %q: %v", got, err)
	}
	assertZipContains(t, zipPath, "sha.txt")
}

func TestFinalizeArtifactOutputExportsLocalFiles(t *testing.T) {
	for _, variant := range []string{"dev", "release", "mfg"} {
		t.Run(variant, func(t *testing.T) {
			dir := t.TempDir()
			sourceDir := filepath.Join(dir, "local")
			if err := os.Mkdir(sourceDir, 0o755); err != nil {
				t.Fatal(err)
			}
			content := []byte("#!/bin/sh\nprintf updater\n")
			if err := os.WriteFile(filepath.Join(sourceDir, "proto-swupdate"), content, 0o755); err != nil {
				t.Fatal(err)
			}
			cfg := &config.Config{Artifacts: config.Artifacts{RootDir: "artifacts", ImagesBasePath: "images", Files: []string{"rootfs.ext4"}, LocalFiles: []string{"local/proto-swupdate"}}}
			images := filepath.Join(dir, "images")
			if err := os.Mkdir(images, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(images, "rootfs.ext4"), []byte("rootfs"), 0o644); err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(dir, "distro.yaml")
			folder, err := CreateArtifactOutput(cfg, configPath, dir, "c3-p1", "proto", variant, "1.2.3")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(folder, "proto-swupdate")); !os.IsNotExist(err) {
				t.Fatalf("local artifact exported before finalization: %v", err)
			}
			if err := os.WriteFile(filepath.Join(folder, "sha.txt"), []byte("old checksum sentinel"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := FinalizeArtifactOutput(cfg, configPath, "c3-p1", variant, folder); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(filepath.Join(folder, "proto-swupdate"))
			if err != nil || info.Mode().Perm() != 0o755 {
				t.Fatalf("exported executable mode: %v: %v", info, err)
			}
			checksums, err := os.ReadFile(filepath.Join(folder, "sha.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(checksums), fmt.Sprintf("%x  proto-swupdate\n", sha256.Sum256(content))) {
				t.Fatal("local artifact missing from checksums")
			}
			assertZipContains(t, folder+".zip", "proto-swupdate")
			assertZipContains(t, folder+".zip", "sha.txt")
			assertSymlink(t, filepath.Join(dir, "artifacts", "miner-image-latest"), filepath.Base(folder))
		})
	}
}

func TestCreateArtifactOutputReplacesExistingFolder(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "distro.yaml")
	distroDir := dir
	imagesDir := filepath.Join(dir, "build", "tmp-glibc", "deploy", "images", "c3-p1")
	if err := os.MkdirAll(imagesDir, 0o755); err != nil {
		t.Fatalf("creating images dir: %v", err)
	}
	srcArtifact := filepath.Join(imagesDir, "miner-image-release-c3-p1-1.2.3.rootfs.ext4")
	if err := os.WriteFile(srcArtifact, []byte("new artifact"), 0o644); err != nil {
		t.Fatalf("writing source artifact: %v", err)
	}

	artifactsDir := filepath.Join(dir, "artifacts")
	existingFolder := filepath.Join(artifactsDir, "miner-image-release-c3-p1-1.2.3")
	if err := os.MkdirAll(existingFolder, 0o755); err != nil {
		t.Fatalf("creating existing output folder: %v", err)
	}
	if err := os.WriteFile(filepath.Join(existingFolder, "stale.txt"), []byte("stale"), 0o644); err != nil {
		t.Fatalf("writing stale artifact: %v", err)
	}

	cfg := &config.Config{
		Artifacts: config.Artifacts{
			RootDir:        "artifacts",
			ImagesBasePath: "build/tmp-glibc/deploy/images/{machine}/",
			Files:          []string{"{target}-{machine}-{version}.rootfs.ext4"},
		},
	}
	folderPath, err := CreateArtifactOutput(cfg, configPath, distroDir, "c3-p1", "proto", "release", "1.2.3")
	if err != nil {
		t.Fatalf("CreateArtifactOutput returned error: %v", err)
	}
	if folderPath != existingFolder {
		t.Fatalf("folderPath = %q, want %q", folderPath, existingFolder)
	}
	if _, err := os.Stat(filepath.Join(existingFolder, "stale.txt")); !os.IsNotExist(err) {
		t.Fatalf("stale file still exists, stat err = %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(existingFolder, filepath.Base(srcArtifact))); err != nil {
		t.Fatalf("reading copied artifact: %v", err)
	} else if string(data) != "new artifact" {
		t.Fatalf("copied artifact = %q, want new artifact", data)
	}
}

func TestCreateArtifactOutputVariantFiles(t *testing.T) {
	for _, tc := range []struct {
		name, variant                                       string
		metadata, requireMetadata, missingRootfs, wantError bool
	}{
		{name: "fresh release without deploy AVB", variant: "release"},
		{name: "missing plain rootfs", variant: "release", missingRootfs: true, wantError: true},
		{name: "release exports required metadata", variant: "release", metadata: true, requireMetadata: true},
		{name: "release requires metadata", variant: "release", requireMetadata: true, wantError: true},
		{name: "mfg ignores release metadata", variant: "mfg", requireMetadata: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			imagesDir := filepath.Join(dir, "images")
			if err := os.Mkdir(imagesDir, 0755); err != nil {
				t.Fatal(err)
			}
			rootfsName := config.TargetName(tc.variant) + "-c3-p1-1.2.3.rootfs.ext4"
			if !tc.missingRootfs {
				if err := os.WriteFile(filepath.Join(imagesDir, rootfsName), []byte("rootfs"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.metadata {
				if err := os.WriteFile(filepath.Join(imagesDir, "release.metadata"), []byte("metadata"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			cfg := &config.Config{Artifacts: config.Artifacts{
				ImagesBasePath: "images", Files: []string{"{target}-{machine}-{version}.rootfs.ext4"},
			}}
			if tc.requireMetadata {
				cfg.Artifacts.Variants = map[string]config.ArtifactVariant{"release": {Files: []string{"release.metadata"}}}
			}
			folder, err := CreateArtifactOutput(cfg, filepath.Join(dir, "distro.yaml"), dir, "c3-p1", "proto", tc.variant, "1.2.3")
			if tc.wantError {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("expected missing artifact, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(filepath.Join(folder, rootfsName)); err != nil || string(data) != "rootfs" {
				t.Fatalf("exported rootfs = %q, %v", data, err)
			}
			if tc.metadata {
				if data, err := os.ReadFile(filepath.Join(folder, "release.metadata")); err != nil || string(data) != "metadata" {
					t.Fatalf("exported metadata = %q, %v", data, err)
				}
			}
			if _, err := os.Stat(filepath.Join(folder, rootfsName+".avb")); !os.IsNotExist(err) {
				t.Fatalf("AVB unexpectedly exported: %v", err)
			}
		})
	}
}

func assertSymlink(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("reading symlink %s: %v", path, err)
	}
	if got != want {
		t.Fatalf("symlink %s = %q, want %q", path, got, want)
	}
}

func assertZipContains(t *testing.T, zipPath, name string) {
	t.Helper()
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("opening zip: %v", err)
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name == name {
			return
		}
	}
	t.Fatalf("zip %s does not contain %s", zipPath, name)
}

package postbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"proto-distro/tools/distro/internal/config"
)

func TestFinalizeArtifactOutputThroughSymlinkedRoot(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	realRoot := filepath.Join(dir, "deep", "real", "artifacts")
	if err := os.MkdirAll(realRoot, 0755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(filepath.Join(dir, "deep", "real"), alias); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(realRoot, "miner-image-release-c3-p1-1")
	if err := os.Mkdir(folder, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "payload"), []byte("final"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Artifacts: config.Artifacts{RootDir: filepath.Join(alias, "artifacts")}}
	if err := FinalizeArtifactOutput(cfg, filepath.Join(dir, "distro.yaml"), "c3-p1", "release", folder); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		"miner-image-latest":               folder,
		"miner-image-release-c3-p1-latest": folder,
		"miner-image-latest.zip":           folder + ".zip",
	} {
		got, err := os.Stat(filepath.Join(realRoot, name))
		if err != nil {
			t.Fatalf("broken %s: %v", name, err)
		}
		want, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(got, want) {
			t.Fatalf("%s points to wrong output", name)
		}
	}
}

func TestCreateArtifactOutputNestedDirectorySymlinks(t *testing.T) {
	for _, kind := range []string{"overlap", "cycle", "default-cycle", "external-alias"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "images", "bundle")
			external := filepath.Join(dir, "external")
			for _, path := range []string{source, external} {
				if err := os.MkdirAll(path, 0755); err != nil {
					t.Fatal(err)
				}
			}
			sentinel := filepath.Join(external, "payload")
			if err := os.WriteFile(sentinel, []byte("external sentinel"), 0600); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(dir, "output", "nested")
			target := external
			wantError := ""
			switch kind {
			case "overlap":
				output = filepath.Join(external, "new", "nested")
				wantError = "output directory must not be inside input artifact directory"
				// Bound the old copier: fail before it can recurse into its output.
				if err := os.Symlink("missing", filepath.Join(external, "00-stop")); err != nil {
					t.Fatal(err)
				}
			case "cycle", "default-cycle":
				target = source
				wantError = "directory symlink cycle:"
			}
			if err := os.Symlink(target, filepath.Join(source, "alias")); err != nil {
				t.Fatal(err)
			}
			if kind == "external-alias" {
				if err := os.Symlink(external, filepath.Join(source, "second-alias")); err != nil {
					t.Fatal(err)
				}
			}
			cfg := &config.Config{Artifacts: config.Artifacts{
				RootDir: "artifacts", ImagesBasePath: "images", Files: []string{"bundle"},
			}}
			outputParent := filepath.Dir(output)
			if kind == "default-cycle" {
				output = ""
				outputParent = filepath.Join(dir, "artifacts")
			}
			folder, err := CreateArtifactOutput(cfg, filepath.Join(dir, "distro.yaml"), dir, "c3-p1", "proto", "base", "1.2.3", output)
			if wantError != "" {
				if err == nil || !strings.Contains(err.Error(), wantError) {
					t.Errorf("error = %v, want %q", err, wantError)
				}
				if _, err := os.Lstat(outputParent); !os.IsNotExist(err) {
					t.Errorf("preflight created output parent: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				for _, alias := range []string{"alias", "second-alias"} {
					copied := filepath.Join(folder, "bundle", alias, "payload")
					data, err := os.ReadFile(copied)
					if err != nil || string(data) != "external sentinel" {
						t.Errorf("copied alias %s = %q: %v", alias, data, err)
					}
					info, err := os.Lstat(filepath.Dir(copied))
					if err != nil || !info.IsDir() {
						t.Errorf("alias not copied as directory: %v", err)
					}
				}
			}
			data, err := os.ReadFile(sentinel)
			if err != nil || string(data) != "external sentinel" {
				t.Errorf("source changed = %q: %v", data, err)
			}
			assertSymlink(t, filepath.Join(source, "alias"), target)
		})
	}
}

package postbuild

import (
	"os"
	"path/filepath"
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

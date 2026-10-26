package distroinject

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"proto-distro/tools/distro/internal/config"
)

func TestAVBIdentity(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "c3"), []byte("c3"), 0600); err != nil {
		t.Fatal(err)
	}
	keys := map[string]config.AVBKeys{
		"c3-p1": {PrivateKey: "c3", PublicKey: "c3", AVBPublicKey: "c3"},
	}
	for _, tt := range []struct {
		name, machine, variant, want, bad string
		sidecar                           bool
	}{
		{name: "miner-image-release-c3-p1-1", want: "c3"},
		{name: "miner-image-release-unknown-1", bad: "exactly one"},
		{name: "miner-image-mfg-c3-p1-1", want: "c3"},
		{name: "miner-image-mfg-c3-p1-1", sidecar: true, want: "c3"},
		{name: "miner-image-mfg-c3-p1-1", machine: "c3-p1", variant: "mfg", want: "c3"},
		{name: "miner-image-mfg-unknown-1", bad: "exactly one"},
		{name: "generic", machine: "unknown", variant: "mfg", bad: "keys missing"},
		{name: "generic"},
		{name: "generic", sidecar: true, bad: "canonical"},
		{name: "generic", sidecar: true, machine: "c3-p1", variant: "release", want: "c3"},
		{name: "miner-image-release-unknown-1", machine: "c3-p1", variant: "release", bad: "contradicts"},
		{name: "miner-image-mfg-c3-p1-1", machine: "c3-p1", variant: "release", bad: "contradicts"},
	} {
		t.Run(tt.name+tt.machine+tt.variant+tt.bad, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), tt.name+".rootfs.ext4")
			if tt.sidecar {
				if err := os.WriteFile(root+".avb.body", []byte("old"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := avbKeys(root, Options{Machine: tt.machine, Variant: tt.variant, AVB: keys, ConfigPath: filepath.Join(dir, "distro.yaml")})
			if tt.bad != "" {
				if err == nil || !strings.Contains(err.Error(), tt.bad) {
					t.Fatalf("got %v, want %s", err, tt.bad)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.want == "" {
				if got != nil {
					t.Fatal("unsigned rootfs selected keys")
				}
				return
			}
			if got == nil {
				t.Fatal("release missing keys")
			}
			data, err := os.ReadFile(got.PrivateKey)
			if err != nil || string(data) != tt.want {
				t.Fatalf("wrong machine key contents %q: %v", data, err)
			}
		})
	}
	t.Run("ambiguous", func(t *testing.T) {
		copyKeys := map[string]config.AVBKeys{"c3": keys["c3-p1"], "c3-p1": keys["c3-p1"]}
		if _, err := avbKeys("miner-image-release-c3-p1-1.rootfs.ext4", Options{AVB: copyKeys}); err == nil {
			t.Fatal("ambiguous machine accepted")
		}
	})
	t.Run("missing field", func(t *testing.T) {
		if _, err := avbKeys("miner-image-release-c3-p1-1.rootfs.ext4", Options{AVB: map[string]config.AVBKeys{"c3-p1": {}}}); err == nil || !strings.Contains(err.Error(), "private_key") {
			t.Fatalf("missing key: %v", err)
		}
	})
}

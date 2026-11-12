package distroinject

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInvalidAVBKeysFailBeforeMutation(t *testing.T) {
	for _, field := range []string{"private_key", "public_key", "avb_public_key"} {
		for _, invalid := range []string{"empty", "missing", "directory"} {
			t.Run(field+"/"+invalid, func(t *testing.T) {
				testDocker(t)
				t.Setenv("DISTRO_TEST_NO_DOCKER", "1")
				cfg, configPath := testConfig(t)
				dir := t.TempDir()
				root := filepath.Join(dir, "miner-image-release-c3-p1-1.rootfs.ext4")
				writeTestFile(t, root, "plain")
				keys := cfg.AVB["c3-p1"]
				value := ""
				if invalid == "missing" {
					value = filepath.Join(t.TempDir(), "absent")
				}
				if invalid == "directory" {
					value = t.TempDir()
				}
				switch field {
				case "private_key":
					keys.PrivateKey = value
				case "public_key":
					keys.PublicKey = value
				case "avb_public_key":
					keys.AVBPublicKey = value
				}
				cfg.AVB["c3-p1"] = keys
				_, err := Run(context.Background(), Options{InputPath: dir, ConfigPath: configPath, AVB: cfg.AVB, Version: "2"})
				if err == nil || !strings.Contains(err.Error(), field) {
					t.Fatalf("invalid %s accepted: %v", field, err)
				}
				data, err := os.ReadFile(root)
				if err != nil || string(data) != "plain" {
					t.Fatalf("rootfs mutated: %q, %v", data, err)
				}
			})
		}
	}
}

func TestAVBKeysResolveSymlinksWithoutMutatingConfig(t *testing.T) {
	cfg, configPath := testConfig(t)
	dir := filepath.Dir(configPath)
	if err := os.Symlink("c3", filepath.Join(dir, "selected-key")); err != nil {
		t.Fatal(err)
	}
	keys := cfg.AVB["c3-p1"]
	keys.PrivateKey, keys.PublicKey, keys.AVBPublicKey = "selected-key", "selected-key", "selected-key"
	cfg.AVB["c3-p1"] = keys
	got, err := avbKeys("miner-image-release-c3-p1-1.rootfs.ext4", Options{AVB: cfg.AVB, ConfigPath: configPath})
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(dir, "c3"))
	if err != nil {
		t.Fatal(err)
	}
	if got.PrivateKey != want || got.PublicKey != want || got.AVBPublicKey != want {
		t.Fatalf("unresolved bind paths: %+v", got)
	}
	if cfg.AVB["c3-p1"] != keys {
		t.Fatal("selected key resolution changed loaded config")
	}
}

func TestExplicitIdentityMismatchFailsBeforeMutation(t *testing.T) {
	testDocker(t)
	t.Setenv("DISTRO_TEST_NO_DOCKER", "1")
	cfg, configPath := testConfig(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "miner-image-release-unknown-1.rootfs.ext4")
	writeTestFile(t, root, "plain")
	_, err := RunWithConfig(context.Background(), Params{Config: cfg, ConfigPath: configPath, InputPath: dir, Machine: "c3-p1", Variant: "release", Version: "2"})
	if err == nil || !strings.Contains(err.Error(), "contradicts") {
		t.Fatalf("wrong explicit identity accepted: %v", err)
	}
	data, err := os.ReadFile(root)
	if err != nil || string(data) != "plain" {
		t.Fatalf("rootfs mutated: %q, %v", data, err)
	}
}

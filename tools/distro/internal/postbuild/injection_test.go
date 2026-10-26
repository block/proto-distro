package postbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"proto-distro/tools/distro/internal/config"
)

const testArch = "armv7"

func TestPickIPKMatchBasic(t *testing.T) {
	tmp := t.TempDir()
	ipk := filepath.Join(tmp, "mcdd_armv7_1.2.4.ipk")
	if err := os.WriteFile(ipk, []byte("ipk"), 0o644); err != nil {
		t.Fatalf("creating test file: %v", err)
	}

	got, err := pickIPKMatch([]string{ipk}, "mcdd", "mcdd", "local", testArch)
	if err != nil {
		t.Fatalf("pickIPKMatch returned error: %v", err)
	}
	if got != ipk {
		t.Fatalf("pickIPKMatch = %q, want %q", got, ipk)
	}
}

func TestPickIPKMatchVariant(t *testing.T) {
	tmp := t.TempDir()
	ipk := filepath.Join(tmp, "mcdd_dev_armv7_1.2.4.ipk")
	if err := os.WriteFile(ipk, []byte("ipk"), 0o644); err != nil {
		t.Fatalf("creating test file: %v", err)
	}

	got, err := pickIPKMatch([]string{ipk}, "mcdd", "mcdd_dev", "local", testArch)
	if err != nil {
		t.Fatalf("pickIPKMatch returned error: %v", err)
	}
	if got != ipk {
		t.Fatalf("pickIPKMatch = %q, want %q", got, ipk)
	}
}

func TestPickIPKMatchReleaseDoesNotMatchVariant(t *testing.T) {
	tmp := t.TempDir()
	devIPK := filepath.Join(tmp, "mcdd_dev_armv7_1.2.4.ipk")
	if err := os.WriteFile(devIPK, []byte("ipk"), 0o644); err != nil {
		t.Fatalf("creating test file: %v", err)
	}

	got, err := pickIPKMatch([]string{devIPK}, "mcdd", "mcdd", "remote", testArch)
	if err != nil {
		t.Fatalf("pickIPKMatch returned error: %v", err)
	}
	if got != "" {
		t.Fatalf("pickIPKMatch = %q, want empty (release should not match variant)", got)
	}
}

func TestPickIPKMatchReleaseAndVariantCoexist(t *testing.T) {
	tmp := t.TempDir()
	release := filepath.Join(tmp, "mcdd_armv7_1.2.4.ipk")
	dev := filepath.Join(tmp, "mcdd_dev_armv7_1.2.4.ipk")
	for _, p := range []string{release, dev} {
		if err := os.WriteFile(p, []byte("ipk"), 0o644); err != nil {
			t.Fatalf("creating test file: %v", err)
		}
	}

	got, err := pickIPKMatch([]string{release, dev}, "mcdd", "mcdd", "remote", testArch)
	if err != nil {
		t.Fatalf("pickIPKMatch returned error: %v", err)
	}
	if got != release {
		t.Fatalf("pickIPKMatch(release) = %q, want %q", got, release)
	}

	got, err = pickIPKMatch([]string{release, dev}, "mcdd", "mcdd_dev", "remote", testArch)
	if err != nil {
		t.Fatalf("pickIPKMatch returned error: %v", err)
	}
	if got != dev {
		t.Fatalf("pickIPKMatch(dev) = %q, want %q", got, dev)
	}
}

func TestPickIPKMatchNoMatch(t *testing.T) {
	tmp := t.TempDir()
	other := filepath.Join(tmp, "other_armv7_1.0.0.ipk")
	if err := os.WriteFile(other, []byte("ipk"), 0o644); err != nil {
		t.Fatalf("creating test file: %v", err)
	}

	got, err := pickIPKMatch([]string{other}, "mcdd", "mcdd", "local", testArch)
	if err != nil {
		t.Fatalf("pickIPKMatch returned error: %v", err)
	}
	if got != "" {
		t.Fatalf("pickIPKMatch = %q, want empty", got)
	}
}

func TestPickIPKMatchMultipleErrors(t *testing.T) {
	tmp := t.TempDir()
	a := filepath.Join(tmp, "mcdd_armv7_1.2.3.ipk")
	b := filepath.Join(tmp, "mcdd_armv7_1.2.4.ipk")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("ipk"), 0o644); err != nil {
			t.Fatalf("creating test file: %v", err)
		}
	}

	_, err := pickIPKMatch([]string{a, b}, "mcdd", "mcdd", "local", testArch)
	if err == nil {
		t.Fatalf("expected error for multiple matches, got nil")
	}
}

func TestPickIPKMatchHyphenatedPackageName(t *testing.T) {
	tmp := t.TempDir()
	ipk := filepath.Join(tmp, "hashboard-tools_armv7_1.0.11.ipk")
	if err := os.WriteFile(ipk, []byte("ipk"), 0o644); err != nil {
		t.Fatalf("creating test file: %v", err)
	}

	got, err := pickIPKMatch([]string{ipk}, "hashboard-tools", "hashboard-tools", "remote", testArch)
	if err != nil {
		t.Fatalf("pickIPKMatch returned error: %v", err)
	}
	if got != ipk {
		t.Fatalf("pickIPKMatch = %q, want %q", got, ipk)
	}
}

func TestPickIPKMatchMultiArchFiltersToConfigured(t *testing.T) {
	tmp := t.TempDir()
	armv7 := filepath.Join(tmp, "hashboard-tools_armv7_1.0.11.ipk")
	arm64 := filepath.Join(tmp, "hashboard-tools_arm64_1.0.11.ipk")
	amd64 := filepath.Join(tmp, "hashboard-tools_amd64_1.0.11.ipk")
	for _, p := range []string{armv7, arm64, amd64} {
		if err := os.WriteFile(p, []byte("ipk"), 0o644); err != nil {
			t.Fatalf("creating test file: %v", err)
		}
	}

	got, err := pickIPKMatch([]string{armv7, arm64, amd64}, "hashboard-tools", "hashboard-tools", "remote", "armv7")
	if err != nil {
		t.Fatalf("pickIPKMatch returned error: %v", err)
	}
	if got != armv7 {
		t.Fatalf("pickIPKMatch = %q, want %q", got, armv7)
	}
}

func TestMatchesIPKPrefix(t *testing.T) {
	tests := []struct {
		filename   string
		pkgName    string
		ipkPrefix  string
		targetArch string
		want       bool
	}{
		// Basic: name_arch_version.ipk
		{"mcdd_armv7_1.2.4.ipk", "mcdd", "mcdd", "armv7", true},
		// Variant: name_variant_arch_version.ipk
		{"mcdd_dev_armv7_1.2.4.ipk", "mcdd", "mcdd_dev", "armv7", true},
		{"mcdd_mfg_armv7_1.2.4.ipk", "mcdd", "mcdd_mfg", "armv7", true},
		// Arch mismatch
		{"mcdd_arm64_1.2.4.ipk", "mcdd", "mcdd", "armv7", false},
		// "all" arch always matches any target
		{"mcdd_all_1.2.4.ipk", "mcdd", "mcdd", "armv7", true},
		// Release prefix must NOT match variant files
		{"mcdd_dev_armv7_1.2.4.ipk", "mcdd", "mcdd", "armv7", false},
		{"mcdd_mfg_armv7_1.2.4.ipk", "mcdd", "mcdd", "armv7", false},
		// Variant prefix must NOT match release files
		{"mcdd_armv7_1.2.4.ipk", "mcdd", "mcdd_dev", "armv7", false},
		// No match at all
		{"other_armv7_1.0.0.ipk", "mcdd", "mcdd", "armv7", false},
		// Hyphenated package names
		{"hashboard-tools_armv7_1.0.11.ipk", "hashboard-tools", "hashboard-tools", "armv7", true},
		{"hashboard-firmware-dev_armv7_1.0.ipk", "hashboard-firmware-dev", "hashboard-firmware-dev", "armv7", true},
		// Dash-delimited filenames should NOT match
		{"mcdd-armv7-1.2.4.ipk", "mcdd", "mcdd", "armv7", false},
		{"mcdd-dev-armv7-1.2.4.ipk", "mcdd", "mcdd_dev", "armv7", false},
		// Other
		{"proto-os_1.2.4.ipk", "proto-os", "proto-os", "armv7", true},
		{"mcdd_dev_1.2.3.ipk", "mcdd", "mcdd", "armv7", false},
	}

	for _, tt := range tests {
		got := matchesIPK(tt.filename, tt.ipkPrefix, tt.targetArch)
		if got != tt.want {
			t.Errorf("matchesIPK(%q, %q, %q) = %v, want %v", tt.filename, tt.ipkPrefix, tt.targetArch, got, tt.want)
		}
	}
}

func TestPickIPKMatchRejectsUnsupportedArch(t *testing.T) {
	tmp := t.TempDir()
	ipk := filepath.Join(tmp, "mcdd_amd64_1.0.0.ipk")
	if err := os.WriteFile(ipk, []byte("ipk"), 0o644); err != nil {
		t.Fatalf("creating test file: %v", err)
	}

	got, err := pickIPKMatch([]string{ipk}, "mcdd", "mcdd", "local", "amd64")
	if err != nil {
		t.Fatalf("pickIPKMatch returned error: %v", err)
	}
	if got != "" {
		t.Fatalf("pickIPKMatch = %q, want empty (amd64 is not supported)", got)
	}

	got, err = pickIPKMatch([]string{ipk}, "mcdd", "mcdd", "local", testArch)
	if err != nil {
		t.Fatalf("pickIPKMatch returned error: %v", err)
	}
	if got != "" {
		t.Fatalf("pickIPKMatch = %q, want empty (amd64 not matching armv7)", got)
	}
}

func TestResolveVariantIPKsRejectsUnsupportedArch(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{Arch: "amd64"},
	}

	_, err := resolveVariantIPKs(cfg, filepath.Join(t.TempDir(), "distro.yaml"), config.InjectionVariant{})
	if err == nil {
		t.Fatalf("resolveVariantIPKs returned nil error, want unsupported arch error")
	}
	if !strings.Contains(err.Error(), "only armv7 is supported") {
		t.Fatalf("resolveVariantIPKs error = %q, want armv7-only message", err.Error())
	}
}

func TestPickIPKMatchArchAll(t *testing.T) {
	tmp := t.TempDir()
	ipk := filepath.Join(tmp, "miner-web_all_2.3.0.ipk")
	if err := os.WriteFile(ipk, []byte("ipk"), 0o644); err != nil {
		t.Fatalf("creating test file: %v", err)
	}

	got, err := pickIPKMatch([]string{ipk}, "miner-web", "miner-web", "local", "armv7")
	if err != nil {
		t.Fatalf("pickIPKMatch returned error: %v", err)
	}
	if got != ipk {
		t.Fatalf("pickIPKMatch = %q, want %q (arch 'all' should always match)", got, ipk)
	}
}

func TestDeriveIPKPrefix(t *testing.T) {
	tests := []struct {
		name, variant string
		want          string
	}{
		{"mcdd", "", "mcdd"},
		{"mcdd", "release", "mcdd"},
		{"mcdd", "dev", "mcdd_dev"},
		{"mcdd", "mfg", "mcdd_mfg"},
		{"miner-api-server", "dev", "miner-api-server_dev"},
	}
	for _, tt := range tests {
		got := deriveIPKPrefix(tt.name, tt.variant)
		if got != tt.want {
			t.Errorf("deriveIPKPrefix(%q, %q) = %q, want %q", tt.name, tt.variant, got, tt.want)
		}
	}
}

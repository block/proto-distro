package builder

import (
	"context"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"proto-distro/tools/distro/internal/config"
)

func TestDistroVersionStringUsesEnvOverrideWhenSet(t *testing.T) {
	t.Setenv("DISTRO_VERSION", "9.9.9-test")

	version, err := DistroVersionString(context.Background(), nil)
	if err != nil {
		t.Fatalf("DistroVersionString returned error: %v", err)
	}
	if version != "9.9.9-test" {
		t.Fatalf("DistroVersionString = %q, want %q", version, "9.9.9-test")
	}
}

func TestDistroVersionStringNilConfigWithoutEnvReturnsError(t *testing.T) {
	oldValue, hadOldValue := os.LookupEnv("DISTRO_VERSION")
	if err := os.Unsetenv("DISTRO_VERSION"); err != nil {
		t.Fatalf("failed to unset DISTRO_VERSION: %v", err)
	}
	t.Cleanup(func() {
		if hadOldValue {
			_ = os.Setenv("DISTRO_VERSION", oldValue)
		} else {
			_ = os.Unsetenv("DISTRO_VERSION")
		}
	})

	_, err := DistroVersionString(context.Background(), nil)
	if err == nil {
		t.Fatal("DistroVersionString returned nil error, want non-nil")
	}
	if !strings.Contains(err.Error(), "config is required") {
		t.Fatalf("DistroVersionString error = %q, want message containing %q", err.Error(), "config is required")
	}
}

func TestParseUninitializedSubmodules(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{
			"all initialized",
			" abc123 sources/poky (heads/main)\n abc124 sources/meta-rust (heads/main)",
			nil,
		},
		{
			"mixed prefixes",
			"-abc sources/a\n+def sources/b (heads/x)\n ghi sources/c (heads/main)",
			[]string{"sources/a"},
		},
		{
			"multiple uninitialized",
			"-a sources/x\n-b sources/y\n-c sources/z",
			[]string{"sources/x", "sources/y", "sources/z"},
		},
		{
			"nested path under initialized parent",
			" a sources/poky (heads/main)\n-b sources/poky/meta-something",
			[]string{"sources/poky/meta-something"},
		},
		{"trailing newline", "-a sources/x\n", []string{"sources/x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseUninitializedSubmodules(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseUninitializedSubmodules(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestGitVersionAndDistroPrecedence(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DISTRO_VERSION", "")
	if err := os.Unsetenv("DISTRO_VERSION"); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	if err := os.WriteFile("tracked", []byte("clean"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "tracked")
	git("-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "-qm", "initial")
	full, short := git("rev-parse", "HEAD"), git("rev-parse", "--short", "HEAD")
	ctx := context.Background()
	cfg := &config.Config{Version: config.Version{TagPrefix: "distro-"}}
	for _, dirty := range []bool{false, true} {
		suffix := ""
		if dirty {
			if err := os.WriteFile("tracked", []byte("dirty"), 0600); err != nil {
				t.Fatal(err)
			}
			suffix = "-dirty"
		}
		for abbreviated, want := range map[bool]string{false: full + suffix, true: short + suffix} {
			got, err := gitVersion(ctx, abbreviated)
			if err != nil || got != want {
				t.Fatalf("gitVersion(short=%t) = %q, %v; want %q", abbreviated, got, err, want)
			}
		}
		if got, err := DistroVersionString(ctx, cfg); err != nil || got != short+suffix {
			t.Fatalf("fallback = %q, %v; want %q", got, err, short+suffix)
		}
	}
	git("-c", "tag.gpgsign=false", "tag", "distro-1.2.3")
	if got, err := DistroVersionString(ctx, cfg); err != nil || got != "1.2.3" {
		t.Fatalf("tagged dirty version = %q, %v; want 1.2.3", got, err)
	}
	t.Setenv("DISTRO_VERSION", "")
	if got, err := DistroVersionString(ctx, cfg); err != nil || got != "" {
		t.Fatalf("empty override = %q, %v; want empty", got, err)
	}
}

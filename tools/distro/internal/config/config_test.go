package config

import "testing"

func TestArtifactRootDir_Default(t *testing.T) {
	cfg := &Config{}
	got := ArtifactRootDir(cfg, "/tmp/work/distro/distro.yaml")
	want := "/tmp/work/distro/artifacts"
	if got != want {
		t.Fatalf("ArtifactRootDir() = %q, want %q", got, want)
	}
}

func TestArtifactRootDir_ConfiguredRelative(t *testing.T) {
	cfg := &Config{
		Artifacts: Artifacts{
			RootDir: "downloads",
		},
	}
	got := ArtifactRootDir(cfg, "/tmp/work/distro/distro.yaml")
	want := "/tmp/work/distro/downloads"
	if got != want {
		t.Fatalf("ArtifactRootDir() = %q, want %q", got, want)
	}
}

func TestArtifactRootDir_ConfiguredAbsolute(t *testing.T) {
	cfg := &Config{
		Artifacts: Artifacts{
			RootDir: "/var/tmp/miner-artifacts",
		},
	}
	got := ArtifactRootDir(cfg, "/tmp/work/distro/distro.yaml")
	want := "/var/tmp/miner-artifacts"
	if got != want {
		t.Fatalf("ArtifactRootDir() = %q, want %q", got, want)
	}
}

func TestResolvePath_Relative(t *testing.T) {
	got := ResolvePath("/tmp/work/distro/distro.yaml", "../python")
	want := "/tmp/work/python"
	if got != want {
		t.Fatalf("ResolvePath() = %q, want %q", got, want)
	}
}

func TestResolvePath_Absolute(t *testing.T) {
	got := ResolvePath("/tmp/work/distro/distro.yaml", "/var/data")
	want := "/var/data"
	if got != want {
		t.Fatalf("ResolvePath() = %q, want %q", got, want)
	}
}

func TestFetchSourceByName(t *testing.T) {
	cfg := &Config{
		Fetch: Fetch{
			Sources: []FetchSource{
				{Name: "proto-apps", Repo: "block/miner-firmware"},
			},
		},
	}

	src, ok := cfg.FetchSourceByName("proto-apps")
	if !ok {
		t.Fatalf("FetchSourceByName() did not find expected source")
	}
	if src.Repo != "block/miner-firmware" {
		t.Fatalf("FetchSourceByName() repo = %q", src.Repo)
	}
}

func TestInjectionSourceByName(t *testing.T) {
	cfg := &Config{
		Injection: Injection{
			Sources: []InjectionSource{
				{Name: "proto-apps", Tag: "latest", Packages: []string{"mcdd"}},
			},
		},
	}

	src, ok := cfg.InjectionSourceByName("proto-apps")
	if !ok {
		t.Fatalf("InjectionSourceByName() did not find expected source")
	}
	if src.Tag != "latest" {
		t.Fatalf("InjectionSourceByName() returned unexpected source: %+v", src)
	}
}

func TestInjectionSourceForPackage(t *testing.T) {
	cfg := &Config{
		Injection: Injection{
			Sources: []InjectionSource{
				{Name: "proto-apps", Packages: []string{"mcdd", "miner-api-server"}},
			},
		},
	}

	src, err := cfg.InjectionSourceForPackage("mcdd")
	if err != nil {
		t.Fatalf("InjectionSourceForPackage() error = %v", err)
	}
	if src.Name != "proto-apps" {
		t.Fatalf("InjectionSourceForPackage() source = %q, want %q", src.Name, "proto-apps")
	}
}

func TestInjectionSourceForPackageDuplicate(t *testing.T) {
	cfg := &Config{
		Injection: Injection{
			Sources: []InjectionSource{
				{Name: "source-a", Packages: []string{"mcdd"}},
				{Name: "source-b", Packages: []string{"mcdd"}},
			},
		},
	}

	if _, err := cfg.InjectionSourceForPackage("mcdd"); err == nil {
		t.Fatalf("expected duplicate mapping error")
	}
}

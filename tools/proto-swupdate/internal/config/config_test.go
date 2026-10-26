package config

import "testing"

func TestDefaultUsesStaticSWUpdateConfig(t *testing.T) {
	got := Default().SWUpdateCfg
	want := "/etc/swupdate.cfg"
	if got != want {
		t.Fatalf("SWUpdateCfg = %q, want %q", got, want)
	}
}

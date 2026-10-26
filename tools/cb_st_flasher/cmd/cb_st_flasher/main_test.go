package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestVerityFlags(t *testing.T) {
	if os.Getenv("CB_ST_FLASHER_VERITY_FLAGS_TEST") == "1" {
		os.Args = append([]string{"cb_st_flasher"}, os.Args[slices.Index(os.Args, "--")+1:]...)
		main()
		return
	}
	image := filepath.Join(t.TempDir(), "missing-image") // Stop before USB access.
	for _, tc := range []struct {
		name     string
		flags    []string
		conflict bool
	}{
		{name: "default"},
		{name: "explicit", flags: []string{"--verity"}},
		{name: "opt-out", flags: []string{"--no-verity"}},
		{name: "false", flags: []string{"--verity=false"}},
		{name: "conflict", flags: []string{"--verity", "--no-verity"}, conflict: true},
		{name: "reverse-conflict", flags: []string{"--no-verity", "--verity"}, conflict: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"-test.run=^TestVerityFlags$", "--"}, tc.flags...)
			cmd := exec.Command(os.Args[0], append(args, image)...)
			cmd.Env = append(os.Environ(), "CB_ST_FLASHER_VERITY_FLAGS_TEST=1")
			output, err := cmd.CombinedOutput()
			if err == nil || cmd.ProcessState.ExitCode() != 1 {
				t.Fatalf("expected preflight failure, got %v: %s", err, output)
			}
			if tc.conflict {
				if strings.Contains(string(output), image) || !strings.Contains(string(output), "--verity") || !strings.Contains(string(output), "--no-verity") {
					t.Fatalf("expected conflicting flags rejected before image access: %s", output)
				}
			} else if !strings.Contains(string(output), image) {
				t.Fatalf("expected accepted flags to reach missing-image preflight: %s", output)
			}
		})
	}
}

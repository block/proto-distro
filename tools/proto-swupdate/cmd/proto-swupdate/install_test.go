package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/urfave/cli/v2"

	"proto-distro/tools/proto-swupdate/internal/config"
	"proto-distro/tools/proto-swupdate/internal/swupdate"
)

func TestRunInstallResetsBootTarget(t *testing.T) {
	rootfsFailed := fmt.Errorf("%w (stable,copy2): exit status 1", swupdate.ErrRootfsInstall)
	type env = map[string]string
	tests := []struct {
		name        string
		running     string
		rootpart    string
		ustate      string
		noRootfs    bool
		hasErr      error
		failWrite   int // 1-based env write that fails; 0: none
		swupdate    env // env changes the install makes
		installErr  error
		wantWrites  []env
		wantInstall bool
		wantExit    int // 0: no error; -1: plain error; >0: exit code
		wantIs      error
	}{
		{name: "first install", running: "7", rootpart: "7", ustate: "0", wantInstall: true},
		{name: "second install before reboot", running: "7", rootpart: "8", ustate: "1",
			wantWrites: []env{{"rootpart": "7", "ustate": "0"}}, wantInstall: true},
		{name: "second install from slot B", running: "8", rootpart: "7", ustate: "1",
			wantWrites: []env{{"rootpart": "8", "ustate": "0"}}, wantInstall: true},
		{name: "probation", running: "8", rootpart: "8", ustate: "1", wantExit: exitUpdatePending},
		{name: "interrupted install leftover", running: "7", rootpart: "7", ustate: "7",
			wantWrites: []env{{"ustate": "0"}}, wantInstall: true},
		{name: "interrupted second install leftover", running: "7", rootpart: "8", ustate: "7",
			wantWrites: []env{{"rootpart": "7", "ustate": "0"}}, wantInstall: true},
		{name: "rollback leftover", running: "7", rootpart: "7", ustate: "3",
			wantWrites: []env{{"ustate": "0"}}, wantInstall: true},
		{name: "unset ustate", running: "7", rootpart: "7", ustate: "",
			wantWrites: []env{{"ustate": "0"}}, wantInstall: true},
		{name: "env write failure", running: "7", rootpart: "8", ustate: "1", failWrite: 1,
			wantWrites: []env{{"rootpart": "7", "ustate": "0"}}, wantExit: -1},
		{name: "running slot unknown", running: "9", rootpart: "7", ustate: "0", wantExit: -1},
		{name: "SWU unreadable", running: "7", rootpart: "8", ustate: "1", hasErr: errors.New("bad cpio"), wantExit: -1},

		{name: "FIP-only SWU in probation", running: "8", rootpart: "8", ustate: "1", noRootfs: true, wantInstall: true},
		{name: "FIP-only SWU keeps pending rootfs update", running: "7", rootpart: "8", ustate: "1", noRootfs: true, wantInstall: true},

		{name: "failed rootfs install", running: "7", rootpart: "7", ustate: "0",
			swupdate: env{"ustate": "3"}, installErr: rootfsFailed,
			wantWrites: []env{{"ustate": "0"}}, wantInstall: true, wantExit: -1, wantIs: swupdate.ErrRootfsInstall},
		{name: "failed rootfs install after rootpart switch", running: "7", rootpart: "8", ustate: "1",
			swupdate: env{"rootpart": "8", "ustate": "3"}, installErr: rootfsFailed,
			wantWrites:  []env{{"rootpart": "7", "ustate": "0"}, {"rootpart": "7", "ustate": "0"}},
			wantInstall: true, wantExit: -1, wantIs: swupdate.ErrRootfsInstall},
		{name: "cleanup write failure", running: "7", rootpart: "7", ustate: "0", failWrite: 1,
			swupdate: env{"ustate": "7"}, installErr: rootfsFailed,
			wantWrites: []env{{"ustate": "0"}}, wantInstall: true, wantExit: -1, wantIs: swupdate.ErrRootfsInstall},
		{name: "FIP failure keeps rootfs update", running: "7", rootpart: "7", ustate: "0",
			swupdate: env{"rootpart": "8", "ustate": "1"}, installErr: errors.New("fip install (fip,bank_1): exit status 1"),
			wantInstall: true, wantExit: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			envVals := env{"a_partnum": "7", "b_partnum": "8", "rootpart": tt.rootpart, "ustate": tt.ustate}
			var writes []env
			cleared, installed := false, false
			deps := installEnv{
				inspectSWU: func(path string, options swupdate.InstallOptions) (bool, bool, error) {
					if path != "/data/update.swu" {
						t.Fatalf("inspected %q", path)
					}
					return !tt.noRootfs, false, tt.hasErr
				},
				activePartition: func() (string, error) { return tt.running, nil },
				getEnv: func(name string) (string, error) {
					v, ok := envVals[name]
					if !ok {
						t.Fatalf("read unexpected variable %q", name)
					}
					return v, nil
				},
				setEnvs: func(vars map[string]string) error {
					writes = append(writes, maps.Clone(vars))
					if len(writes) == tt.failWrite {
						return errors.New("store failed")
					}
					maps.Copy(envVals, vars)
					return nil
				},
				clearBootcount: func(addr uint32) error {
					if addr != cfg.BootcountAddr {
						t.Fatalf("cleared bootcount at %#x, want %#x", addr, cfg.BootcountAddr)
					}
					cleared = true
					return nil
				},
			}
			install := func(config.Config, string, swupdate.InstallOptions) error {
				if !tt.noRootfs && !cleared {
					t.Fatal("rootfs install started before bootcount was cleared")
				}
				installed = true
				maps.Copy(envVals, tt.swupdate)
				return tt.installErr
			}

			err := runInstall(cfg, "/data/update.swu", swupdate.InstallOptions{}, deps, install)

			var exitCoder cli.ExitCoder
			switch {
			case tt.wantExit == 0 && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantExit == -1 && (err == nil || errors.As(err, &exitCoder)):
				t.Fatalf("error = %v, want plain error", err)
			case tt.wantExit > 0 && (!errors.As(err, &exitCoder) || exitCoder.ExitCode() != tt.wantExit):
				t.Fatalf("error = %v, want exit code %d", err, tt.wantExit)
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Fatalf("error = %v, want %v", err, tt.wantIs)
			}
			if !slices.EqualFunc(writes, tt.wantWrites, maps.Equal) {
				t.Fatalf("env writes = %v, want %v", writes, tt.wantWrites)
			}
			if installed != tt.wantInstall {
				t.Fatalf("install ran = %v, want %v", installed, tt.wantInstall)
			}
		})
	}
}

func writeInstallSWU(t *testing.T, payloads ...string) string {
	t.Helper()
	var data strings.Builder
	for _, name := range append(payloads, "TRAILER!!!") {
		header := "070701" + strings.Repeat("00000000", 11) + fmt.Sprintf("%08x", len(name)+1) + "00000000"
		entry := header + name + "\x00"
		data.WriteString(entry)
		data.WriteString(strings.Repeat("\x00", (4-len(entry)%4)%4))
	}
	path := filepath.Join(t.TempDir(), "update.swu")
	if err := os.WriteFile(path, []byte(data.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunInstallFIPOnlyBypassesRootfs(t *testing.T) {
	path := writeInstallSWU(t, "test.rootfs.ext4.gz", "fip-ota.bin")
	options := swupdate.InstallOptions{FIP: true}
	installed := false
	// Nil rootfs accessors fail immediately if the wrapper touches boot state.
	env := installEnv{inspectSWU: swupdate.InspectComponents}
	install := func(_ config.Config, imagePath string, got swupdate.InstallOptions) error {
		if imagePath != path || got != options {
			t.Fatalf("install got %q, %+v; want %q, %+v", imagePath, got, path, options)
		}
		installed = true
		return nil
	}
	if err := runInstall(config.Default(), path, options, env, install); err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Fatal("FIP-only install did not run")
	}
}

func TestInstallCmdSelectors(t *testing.T) {
	tests := []struct {
		name      string
		payloads  []string
		flags     []string
		wantError string
		pending   bool
	}{
		{name: "FIP-only paired SWU bypasses rootfs", payloads: []string{"test.rootfs.ext4.gz", "fip-ota.bin"}, flags: []string{"--fip"}, wantError: "read hw version:"},
		{name: "missing rootfs", payloads: []string{"fip-ota.bin"}, flags: []string{"--rootfs"}, wantError: "SWU has no requested rootfs image"},
		{name: "missing FIP", payloads: []string{"test.rootfs.ext4.gz"}, flags: []string{"--fip"}, wantError: "SWU has no requested FIP image"},
		{name: "both missing rootfs", payloads: []string{"fip-ota.bin"}, flags: []string{"--rootfs", "--fip"}, wantError: "SWU has no requested rootfs image"},
		{name: "both missing FIP before boot-target writes", payloads: []string{"test.rootfs.ext4.gz"}, flags: []string{"--rootfs", "--fip"}, wantError: "SWU has no requested FIP image"},
		{name: "rootfs retains pending guard", payloads: []string{"test.rootfs.ext4.gz", "fip-ota.bin"}, flags: []string{"--rootfs"}, pending: true},
		{name: "both retain pending guard", payloads: []string{"test.rootfs.ext4.gz", "fip-ota.bin"}, flags: []string{"--rootfs", "--fip"}, pending: true},
		{name: "default retains pending guard", payloads: []string{"test.rootfs.ext4.gz", "fip-ota.bin"}, pending: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			// Stop the real installer before any hardware access.
			cfg.OSReleaseFile = filepath.Join(t.TempDir(), "missing-os-release")
			original := deviceEnv
			t.Cleanup(func() { deviceEnv = original })
			deviceEnv = installEnv{inspectSWU: swupdate.InspectComponents}
			if tt.pending {
				deviceEnv.activePartition = func() (string, error) { return "8", nil }
				deviceEnv.getEnv = func(name string) (string, error) {
					return map[string]string{"a_partnum": "7", "b_partnum": "8", "rootpart": "8", "ustate": "1"}[name], nil
				}
			}
			app := &cli.App{Commands: []*cli.Command{installCmd(&cfg)}, ExitErrHandler: func(*cli.Context, error) {}}
			args := append([]string{"proto-swupdate", "install"}, tt.flags...)
			args = append(args, writeInstallSWU(t, tt.payloads...))
			err := app.Run(args)
			if tt.pending {
				var exitCoder cli.ExitCoder
				if !errors.As(err, &exitCoder) || exitCoder.ExitCode() != exitUpdatePending {
					t.Fatalf("error = %v, want pending exit %d", err, exitUpdatePending)
				}
			} else if err == nil || !strings.HasPrefix(err.Error(), tt.wantError) {
				t.Fatalf("error = %v, want prefix %q", err, tt.wantError)
			}
		})
	}
}

package swupdate

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"proto-distro/tools/proto-swupdate/internal/config"
)

// Launch mode exercises the real launcher and shell hook without hardware access.
func TestMain(m *testing.M) {
	if os.Getenv("PROTO_SWUPDATE_TEST_MODE") != "launch" {
		os.Exit(m.Run())
	}
	if len(os.Args) == 3 && os.Args[1] == "fip" && os.Args[2] == "post-install" {
		if err := os.WriteFile(os.Getenv("PROTO_SWUPDATE_TEST_RESULT"), []byte("current"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	cfg := config.Default()
	cfg.SWUpdateBinary = os.Getenv("PROTO_SWUPDATE_TEST_NATIVE")
	parentPath, parentPathSet := os.LookupEnv("PATH")
	parentSuppression, parentSuppressionSet := os.LookupEnv("PROTO_SWUPDATE_SKIP_FIP")
	selection := os.Getenv("PROTO_SWUPDATE_TEST_SELECTION")
	if selection == "" {
		selection = "fip,bank_1"
	}
	err := runSWUpdate(cfg, "c3-p1:1.0", selection, "/unused/update with spaces.swu")
	if after, set := os.LookupEnv("PROTO_SWUPDATE_SKIP_FIP"); after != parentSuppression || set != parentSuppressionSet {
		fmt.Fprintln(os.Stderr, "runSWUpdate changed parent suppression")
		os.Exit(1)
	}
	if after, set := os.LookupEnv("PATH"); after != parentPath || set != parentPathSet {
		fmt.Fprintln(os.Stderr, "runSWUpdate changed parent PATH")
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		os.Exit(1)
	}
	os.Exit(0)
}

func TestRunSWUpdateUsesRunningBinary(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	copiedDir := filepath.Join(dir, "copied updater")
	staleDir := filepath.Join(dir, "stale")
	workDir := filepath.Join(dir, "different working directory")
	for _, path := range []string{copiedDir, staleDir, workDir} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	copied := filepath.Join(copiedDir, "proto-swupdate")
	if err := os.WriteFile(copied, binary, 0o700); err != nil {
		t.Fatal(err)
	}
	copiedInfo, err := os.Stat(copied)
	if err != nil {
		t.Fatal(err)
	}
	stale := "#!/bin/sh\nprintf stale > \"$PROTO_SWUPDATE_TEST_RESULT\"\nexit 42\n"
	for _, path := range []string{filepath.Join(staleDir, "proto-swupdate"), filepath.Join(workDir, "proto-swupdate")} {
		if err := os.WriteFile(path, []byte(stale), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	native := filepath.Join(dir, "native-swupdate")
	if err := os.WriteFile(native, []byte(`#!/bin/sh
set -e
printf %s "$PATH" > "$PROTO_SWUPDATE_TEST_PATH"
printf %s "$PROTO_SWUPDATE_TEST_UNRELATED" > "$PROTO_SWUPDATE_TEST_ENV"
command -v proto-swupdate > "$PROTO_SWUPDATE_TEST_RESOLVED"
[ "$PROTO_SWUPDATE_SKIP_FIP" = 1 ]
printf %s "$PROTO_SWUPDATE_SKIP_FIP" > "$PROTO_SWUPDATE_TEST_SUPPRESSION"
while [ "$#" -gt 0 ]; do
    if [ "$1" = -e ]; then
        case "$2" in fip,*) /bin/sh "$PROTO_SWUPDATE_TEST_HOOK" ;; esac
        break
    fi
    shift
done
exit "$PROTO_SWUPDATE_TEST_EXIT"
`), 0o700); err != nil {
		t.Fatal(err)
	}
	hook, err := filepath.Abs("../../../../sources/meta-miner/recipes-core/images/files/fip-post-install.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, selection := range []string{"fip,bank_1", "stable,copy1"} {
		for _, tc := range []struct {
			name           string
			path           string
			unset          bool
			exit           int
			suppression    string
			suppressionSet bool
		}{
			{name: "missing", path: dir},
			{name: "stale", path: staleDir + string(os.PathListSeparator) + dir},
			{name: "empty"},
			{name: "unset", unset: true},
			{name: "native failure", path: staleDir, exit: 37},
			{name: "suppression empty", path: staleDir, suppressionSet: true},
			{name: "suppression zero", path: staleDir, suppression: "0", suppressionSet: true},
			{name: "suppression one", path: staleDir, suppression: "1", suppressionSet: true},
			{name: "suppression other", path: staleDir, suppression: "inherited", suppressionSet: true},
		} {
			t.Run(selection+"/"+tc.name, func(t *testing.T) {
				resultDir := t.TempDir()
				parentPath, parentPathSet := os.LookupEnv("PATH")
				parentSuppression, parentSuppressionSet := os.LookupEnv("PROTO_SWUPDATE_SKIP_FIP")
				cmd := exec.Command(copied)
				cmd.Dir = workDir
				for _, entry := range cmd.Environ() {
					if !strings.HasPrefix(entry, "PATH=") && !strings.HasPrefix(entry, "PROTO_SWUPDATE_SKIP_FIP=") && !strings.HasPrefix(entry, "PROTO_SWUPDATE_TEST_") {
						cmd.Env = append(cmd.Env, entry)
					}
				}
				if !tc.unset {
					cmd.Env = append(cmd.Env, "PATH="+tc.path)
				}
				if tc.suppressionSet {
					cmd.Env = append(cmd.Env, "PROTO_SWUPDATE_SKIP_FIP="+tc.suppression)
				}
				cmd.Env = append(cmd.Env,
					"PROTO_SWUPDATE_TEST_MODE=launch",
					"PROTO_SWUPDATE_TEST_SELECTION="+selection,
					"PROTO_SWUPDATE_TEST_SUPPRESSION="+filepath.Join(resultDir, "suppression"),
					"PROTO_SWUPDATE_TEST_NATIVE="+native,
					"PROTO_SWUPDATE_TEST_HOOK="+hook,
					"PROTO_SWUPDATE_TEST_RESULT="+filepath.Join(resultDir, "result"),
					"PROTO_SWUPDATE_TEST_RESOLVED="+filepath.Join(resultDir, "resolved"),
					"PROTO_SWUPDATE_TEST_PATH="+filepath.Join(resultDir, "path"),
					"PROTO_SWUPDATE_TEST_ENV="+filepath.Join(resultDir, "environment"),
					"PROTO_SWUPDATE_TEST_UNRELATED=preserved value",
					fmt.Sprintf("PROTO_SWUPDATE_TEST_EXIT=%d", tc.exit),
				)
				output, err := cmd.CombinedOutput()
				status := 0
				if err != nil {
					var exitErr *exec.ExitError
					if !errors.As(err, &exitErr) {
						t.Fatalf("launch: %v\n%s", err, output)
					}
					status = exitErr.ExitCode()
				}
				if status != tc.exit {
					t.Fatalf("exit status = %d, want %d\n%s", status, tc.exit, output)
				}
				readResult := func(name string) string {
					t.Helper()
					data, err := os.ReadFile(filepath.Join(resultDir, name))
					if err != nil {
						t.Fatal(err)
					}
					return string(data)
				}
				resolvedInfo, err := os.Stat(strings.TrimSpace(readResult("resolved")))
				if err != nil {
					t.Fatal(err)
				}
				if !os.SameFile(copiedInfo, resolvedInfo) {
					t.Fatal("hook resolved a different proto-swupdate")
				}
				if got := readResult("suppression"); got != "1" {
					t.Fatalf("child suppression = %q, want 1", got)
				}
				if !strings.HasPrefix(selection, "fip,") {
					if _, err := os.Stat(filepath.Join(resultDir, "result")); !os.IsNotExist(err) {
						t.Fatalf("rootfs invoked FIP hook: %v", err)
					}
				} else if got := readResult("result"); got != "current" {
					t.Fatalf("hook marker = %q, want current", got)
				}
				// Allow host path canonicalization, but no extra or empty PATH entries.
				childPath := readResult("path")
				parts := strings.SplitN(childPath, string(os.PathListSeparator), 2)
				info, err := os.Stat(parts[0])
				copiedDirInfo, statErr := os.Stat(copiedDir)
				if err != nil || statErr != nil || !os.SameFile(info, copiedDirInfo) {
					t.Fatalf("child PATH = %q, want copied directory first", childPath)
				}
				if (tc.path == "" && len(parts) != 1) || (tc.path != "" && (len(parts) != 2 || parts[1] != tc.path)) {
					t.Fatalf("child PATH suffix = %q, want %q", childPath, tc.path)
				}
				if got := readResult("environment"); got != "preserved value" {
					t.Fatalf("unrelated environment = %q", got)
				}
				if after, set := os.LookupEnv("PATH"); after != parentPath || set != parentPathSet {
					t.Fatal("parent PATH changed")
				}
				if after, set := os.LookupEnv("PROTO_SWUPDATE_SKIP_FIP"); after != parentSuppression || set != parentSuppressionSet {
					t.Fatal("parent suppression changed")
				}
			})
		}
	}
}

func TestReadHWVersion(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
		wantErr bool
	}{
		{
			name: "quoted machine",
			content: `NAME="Proto Embedded Linux Distribution"
VERSION="ea1828ee4a-dirty"
MACHINE="c3-p1"
VARIANT="release"
`,
			want: "c3-p1",
		},
		{
			name:    "unquoted machine",
			content: "MACHINE=c3-p1\n",
			want:    "c3-p1",
		},
		{
			name:    "single-quoted machine",
			content: "MACHINE='c3-p1'\n",
			want:    "c3-p1",
		},
		{
			name:    "missing machine",
			content: `NAME="foo"` + "\n",
			wantErr: true,
		},
		{
			name:    "empty machine",
			content: `MACHINE=""` + "\n",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "os-release")
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := readHWVersion(path)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFIPSelectionRejectsInvalidBank(t *testing.T) {
	for bank, want := range map[uint32]string{0: "fip,bank_1", 1: "fip,bank_0"} {
		for i := 0; i < 2; i++ {
			got, err := fipSelectionForBank(bank)
			if err != nil || got != want {
				t.Fatalf("bank %d: %q, %v", bank, got, err)
			}
		}
	}
	for _, bank := range []uint32{2, 15, ^uint32(0)} {
		got, err := fipSelectionForBank(bank)
		if err == nil || got != "" {
			t.Fatalf("invalid bank %d selected %q: %v", bank, got, err)
		}
	}
}

func TestInstallPreflightsRequestedPayloads(t *testing.T) {
	// Missing requests fail before reading hardware or discovering devices.
	entry := func(name string) string {
		header := "070701" + strings.Repeat("00000000", 11) + fmt.Sprintf("%08x", len(name)+1) + "00000000"
		data := header + name + "\x00"
		return data + strings.Repeat("\x00", (4-len(data)%4)%4)
	}
	for _, payload := range []string{"", "test.rootfs.ext4.gz", "fip-ota.bin"} {
		for _, options := range []InstallOptions{{Rootfs: true}, {FIP: true}, {Rootfs: true, FIP: true}} {
			if payload == "test.rootfs.ext4.gz" && !options.FIP || payload == "fip-ota.bin" && !options.Rootfs {
				continue
			}
			path := filepath.Join(t.TempDir(), "missing-component.swu")
			data := entry("TRAILER!!!")
			if payload != "" {
				data = entry(payload) + data
			}
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			cfg := config.Default()
			cfg.OSReleaseFile = filepath.Join(t.TempDir(), "no-hardware-file")
			err := Install(cfg, path, options)
			var pathErr *os.PathError
			if err == nil || errors.As(err, &pathErr) {
				t.Fatalf("payload %q options %+v did not preflight: %v", payload, options, err)
			}
		}
	}
}

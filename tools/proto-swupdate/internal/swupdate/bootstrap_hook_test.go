package swupdate

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBootstrapHookReusesArchive(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux mount namespaces")
	}
	for _, path := range []string{"/data", "/mnt"} {
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Skipf("requires existing %s mountpoint", path)
		}
	}
	if output, err := exec.Command("/usr/bin/unshare", "--mount", "--propagation", "private", "/bin/true").CombinedOutput(); err != nil {
		t.Skipf("requires mount privileges: %v %s", err, output)
	}
	if _, err := exec.LookPath("cpio"); err != nil {
		t.Skip("requires cpio")
	}
	updater := []byte(`#!/bin/sh
set -eu
[ "$(stat -c '%d:%i' "$3")" = "$SOURCE_ID" ] || { echo 'archive was copied' >&2; exit 91; }
if (printf x >> "$3") 2>/dev/null; then
    echo 'archive is writable' >&2
    exit 92
fi
printf 'executed\n' >> /mnt/executed
exit "$CHILD_EXIT"
`)
	for _, tc := range []struct {
		name, archive string
		status        int
		executes      bool
	}{
		{"ordinary", "update.swu", 0, true},
		{"legacy tmp", "update.swu", 0, true},
		{"spaces", "update with spaces.swu", 0, true},
		{"newlines", "update\nname.swu", 0, true},
		{"missing archive", "update.swu", 0, false},
		{"mismatched manifest", "update.swu", 0, false},
		{"tampered updater", "update.swu", 1, false},
		{"ambiguous", "update.swu", 0, false},
		{"symlink only", "update.swu", 0, false},
		{"discovery failure", "update.swu", 0, false},
		{"suppressed", "update.swu", 0, false},
		{"nested failure", "update.swu", 73, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			for _, name := range []string{"data", "tmp", "payload"} {
				if err := os.Mkdir(filepath.Join(workspace, name), 0700); err != nil {
					t.Fatal(err)
				}
			}
			payload := filepath.Join(workspace, "payload")
			manifest := []byte("active signed manifest\n")
			bootstrapWriteTest(t, filepath.Join(payload, "sw-description"), manifest)
			binary := updater
			if tc.name == "tampered updater" {
				binary = []byte("#!/bin/sh\nexit 0\n")
			}
			bootstrapWriteTest(t, filepath.Join(payload, "proto-swupdate"), binary)
			archive := filepath.Join(workspace, "data", tc.archive)
			if tc.name == "legacy tmp" {
				archive = filepath.Join(workspace, "tmp", tc.archive)
			}
			bootstrapWriteTest(t, archive, bootstrapRunTest(t, payload, []byte("sw-description\nproto-swupdate\n"), "cpio", "-o", "-H", "newc"))
			identity := strings.TrimSpace(string(bootstrapRunTest(t, "", nil, "stat", "-c", "%d:%i", archive)))
			if tc.name == "missing archive" {
				if err := os.Remove(archive); err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "mismatched manifest" {
				manifest = []byte("different active manifest\n")
			}
			if tc.name == "ambiguous" {
				bootstrapWriteTest(t, filepath.Join(workspace, "data", "second.swu"), bootstrapReadTest(t, archive))
			}
			if tc.name == "symlink only" {
				target := filepath.Join(workspace, "outside.swu")
				if err := os.Rename(archive, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("/mnt/outside.swu", archive); err != nil {
					t.Fatal(err)
				}
			}
			bootstrapWriteTest(t, filepath.Join(workspace, "tmp", "sw-description"), manifest)
			bootstrapWriteTest(t, filepath.Join(workspace, "hook.sh"), []byte(renderBootstrapHookTest(t, fmt.Sprintf("%x", sha256.Sum256(updater)))))
			childExit := "0"
			if tc.name == "nested failure" {
				childExit = "73"
			}
			if tc.name == "discovery failure" || tc.name == "suppressed" {
				tools := filepath.Join(workspace, "bin")
				if err := os.Mkdir(tools, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(tools, "find"), []byte("#!/bin/sh\n: > /mnt/discovery\nexit 1\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("/usr/bin/unshare", "--mount", "--propagation", "private", "/bin/sh", "-ec", `
/bin/mount --bind "$1/data" /data
/bin/mount --bind "$1" /mnt
/bin/mount --bind /mnt/tmp /tmp
exec /bin/sh /mnt/hook.sh
`, "bootstrap-test", workspace)
			cmd.Env = append(os.Environ(), "TMPDIR=/tmp", "PROTO_SWUPDATE_SKIP_FIP=0", "SOURCE_ID="+identity, "CHILD_EXIT="+childExit)
			if tc.name == "discovery failure" || tc.name == "suppressed" {
				cmd.Env = append(cmd.Env, "PATH=/mnt/bin:"+os.Getenv("PATH"))
			}
			if tc.name == "suppressed" {
				cmd.Env = append(cmd.Env, "PROTO_SWUPDATE_SKIP_FIP=1", "TMPDIR=/does-not-exist")
			}
			output, err := cmd.CombinedOutput()
			status := 0
			if err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					status = exit.ExitCode()
				} else {
					t.Fatal(err)
				}
			}
			if status != tc.status {
				t.Fatalf("exit %d, want %d: %s", status, tc.status, output)
			}
			_, executed := os.Stat(filepath.Join(workspace, "executed"))
			if (executed == nil) != tc.executes {
				t.Fatalf("unexpected updater execution: %v %s", executed, output)
			}
			if tc.executes && string(bootstrapReadTest(t, filepath.Join(workspace, "executed"))) != "executed\n" {
				t.Fatal("updater did not execute exactly once")
			}
			if tc.status == 0 && !tc.executes && tc.name != "suppressed" && !strings.Contains(string(output), "FIP bootstrap:") {
				t.Fatalf("missing skip diagnostic: %s", output)
			}
			if tc.name == "discovery failure" {
				if _, err := os.Stat(filepath.Join(workspace, "discovery")); err != nil {
					t.Fatalf("failing find was not invoked: %v", err)
				}
			}
			if tc.name == "suppressed" {
				if _, err := os.Stat(filepath.Join(workspace, "discovery")); !os.IsNotExist(err) {
					t.Fatalf("suppression invoked discovery: %v", err)
				}
				if len(output) != 0 {
					t.Fatalf("suppression produced output: %s", output)
				}
			}
			if string(bootstrapReadTest(t, filepath.Join(workspace, "tmp", "sw-description"))) != string(manifest) {
				t.Fatal("parent manifest changed")
			}
			entries, err := os.ReadDir(filepath.Join(workspace, "tmp"))
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Name() != "sw-description" && !(tc.name == "legacy tmp" && entry.Name() == tc.archive) {
					t.Fatalf("hook left staging files: %s", entry.Name())
				}
			}
		})
	}
}

func TestBootstrapHookCleansFailedWorkspace(t *testing.T) {
	mktemp, err := exec.LookPath("mktemp")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	tools := t.TempDir()
	// Relocate only mktemp for isolation; execute the real utility and hook.
	wrapper := filepath.Join(tools, "mktemp")
	bootstrapWriteTest(t, wrapper, []byte(fmt.Sprintf("#!/bin/sh\nexec %q -d %q\n", mktemp, filepath.Join(workspace, "stage.XXXXXX"))))
	if err := os.Chmod(wrapper, 0755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", renderBootstrapHookTest(t, strings.Repeat("0", 64)))
	cmd.Env = append(os.Environ(), "PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"),
		"PROTO_SWUPDATE_SKIP_FIP=0", "TMPDIR="+filepath.Join(t.TempDir(), "missing"))
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("missing active manifest succeeded: %s", output)
	}
	entries, err := os.ReadDir(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed hook left staging files: %v", entries)
	}
}

func bootstrapWriteTest(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func bootstrapReadTest(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func bootstrapRunTest(t *testing.T, dir string, stdin []byte, name string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, stderr.String())
	}
	return out
}

func renderBootstrapHookTest(t *testing.T, updaterHash string) string {
	t.Helper()
	template := string(bootstrapReadTest(t, filepath.Join("..", "..", "..", "..", "sources", "meta-miner", "recipes-core", "images", "files", "bootstrap-hook.sh")))
	if strings.Count(template, "@UPDATER_SHA256@") != 1 {
		t.Fatal("bootstrap template must contain exactly one updater hash token")
	}
	return strings.Replace(template, "@UPDATER_SHA256@", updaterHash, 1)
}

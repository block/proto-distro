package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/urfave/cli/v2"
	"proto-distro/tools/distro/internal/fileutil"
)

func artifactCommand(command *cli.Command, configPath string, args ...string) (string, error) {
	var report bytes.Buffer
	app := &cli.App{Writer: &report, ErrWriter: &report, Flags: []cli.Flag{&cli.StringFlag{Name: "config", Value: configPath}}, Commands: []*cli.Command{command}}
	err := app.Run(append([]string{"distro", command.Name}, args...))
	return report.String(), err
}

func artifactPut(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		t.Fatal(err)
	}
	commandWrite(t, name, data)
}

type artifactState struct {
	Mode     fs.FileMode
	Size     int64
	Modified time.Time
	Hash     [32]byte
	Link     string
}

func artifactSnapshot(t *testing.T, root string) map[string]artifactState {
	t.Helper()
	result := map[string]artifactState{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		state := artifactState{Mode: info.Mode(), Size: info.Size(), Modified: info.ModTime()}
		if info.Mode().IsRegular() {
			state.Hash = sha256.Sum256(commandRead(t, name))
		}
		if info.Mode()&os.ModeSymlink != 0 {
			state.Link, err = os.Readlink(name)
			if err != nil {
				return err
			}
		}
		result[name] = state
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

const seedRoot = "miner-image-release-c3-p1-1.2.3.rootfs.ext4"

func swuArtifactFixture(t *testing.T, base string) (string, string, string) {
	t.Helper()
	for _, name := range []string{"cpio", "gzip", "openssl"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("requires %s: %v", name, err)
		}
	}
	root := filepath.Join(base, "replacement.rootfs.ext4")
	artifactPut(t, root, bytes.Repeat([]byte{0x51}, 1024))
	artifactPut(t, root+".avb.body", bytes.Repeat([]byte{0x24}, 512))
	artifactPut(t, root+".avb.tail", bytes.Repeat([]byte{0x97}, 512))
	artifactPut(t, filepath.Join(base, "passout"), []byte("test-passphrase\n"))
	commandRun(t, base, "", "openssl", "genpkey", "-algorithm", "RSA", "-pkeyopt", "rsa_keygen_bits:2048", "-aes-256-cbc", "-pass", "file:"+filepath.Join(base, "passout"), "-out", "private.pem")
	commandRun(t, base, "", "openssl", "pkey", "-in", "private.pem", "-passin", "file:"+filepath.Join(base, "passout"), "-pubout", "-out", "public.pem")
	payload := filepath.Join(base, "payload")
	artifactPut(t, filepath.Join(payload, seedRoot+".gz"), commandRun(t, base, "old rootfs", "gzip", "-c"))
	record := func(device string) string {
		return fmt.Sprintf("{ filename = %q; sha256 = %q; device = %q; type = %q; compressed = %q; installed-directly = true; }", seedRoot+".gz", strings.Repeat("0", 64), device, "raw", "zlib")
	}
	script := []byte("#!/bin/sh\nexit 0\n")
	description := fmt.Sprintf(`software = { version = "1.2.3"; hardware-compatibility = ["c3-p1"]; scripts = ({ filename = "post.sh"; type = "shellscript"; sha256 = "%x"; }); stable = { copy1 = { images = (%s); }; copy2 = { images = (%s); }; }; };`, sha256.Sum256(script), record("/dev/mmcblk0p7"), record("/dev/mmcblk0p8"))
	artifactPut(t, filepath.Join(payload, "sw-description"), []byte(description))
	artifactPut(t, filepath.Join(payload, "post.sh"), script)
	artifactPut(t, filepath.Join(payload, "unrelated.bin"), []byte("unrelated payload"))
	seed := filepath.Join(base, "seed.swu")
	artifactPut(t, seed, commandRun(t, payload, "sw-description\n"+seedRoot+".gz\npost.sh\nunrelated.bin\n", "cpio", "-o", "-H", "newc"))
	configPath := filepath.Join(base, "distro.yaml")
	artifactPut(t, configPath, []byte("signing:\n  key_dir: .\n  private_key: private.pem\n  passfile: passout\n"))
	return configPath, root, seed
}

func TestSWURecreateDryRunNoWrites(t *testing.T) {
	base, err := fileutil.ResolveArtifact(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	configPath, root, seed := swuArtifactFixture(t, base)
	fip := filepath.Join(base, "fip-input.bin")
	artifactPut(t, fip, []byte("explicit signed FIP"))
	addSeedFIP(t, base, seed, fip)

	scratch := filepath.Join(base, "scratch")
	if err := os.Mkdir(scratch, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", scratch)
	denied := filepath.Join(base, "denied")
	for _, name := range []string{"gh", "docker", "openssl", "gzip"} {
		artifactPut(t, filepath.Join(denied, name), []byte("#!/bin/sh\necho forbidden >&2\nexit 97\n"))
		if err := os.Chmod(filepath.Join(denied, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", denied+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := os.Symlink(root, filepath.Join(base, "alias")); err != nil {
		t.Fatal(err)
	}
	before := artifactSnapshot(t, base)
	output := filepath.Join(base, "rebuilt.swu")
	report, err := artifactCommand(swuRecreateCommand(), configPath, "--dry", "--swu", seed, "--fip", fip, "-o", output, "--rootfs", root, "--version", "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{root, root + ".avb.body", root + ".avb.tail", fip, seed, filepath.Join(base, "private.pem"), filepath.Join(base, "passout"), configPath} {
		hash := fmt.Sprintf("%x", sha256.Sum256(commandRead(t, input)))
		if !strings.Contains(report, hash) || !strings.Contains(report, input) {
			t.Fatalf("missing input fingerprint %s:\n%s", input, report)
		}
	}
	if !strings.Contains(report, output) {
		t.Fatalf("missing output path %s:\n%s", output, report)
	}
	for _, member := range []string{filepath.Base(fip), "fip-post-install.sh"} {
		if !strings.Contains(report, member) {
			t.Fatalf("missing planned FIP member %s: %s", member, report)
		}
	}
	if !reflect.DeepEqual(before, artifactSnapshot(t, base)) {
		t.Fatal("dry run changed names, bytes, sizes, mtimes or symlinks")
	}
}

func TestSWURecreateRejectsRootfsArguments(t *testing.T) {
	base := t.TempDir()
	configPath, root, seed := swuArtifactFixture(t, base)
	for _, suffix := range []string{"", ".avb.body", ".avb.tail"} {
		artifactPut(t, filepath.Join(base, "rootfs.ext4")+suffix, commandRead(t, root+suffix))
	}
	for _, args := range [][]string{{root}, {"--rootfs", ""}, {"--rootfs", root + ".gz"}, {"--rootfs", filepath.Join(base, "rootfs.ext4")}, {"--rootfs", root, root}} {
		before := artifactSnapshot(t, base)
		if _, err := artifactCommand(swuRecreateCommand(), configPath, append([]string{"--swu", seed, "--version", "1.2.3"}, args...)...); err == nil {
			t.Fatalf("accepted invalid rootfs arguments %q", args)
		}
		if !reflect.DeepEqual(before, artifactSnapshot(t, base)) {
			t.Fatalf("invalid rootfs arguments mutated files: %q", args)
		}
	}
}

func TestSWURecreateVersionContract(t *testing.T) {
	base := t.TempDir()
	configPath, root, seed := swuArtifactFixture(t, base)
	fip := filepath.Join(base, "fip-input.bin")
	artifactPut(t, fip, []byte("firmware"))
	addSeedFIP(t, base, seed, fip)
	for _, args := range [][]string{
		{"--rootfs", root},
		{"--rootfs", root, "--version", ""},
		{"--rootfs", root, "--version", " 2"},
		{"--rootfs", root, "--version", "2\n"},
		{"--fip", fip, "--version", "2"},
	} {
		before := artifactSnapshot(t, base)
		if _, err := artifactCommand(swuRecreateCommand(), configPath, append([]string{"--swu", seed}, args...)...); err == nil {
			t.Fatalf("accepted invalid version arguments %q", args)
		}
		if !reflect.DeepEqual(before, artifactSnapshot(t, base)) {
			t.Fatal("invalid version arguments changed files")
		}
	}
}

func TestSWURecreateUsesCreationTime(t *testing.T) {
	base := t.TempDir()
	configPath, root, seed := swuArtifactFixture(t, base)
	payload := filepath.Join(base, "payload")
	old := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err := filepath.WalkDir(payload, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, old, old)
	}); err != nil {
		t.Fatal(err)
	}
	artifactPut(t, seed, commandRun(t, payload, "sw-description\n"+seedRoot+".gz\npost.sh\nunrelated.bin\n", "cpio", "-o", "-H", "newc"))
	if err := os.Chtimes(seed, old, old); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(base, "rebuilt.swu")
	before := time.Now().Truncate(time.Second)
	if _, err := artifactCommand(swuRecreateCommand(), configPath, "--swu", seed, "-o", output, "--rootfs", root, "--version", "1.2.3"); err != nil {
		t.Fatal(err)
	}
	after := time.Now()
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.ModTime().Before(before) || info.ModTime().After(after) {
		t.Fatalf("SWU timestamp = %s, want creation time between %s and %s", info.ModTime(), before, after)
	}
	extracted := t.TempDir()
	commandRun(t, extracted, string(commandRead(t, output)), "cpio", "-idm")
	if err := filepath.WalkDir(extracted, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || path == extracted {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.ModTime().Before(before) || info.ModTime().After(after) {
			t.Errorf("%s timestamp = %s, want creation time between %s and %s", entry.Name(), info.ModTime(), before, after)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(seed)
	if err != nil || !info.ModTime().Equal(old) {
		t.Fatalf("seed timestamp changed: %v, %v", info, err)
	}
}

func TestSWURecreateUsesReplacementPayloadNames(t *testing.T) {
	base := t.TempDir()
	configPath, root, seed := swuArtifactFixture(t, base)
	checksum := filepath.Join(base, "sha.txt")
	artifactPut(t, checksum, []byte("unchanged checksum sentinel\n"))
	denied := filepath.Join(base, "denied")
	artifactPut(t, filepath.Join(denied, "docker"), []byte("#!/bin/sh\nexit 97\n"))
	if err := os.Chmod(filepath.Join(denied, "docker"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", denied+string(os.PathListSeparator)+os.Getenv("PATH"))
	original := map[string][]byte{}
	for _, name := range []string{root, root + ".avb.body", root + ".avb.tail", seed} {
		original[name] = commandRead(t, name)
	}
	for repeat := range 2 {
		output := filepath.Join(base, fmt.Sprintf("rebuilt-%d.swu", repeat))
		if _, err := artifactCommand(swuRecreateCommand(), configPath, "--swu", seed, "-o", output, "--rootfs", root, "--version", "1.2.3"); err != nil {
			t.Fatal(err)
		}
		extracted := t.TempDir()
		commandRun(t, extracted, string(commandRead(t, output)), "cpio", "-idm")
		if got := commandRun(t, extracted, "", "gzip", "-dc", filepath.Base(root)+".gz"); !bytes.Equal(got, original[root]) {
			t.Fatal("rootfs changed")
		}
		for _, suffix := range []string{".avb.body", ".avb.tail"} {
			if !bytes.Equal(commandRead(t, filepath.Join(extracted, filepath.Base(root)+suffix)), original[root+suffix]) {
				t.Fatal("AVB metadata changed")
			}
		}
		commandRun(t, extracted, "", "openssl", "dgst", "-sha256", "-verify", filepath.Join(base, "public.pem"), "-signature", "sw-description.sig", "sw-description")
		description := string(commandRead(t, filepath.Join(extracted, "sw-description")))
		for _, preserved := range []string{`version = "1.2.3"`, `hardware-compatibility = ["c3-p1"]`} {
			if !strings.Contains(description, preserved) {
				t.Fatalf("metadata lost: %s", preserved)
			}
		}
		records := regexp.MustCompile(`\{\s*filename\s*=\s*"([^"]+)";([^{}]*)\}`).FindAllStringSubmatch(description, -1)
		for _, device := range []string{"/dev/mmcblk0p7", "/dev/mmcblk0p8"} {
			for index, suffix := range []string{".gz", ".avb.body", ".avb.tail"} {
				name := filepath.Base(root) + suffix
				count := 0
				for _, record := range records {
					if record[1] != name || !strings.Contains(record[2], device) {
						continue
					}
					count++
					hash := fmt.Sprintf("%x", sha256.Sum256(commandRead(t, filepath.Join(extracted, name))))
					offset := []string{"0", "1024", "1073741312"}[index]
					if !strings.Contains(record[2], hash) || !strings.Contains(record[2], "offset = \""+offset+"\"") {
						t.Fatalf("wrong image contract: %s", record[0])
					}
				}
				if count != 1 {
					t.Fatalf("%s %s count %d", device, name, count)
				}
			}
		}
		for _, name := range []string{"post.sh", "unrelated.bin"} {
			if !bytes.Equal(commandRead(t, filepath.Join(extracted, name)), commandRead(t, filepath.Join(base, "payload", name))) {
				t.Fatal("unrelated member changed")
			}
		}
		seed = output
	}
	if got := string(commandRead(t, checksum)); got != "unchanged checksum sentinel\n" {
		t.Fatal("standalone recreation changed folder checksums")
	}
	for name, want := range original {
		if !bytes.Equal(commandRead(t, name), want) {
			t.Fatal("source changed")
		}
	}
	for _, failure := range []string{"missing pair", "geometry", "credentials", "existing"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			badRoot := filepath.Join(dir, "replacement.rootfs.ext4")
			for _, suffix := range []string{"", ".avb.body", ".avb.tail"} {
				artifactPut(t, badRoot+suffix, original[root+suffix])
			}
			output := filepath.Join(dir, "out.swu")
			args := []string{"--swu", seed, "-o", output}
			switch failure {
			case "missing pair":
				if err := os.Remove(badRoot + ".avb.tail"); err != nil {
					t.Fatal(err)
				}
			case "geometry":
				commandWrite(t, badRoot+".avb.body", []byte("bad"))
			case "credentials":
				badKey := filepath.Join(dir, "bad.pem")
				commandWrite(t, badKey, []byte("bad key"))
				args = append(args, "--swu-key-file", badKey)
			case "existing":
				commandWrite(t, output, []byte("prior output"))
			}
			before := artifactSnapshot(t, dir)
			if _, err := artifactCommand(swuRecreateCommand(), configPath, append(args, "--rootfs", badRoot, "--version", "1.2.3")...); err == nil {
				t.Fatal("accepted invalid recreation")
			}
			after := artifactSnapshot(t, dir)
			delete(before, dir)
			delete(after, dir)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("failed reconstruction changed inputs or published output")
			}
		})
	}
}

func TestSWURecreateFIPRoundTrip(t *testing.T) {
	base := t.TempDir()
	configPath, root, seed := swuArtifactFixture(t, base)
	// Recreation must ignore missing build artifacts configured for other commands.
	artifactPut(t, configPath, append(commandRead(t, configPath), []byte("\nartifacts:\n  local_files:\n    - dist/proto-swupdate/release/proto-swupdate_linux_arm_7/proto-swupdate\n")...))
	fip := filepath.Join(base, "fip-input.bin")
	want := []byte("explicit production FIP bytes")
	artifactPut(t, fip, want)
	addSeedFIP(t, base, seed, fip)
	for index, include := range []bool{true, true, true} {
		output := filepath.Join(base, fmt.Sprintf("round-%d.swu", index))
		args := []string{"--swu", seed, "-o", output, "--rootfs", root, "--version", "1.2.3"}
		if include {
			args = append(args, "--fip", fip)
		}
		before := artifactSnapshot(t, base)
		dryArgs := append([]string{"--dry"}, args...)
		report, err := artifactCommand(swuRecreateCommand(), configPath, dryArgs...)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, artifactSnapshot(t, base)) {
			t.Fatal("round-trip dry run changed files")
		}
		for _, member := range []string{filepath.Base(fip), "fip-post-install.sh"} {
			if strings.Contains(report, member) != include {
				t.Fatalf("incorrect dry FIP membership: %s", report)
			}
		}
		if _, err := artifactCommand(swuRecreateCommand(), configPath, args...); err != nil {
			t.Fatal(err)
		}
		extracted := t.TempDir()
		commandRun(t, extracted, string(commandRead(t, output)), "cpio", "-idm")
		commandRun(t, extracted, "", "openssl", "dgst", "-sha256", "-verify", filepath.Join(base, "public.pem"), "-signature", "sw-description.sig", "sw-description")
		for _, name := range []string{filepath.Base(fip), "fip-post-install.sh"} {
			_, err := os.Stat(filepath.Join(extracted, name))
			if include && err != nil || !include && !os.IsNotExist(err) {
				t.Fatalf("FIP membership %s include=%t: %v", name, include, err)
			}
		}
		if include {
			if !bytes.Equal(commandRead(t, filepath.Join(extracted, filepath.Base(fip))), want) {
				t.Fatal("FIP bytes changed")
			}
		}
		if got := commandRun(t, extracted, "", "gzip", "-dc", filepath.Base(root)+".gz"); !bytes.Equal(got, commandRead(t, root)) {
			t.Fatal("rootfs changed")
		}
		for _, suffix := range []string{".avb.body", ".avb.tail"} {
			if !bytes.Equal(commandRead(t, filepath.Join(extracted, filepath.Base(root)+suffix)), commandRead(t, root+suffix)) {
				t.Fatal("AVB bytes changed")
			}
		}
		seed = output
	}
	before := artifactSnapshot(t, base)
	if _, err := artifactCommand(swuRecreateCommand(), configPath, "--swu", seed, "--fip", "", "--rootfs", root, "--version", "1.2.3"); err == nil {
		t.Fatal("accepted explicit empty --fip")
	}
	if !reflect.DeepEqual(before, artifactSnapshot(t, base)) {
		t.Fatal("empty --fip mutated files")
	}
}

func addSeedFIP(t *testing.T, base, seed, fip string) {
	t.Helper()
	payload := filepath.Join(base, "payload")
	name := "fip-ota.bin"
	artifactPut(t, filepath.Join(payload, name), commandRead(t, fip))
	hook := []byte("#!/bin/sh\nexit 0\n")
	artifactPut(t, filepath.Join(payload, "fip-post-install.sh"), hook)
	description := string(commandRead(t, filepath.Join(payload, "sw-description")))
	record := fmt.Sprintf(`extra={images=({filename=%q;sha256="%x";type="raw";});scripts=({filename="fip-post-install.sh";sha256="%x";type="postinstall";});};`, name, sha256.Sum256(commandRead(t, fip)), sha256.Sum256(hook))
	description = strings.Replace(description, "stable = {", record+"stable = {", 1)
	artifactPut(t, filepath.Join(payload, "sw-description"), []byte(description))
	artifactPut(t, seed, commandRun(t, payload, "sw-description\n"+seedRoot+".gz\npost.sh\nunrelated.bin\nfip-ota.bin\nfip-post-install.sh\n", "cpio", "-o", "-H", "newc"))
}

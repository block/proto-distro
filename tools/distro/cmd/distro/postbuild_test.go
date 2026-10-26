package main

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v3"
	"proto-distro/tools/distro/internal/config"
	"proto-distro/tools/distro/internal/fileutil"
)

func commandFixture(t *testing.T) (*config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	shim, err := os.ReadFile("../../internal/distroinject/testdata/docker")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docker"), shim, 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"body": bytes.Repeat([]byte{0x37}, 4096),
		"tail": bytes.Repeat([]byte{0x91}, 512),
	} {
		path := filepath.Join(dir, "fixture."+name)
		commandWrite(t, path, data)
		t.Setenv("DISTRO_TEST_"+strings.ToUpper(name), path)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	key := filepath.Join(dir, "avb.key")
	commandWrite(t, key, []byte("c3"))
	cfg := &config.Config{
		Defaults:  config.Defaults{Machine: "c3-p1", Variant: "release"},
		Artifacts: config.Artifacts{RootDir: "artifacts"},
		AVB:       map[string]config.AVBKeys{"c3-p1": {PrivateKey: key, PublicKey: key, AVBPublicKey: key}},
	}
	cfg.Docker.Injector.Image = "test-injector"
	return cfg, filepath.Join(dir, "distro.yaml")
}

func commandWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func commandRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func commandRun(t *testing.T, dir, input, name string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, stderr.String())
	}
	return data
}

func TestRunPostbuildVersionCopy(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("signerFailure=%t", fail), func(t *testing.T) {
			for _, tool := range []string{"cpio", "openssl", "gzip"} {
				if _, err := exec.LookPath(tool); err != nil {
					t.Skipf("%s required: %v", tool, err)
				}
			}
			cfg, configPath := commandFixture(t)
			dir := filepath.Dir(configPath)
			cfg.Signing = config.Signing{KeyDir: dir, PrivateKey: "swu.pem", Passfile: "passout"}
			commandWrite(t, filepath.Join(dir, cfg.Signing.Passfile), []byte("unused\n"))
			commandRun(t, dir, "", "openssl", "genpkey", "-algorithm", "RSA", "-pkeyopt", "rsa_keygen_bits:2048", "-out", cfg.Signing.PrivateKey)
			commandRun(t, dir, "", "openssl", "pkey", "-in", cfg.Signing.PrivateKey, "-pubout", "-out", "public.pem")
			artifacts := filepath.Join(dir, "artifacts")
			oldName := "miner-image-release-c3-p1-1.0.0"
			newName := "miner-image-release-c3-p1-2.0.0"
			oldFolder := filepath.Join(artifacts, oldName)
			newFolder := filepath.Join(artifacts, newName)
			if err := os.MkdirAll(oldFolder, 0755); err != nil {
				t.Fatal(err)
			}
			root := oldName + ".rootfs.ext4"
			plain := bytes.Repeat([]byte("r"), 512)
			commandWrite(t, filepath.Join(oldFolder, root), plain)
			updater := "raw updater"
			cfg.Artifacts.LocalFiles = []string{"local/proto-swupdate"}
			if err := os.Mkdir(filepath.Join(dir, "local"), 0755); err != nil {
				t.Fatal(err)
			}
			commandWrite(t, config.ResolvePath(configPath, cfg.Artifacts.LocalFiles[0]), []byte(updater))
			commandWrite(t, filepath.Join(oldFolder, "proto-swupdate"), []byte("stale artifact updater"))
			for _, suffix := range []string{".avb", ".avb.footer", ".avb.body", ".avb.tail"} {
				commandWrite(t, filepath.Join(oldFolder, root+suffix), []byte("old "+suffix+"\n"))
			}
			commandWrite(t, filepath.Join(oldFolder, "sha.txt"), []byte("old checksums\n"))
			payload := t.TempDir()
			commandWrite(t, filepath.Join(payload, root+".gz"), commandRun(t, dir, "", "gzip", "-c", filepath.Join(oldFolder, root)))
			entry := func(device string) string {
				return fmt.Sprintf("{ filename = %q; sha256 = %q; device = %q; type = %q; compressed = %q; installed-directly = true; }", root+".gz", strings.Repeat("0", 64), device, "raw", "zlib")
			}
			commandWrite(t, filepath.Join(payload, "sw-description"), []byte("software = { stable = { copy1 = { images = ("+entry("/dev/mmcblk0p7")+"); }; copy2 = { images = ("+entry("/dev/mmcblk0p8")+"); }; }; };\n"))
			commandRun(t, payload, "", "openssl", "dgst", "-sha256", "-sign", filepath.Join(dir, cfg.Signing.PrivateKey), "-out", "sw-description.sig", "sw-description")
			if err := os.Mkdir(filepath.Join(oldFolder, "fip"), 0755); err != nil {
				t.Fatal(err)
			}
			commandWrite(t, filepath.Join(oldFolder, "fip", "fip-stm32mp131f-c3p1-optee.bin"), []byte("deployed FIP"))
			swu := commandRun(t, payload, "sw-description\nsw-description.sig\n"+root+".gz\n", "cpio", "-o", "-H", "newc")
			commandWrite(t, filepath.Join(oldFolder, oldName+".swu"), swu)
			writeCommandCombinedSeed(t, payload, oldFolder, oldName+"-with-fip.swu")
			commandWrite(t, filepath.Join(oldFolder, "extra.swu"), swu)
			if err := fileutil.CreateZip(oldFolder, oldFolder+".zip"); err != nil {
				t.Fatal(err)
			}
			oldZIP := commandRead(t, oldFolder+".zip")
			const fipPath = "fip/fip-stm32mp131f-c3p1-optee.bin"
			wantFIP := commandRead(t, filepath.Join(oldFolder, fipPath))
			before := map[string][]byte{}
			entries, err := os.ReadDir(oldFolder)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				before[entry.Name()] = commandRead(t, filepath.Join(oldFolder, entry.Name()))
			}
			links := map[string]string{"miner-image-release-c3-p1-latest": oldName, "miner-image-latest": oldName, "miner-image-latest.zip": oldName + ".zip"}
			for link, target := range links {
				if err := os.Symlink(target, filepath.Join(artifacts, link)); err != nil {
					t.Fatal(err)
				}
			}
			if fail {
				t.Setenv("DISTRO_TEST_SIGNER_FAIL", "1")
			}
			err = runPostbuild(cfg, configPath, oldFolder, "release", "2.0.0")
			for name, want := range before {
				if got := commandRead(t, filepath.Join(oldFolder, name)); !bytes.Equal(got, want) {
					t.Errorf("old artifact %s changed", name)
				}
			}
			if got := commandRead(t, oldFolder+".zip"); !bytes.Equal(got, oldZIP) {
				t.Fatal("source ZIP changed")
			}
			newRoot := newName + ".rootfs.ext4"
			for _, folder := range []string{oldFolder, newFolder} {
				copies, err := filepath.Glob(filepath.Join(folder, "*.original"))
				if err != nil {
					t.Fatal(err)
				}
				if len(copies) != 0 {
					t.Fatalf("generated recovery copies: %v", copies)
				}
			}
			if fail {
				if err == nil {
					t.Fatalf("signer failure = %v", err)
				}
				for link, want := range links {
					if got, err := os.Readlink(filepath.Join(artifacts, link)); err != nil || got != want {
						t.Errorf("latest %s changed: %q, %v", link, got, err)
					}
				}
				if _, err := os.Stat(newFolder + ".zip"); !os.IsNotExist(err) {
					t.Fatalf("failed version published ZIP: %v", err)
				}
				if got := commandRead(t, filepath.Join(newFolder, "sha.txt")); !bytes.Equal(got, before["sha.txt"]) {
					t.Fatal("failed version finalized checksums")
				}
				if got := commandRead(t, filepath.Join(newFolder, newName+".swu")); !bytes.Equal(got, swu) {
					t.Fatal("failed version repackaged SWU")
				}
				for _, suffix := range []string{".avb", ".avb.footer", ".avb.body", ".avb.tail"} {
					if got := commandRead(t, filepath.Join(newFolder, newRoot+suffix)); !bytes.Equal(got, before[root+suffix]) {
						t.Fatalf("failed version replaced %s", suffix)
					}
				}
				if got := commandRead(t, filepath.Join(newFolder, "proto-swupdate")); !bytes.Equal(got, before["proto-swupdate"]) {
					t.Fatal("failed version exported configured updater")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantRoot := append(append([]byte{}, plain...), []byte("VERSION=2.0.0\n")...)
			wantRoot = append(wantRoot, make([]byte, (512-len(wantRoot)%512)%512)...)
			if got := commandRead(t, filepath.Join(newFolder, newRoot)); !bytes.Equal(got, wantRoot) {
				t.Fatalf("new rootfs = %q", got)
			}
			for _, suffix := range []string{".avb", ".avb.footer"} {
				if !bytes.Equal(commandRead(t, filepath.Join(newFolder, newRoot+suffix)), before[root+suffix]) {
					t.Fatalf("legacy %s changed", suffix)
				}
			}
			assertAVBPair(t, newFolder, newRoot)

			extracted := t.TempDir()
			commandRun(t, extracted, string(commandRead(t, filepath.Join(newFolder, newName+".swu"))), "cpio", "-idm")
			compressed := commandRead(t, filepath.Join(extracted, root+".gz"))
			reader, err := gzip.NewReader(bytes.NewReader(compressed))
			if err != nil {
				t.Fatal(err)
			}
			unpacked, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(unpacked, wantRoot) {
				t.Fatal("SWU gzip differs from plain rootfs")
			}
			assertAVBPair(t, extracted, root)
			description := string(commandRead(t, filepath.Join(extracted, "sw-description")))
			if strings.Count(description, root+".gz") != 2 || strings.Contains(description, newRoot+".gz") || strings.Count(description, fmt.Sprintf("%x", sha256.Sum256(compressed))) != 2 {
				t.Fatalf("A/B references or hashes disagree: %s", description)
			}
			for _, name := range []string{newName + ".swu", newName + "-with-fip.swu", "extra.swu"} {
				unpacked := t.TempDir()
				commandRun(t, unpacked, string(commandRead(t, filepath.Join(newFolder, name))), "cpio", "-idm")
				commandRun(t, unpacked, "", "openssl", "dgst", "-sha256", "-verify", filepath.Join(dir, "public.pem"), "-signature", "sw-description.sig", "sw-description")
				assertCommandOpaqueMembers(t, unpacked, name == newName+"-with-fip.swu")
				if got := commandRun(t, unpacked, "", "gzip", "-dc", root+".gz"); !bytes.Equal(got, wantRoot) {
					t.Fatal("rebuilt seed rootfs differs")
				}
				assertAVBPair(t, unpacked, root)
				description := string(commandRead(t, filepath.Join(unpacked, "sw-description")))
				for _, member := range []string{root + ".gz", root + ".avb.body", root + ".avb.tail"} {
					if !strings.Contains(description, fmt.Sprintf("%x", sha256.Sum256(commandRead(t, filepath.Join(unpacked, member))))) {
						t.Fatalf("rebuilt seed missing hash for %s", member)
					}
				}
			}
			if got := string(commandRead(t, filepath.Join(newFolder, "proto-swupdate"))); got != updater {
				t.Fatal("exported updater differs from configured source")
			}
			checksums := string(commandRead(t, filepath.Join(newFolder, "sha.txt")))
			for _, suffix := range []string{".avb", ".avb.footer"} {
				want := fmt.Sprintf("%x  %s%s\n", sha256.Sum256(before[root+suffix]), newRoot, suffix)
				if !strings.Contains(checksums, want) {
					t.Fatalf("checksums omit preserved legacy %s", suffix)
				}
			}
			entries, err = os.ReadDir(newFolder)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				name := entry.Name()
				if strings.Contains(name, oldName) {
					t.Errorf("old filename remains: %s", name)
				}
				if name == "sha.txt" {
					continue
				}
				if entry.IsDir() {
					continue
				}
				want := fmt.Sprintf("%x  %s\n", sha256.Sum256(commandRead(t, filepath.Join(newFolder, name))), name)
				if !strings.Contains(checksums, want) {
					t.Errorf("missing final checksum for %s", name)
				}
			}
			archive, err := zip.OpenReader(newFolder + ".zip")
			if err != nil {
				t.Fatal(err)
			}
			defer archive.Close()
			memberFIP, err := archive.Open(fipPath)
			if err != nil {
				t.Fatalf("ZIP missing deployed FIP: %v", err)
			}
			memberFIP.Close()
			for _, folder := range []string{oldFolder, newFolder} {
				if !bytes.Equal(commandRead(t, filepath.Join(folder, fipPath)), wantFIP) {
					t.Fatalf("changed deployed FIP in %s", folder)
				}
			}
			for _, suffix := range []string{".avb.body", ".avb.tail"} {
				member, err := archive.Open(newRoot + suffix)
				if err != nil {
					t.Fatalf("ZIP missing renamed %s: %v", suffix, err)
				}
				member.Close()
			}
			for _, member := range archive.File {
				if member.FileInfo().IsDir() {
					continue
				}
				if strings.HasSuffix(member.Name, ".original") {
					t.Fatalf("ZIP contains recovery copy %s", member.Name)
				}
				reader, err := member.Open()
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(reader)
				if err != nil {
					t.Fatal(err)
				}
				if err := reader.Close(); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(data, commandRead(t, filepath.Join(newFolder, member.Name))) {
					t.Errorf("ZIP differs from final %s", member.Name)
				}
			}
			for link := range links {
				want := newFolder
				if strings.HasSuffix(link, ".zip") {
					want += ".zip"
				}
				want, err := filepath.EvalSymlinks(want)
				if err != nil {
					t.Fatal(err)
				}
				if got, err := filepath.EvalSymlinks(filepath.Join(artifacts, link)); err != nil || got != want {
					t.Errorf("latest %s resolves to %q, want %q: %v", link, got, want, err)
				}
			}
			commandWrite(t, config.ResolvePath(configPath, cfg.Artifacts.LocalFiles[0]), []byte("new raw updater"))
			if err := runPostbuild(cfg, configPath, newFolder, "release", "2.0.0"); err != nil {
				t.Fatal(err)
			}
			if got := string(commandRead(t, filepath.Join(newFolder, "proto-swupdate"))); got != "new raw updater" {
				t.Fatal("changed raw updater source not exported")
			}
			for _, name := range []string{newName + ".swu", newName + "-with-fip.swu", "extra.swu"} {
				unpacked := t.TempDir()
				commandRun(t, unpacked, string(commandRead(t, filepath.Join(newFolder, name))), "cpio", "-idm")
				commandRun(t, unpacked, "", "openssl", "dgst", "-sha256", "-verify", filepath.Join(dir, "public.pem"), "-signature", "sw-description.sig", "sw-description")
				if got := commandRun(t, unpacked, "", "gzip", "-dc", root+".gz"); !bytes.Equal(got, commandRead(t, filepath.Join(newFolder, newRoot))) {
					t.Fatal("repeated postbuild rootfs differs")
				}
				assertAVBPair(t, unpacked, root)
				assertCommandOpaqueMembers(t, unpacked, name == newName+"-with-fip.swu")
				description := string(commandRead(t, filepath.Join(unpacked, "sw-description")))
				for _, member := range []string{root + ".gz", root + ".avb.body", root + ".avb.tail"} {
					if !strings.Contains(description, fmt.Sprintf("%x", sha256.Sum256(commandRead(t, filepath.Join(unpacked, member))))) {
						t.Fatalf("repeated postbuild missing hash for %s", member)
					}
				}
			}
		})
	}
}

func TestInjectCommandWithoutPackagesOrVersion(t *testing.T) {
	for _, variant := range []string{"release", "mfg"} {
		t.Run(variant, func(t *testing.T) {
			cfg, configPath := commandFixture(t)
			folder := t.TempDir()
			root := "miner-image-" + variant + "-c3-p1-1.rootfs.ext4"
			commandWrite(t, filepath.Join(folder, root), []byte("plain\n"+strings.Repeat("\x00", 512-6)))
			updater := "raw updater"
			commandWrite(t, filepath.Join(folder, "proto-swupdate"), []byte(updater))
			{
				for _, tool := range []string{"cpio", "openssl", "gzip"} {
					if _, err := exec.LookPath(tool); err != nil {
						t.Skipf("%s required: %v", tool, err)
					}
				}
				keyDir := filepath.Dir(configPath)
				cfg.Signing = config.Signing{KeyDir: keyDir, PrivateKey: "swu.pem", Passfile: "passout"}
				commandWrite(t, filepath.Join(keyDir, "passout"), []byte("unused\n"))
				commandRun(t, keyDir, "", "openssl", "genpkey", "-algorithm", "RSA", "-pkeyopt", "rsa_keygen_bits:2048", "-out", "swu.pem")
				commandRun(t, keyDir, "", "openssl", "pkey", "-in", "swu.pem", "-pubout", "-out", "public.pem")
				payload := t.TempDir()
				commandWrite(t, filepath.Join(payload, root+".gz"), commandRun(t, folder, "", "gzip", "-c", root))
				entry := func(device string) string {
					return fmt.Sprintf(`{ filename = %q; sha256 = %q; device = %q; type = "raw"; compressed = "zlib"; installed-directly = true; }`, root+".gz", strings.Repeat("0", 64), device)
				}
				commandWrite(t, filepath.Join(payload, "sw-description"), []byte("software = { stable = { copy1 = { images = ("+entry("/dev/mmcblk0p7")+"); }; copy2 = { images = ("+entry("/dev/mmcblk0p8")+"); }; }; };"))
				if err := os.Mkdir(filepath.Join(folder, "fip"), 0755); err != nil {
					t.Fatal(err)
				}
				commandWrite(t, filepath.Join(folder, "fip", "fip-stm32mp131f-c3p1-optee.bin"), []byte("deployed FIP"))
				commandRun(t, payload, "", "openssl", "dgst", "-sha256", "-sign", filepath.Join(keyDir, "swu.pem"), "-out", "sw-description.sig", "sw-description")
				commandWrite(t, filepath.Join(folder, strings.TrimSuffix(root, ".rootfs.ext4")+".swu"), commandRun(t, payload, "sw-description\nsw-description.sig\n"+root+".gz\n", "cpio", "-o", "-H", "newc"))
			}
			data, err := yaml.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			commandWrite(t, configPath, data)
			input := filepath.Join(t.TempDir(), "arbitrary.zip")
			if err := fileutil.CreateZip(folder, input); err != nil {
				t.Fatal(err)
			}
			app := &cli.App{Flags: []cli.Flag{&cli.StringFlag{Name: "config"}}, Commands: []*cli.Command{injectCommand()}}
			if err := app.Run([]string{"distro", "--config", configPath, "inject", input}); err != nil {
				t.Fatal(err)
			}
			output := t.TempDir()
			if err := fileutil.ExtractZip(input, output); err != nil {
				t.Fatal(err)
			}
			assertAVBPair(t, output, root)
			extracted := t.TempDir()
			canonical := strings.TrimSuffix(root, ".rootfs.ext4") + ".swu"
			commandRun(t, extracted, string(commandRead(t, filepath.Join(output, canonical))), "cpio", "-idm")
			assertAVBPair(t, extracted, root)
			if got := commandRun(t, extracted, "", "gzip", "-dc", root+".gz"); !bytes.Equal(got, commandRead(t, filepath.Join(output, root))) {
				t.Fatal("SWU gzip differs from plain rootfs")
			}
			for _, suffix := range []string{".swu"} {
				unpacked := t.TempDir()
				commandRun(t, unpacked, string(commandRead(t, filepath.Join(output, strings.TrimSuffix(root, ".rootfs.ext4")+suffix))), "cpio", "-idm")
				commandRun(t, unpacked, "", "openssl", "dgst", "-sha256", "-verify", filepath.Join(filepath.Dir(configPath), "public.pem"), "-signature", "sw-description.sig", "sw-description")
				assertCommandOpaqueMembers(t, unpacked, false)
			}
			if _, err := os.Stat(filepath.Join(output, strings.TrimSuffix(canonical, ".swu")+"-with-fip.swu")); !os.IsNotExist(err) {
				t.Fatalf("single seed synthesized companion: %v", err)
			}
			checksums := string(commandRead(t, filepath.Join(output, "sha.txt")))
			for _, name := range []string{root, root + ".avb.body", root + ".avb.tail", canonical, "proto-swupdate"} {
				want := fmt.Sprintf("%x  %s\n", sha256.Sum256(commandRead(t, filepath.Join(output, name))), name)
				if !strings.Contains(checksums, want) {
					t.Errorf("missing final checksum for %s", name)
				}
			}
		})
	}
}

func assertAVBPair(t *testing.T, dir, root string) {
	t.Helper()
	for suffix, fixture := range map[string]string{".avb.body": "DISTRO_TEST_BODY", ".avb.tail": "DISTRO_TEST_TAIL"} {
		if got := commandRead(t, filepath.Join(dir, root+suffix)); !bytes.Equal(got, commandRead(t, os.Getenv(fixture))) {
			t.Fatalf("changed %s fixture", suffix)
		}
	}
}

func assertCommandOpaqueMembers(t *testing.T, dir string, present bool) {
	t.Helper()
	for name, want := range map[string]string{"proto-swupdate": "opaque embedded updater", "seed-hook.sh": "#!/bin/sh\nexit 0\n"} {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if !present {
			if !os.IsNotExist(err) {
				t.Fatalf("seed acquired %s: %v", name, err)
			}
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0755 {
			t.Fatalf("opaque member %s mode changed: %v", name, err)
		}
		if string(commandRead(t, path)) != want {
			t.Fatalf("opaque member %s bytes changed", name)
		}
	}
}

func writeCommandCombinedSeed(t *testing.T, payload, folder, name string) {
	t.Helper()
	hook := "#!/bin/sh\nexit 0\n"
	description := string(commandRead(t, filepath.Join(payload, "sw-description")))
	description = strings.TrimSuffix(strings.TrimSpace(description), "};")
	description += fmt.Sprintf(` extra = { images = ({ filename = "fip-ota.bin"; type = "raw"; sha256 = "old"; }); scripts = ({ filename = "seed-hook.sh"; type = "postinstall"; sha256 = "%x"; }); }; };`, sha256.Sum256([]byte(hook)))
	commandWrite(t, filepath.Join(payload, "sw-description"), []byte(description))
	for member, data := range map[string]string{"fip-ota.bin": "seed FIP", "proto-swupdate": "opaque embedded updater", "seed-hook.sh": hook} {
		commandWrite(t, filepath.Join(payload, member), []byte(data))
		if err := os.Chmod(filepath.Join(payload, member), 0755); err != nil {
			t.Fatal(err)
		}
	}
	members := "sw-description\n"
	entries, err := os.ReadDir(payload)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "sw-description" && entry.Name() != "sw-description.sig" {
			members += entry.Name() + "\n"
		}
	}
	commandWrite(t, filepath.Join(folder, name), commandRun(t, payload, members, "cpio", "-o", "-H", "newc"))
}

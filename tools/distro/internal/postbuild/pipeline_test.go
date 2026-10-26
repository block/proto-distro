package postbuild

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"proto-distro/tools/distro/internal/config"
)

func TestProcessPostBuildPublishesUnsignedImages(t *testing.T) {
	for _, variant := range []string{"minimal", "base"} {
		t.Run(variant, func(t *testing.T) {
			dir := t.TempDir()
			images := filepath.Join(dir, "images")
			if err := os.Mkdir(images, 0755); err != nil {
				t.Fatal(err)
			}
			name := "miner-image-" + variant + "-c3-p1-1.2.3"
			root := name + ".rootfs.ext4"
			plain := []byte("unsigned rootfs")
			if err := os.WriteFile(filepath.Join(images, root), plain, 0600); err != nil {
				t.Fatal(err)
			}
			cfg := &config.Config{Artifacts: config.Artifacts{RootDir: "artifacts", ImagesBasePath: "images", Files: []string{"{target}-{machine}-{version}.rootfs.ext4"}}}
			if err := ProcessPostBuild(cfg, filepath.Join(dir, "distro.yaml"), dir, "c3-p1", "proto", variant, "1.2.3", ""); err != nil {
				t.Fatal(err)
			}
			artifacts := filepath.Join(dir, "artifacts")
			folder := filepath.Join(artifacts, name)
			data, err := os.ReadFile(filepath.Join(folder, root))
			if err != nil || !bytes.Equal(data, plain) {
				t.Fatalf("published rootfs = %q: %v", data, err)
			}
			checksums, err := os.ReadFile(filepath.Join(folder, "sha.txt"))
			if err != nil || string(checksums) != fmt.Sprintf("%x  %s\n", sha256.Sum256(plain), root) {
				t.Fatalf("unsigned checksums = %q: %v", checksums, err)
			}
			for _, link := range []string{"miner-image-" + variant + "-c3-p1-latest", "miner-image-latest"} {
				assertSymlink(t, filepath.Join(artifacts, link), name)
			}
			assertSymlink(t, filepath.Join(artifacts, "miner-image-latest.zip"), name+".zip")
			archive, err := zip.OpenReader(folder + ".zip")
			if err != nil {
				t.Fatal(err)
			}
			defer archive.Close()
			assertZipContains(t, folder+".zip", "sha.txt")
			for _, member := range archive.File {
				if member.Name != root {
					continue
				}
				reader, err := member.Open()
				if err != nil {
					t.Fatal(err)
				}
				data, err = io.ReadAll(reader)
				reader.Close()
				if err != nil || !bytes.Equal(data, plain) {
					t.Fatalf("ZIP rootfs = %q: %v", data, err)
				}
			}
		})
	}
}

func TestProcessPostBuildSignsBeforePublication(t *testing.T) {
	for _, variant := range []string{"release", "mfg"} {
		for _, mode := range []string{"disabled", "zero packages", "signer failure", "missing companion", "missing updater"} {
			fail := mode == "signer failure"
			t.Run(variant+"/"+mode, func(t *testing.T) {
				dir := t.TempDir()
				shim, err := os.ReadFile("../distroinject/testdata/docker")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "docker"), shim, 0755); err != nil {
					t.Fatal(err)
				}
				body := bytes.Repeat([]byte{0x37}, 4096)
				tail := bytes.Repeat([]byte{0x91}, 512)
				for name, data := range map[string][]byte{"body": body, "tail": tail} {
					path := filepath.Join(dir, "fixture."+name)
					if err := os.WriteFile(path, data, 0600); err != nil {
						t.Fatal(err)
					}
					t.Setenv("DISTRO_TEST_"+strings.ToUpper(name), path)
				}
				t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
				if fail {
					t.Setenv("DISTRO_TEST_SIGNER_FAIL", "1")
				}
				images := filepath.Join(dir, "images")
				artifacts := filepath.Join(dir, "artifacts")
				for _, path := range []string{images, artifacts} {
					if err := os.Mkdir(path, 0755); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Mkdir(filepath.Join(images, "fip"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(images, "fip", "fip-stm32mp131f-c3p1-optee.bin"), []byte("exact deployed FIP"), 0600); err != nil {
					t.Fatal(err)
				}
				name := "miner-image-" + variant + "-c3-p1-1.2.3"
				root := name + ".rootfs.ext4"
				key := filepath.Join(dir, "c3.key")
				plain := strings.Repeat("r", 512)
				for path, data := range map[string]string{
					filepath.Join(images, root): plain, key: "c3",
					filepath.Join(images, "sha.txt"):          "old checksum sentinel",
					filepath.Join(images, root+".avb"):        "legacy full sentinel",
					filepath.Join(images, root+".avb.footer"): "legacy footer sentinel",
				} {
					if err := os.WriteFile(path, []byte(data), 0600); err != nil {
						t.Fatal(err)
					}
				}
				privateKey := filepath.Join(dir, "private.pem")
				passfile := filepath.Join(dir, "passfile")
				if err := os.WriteFile(passfile, []byte("test-passphrase\n"), 0600); err != nil {
					t.Fatal(err)
				}
				postbuildCommand(t, dir, "", "openssl", "genpkey", "-algorithm", "RSA", "-pkeyopt", "rsa_keygen_bits:2048", "-aes-256-cbc", "-pass", "file:"+passfile, "-out", privateKey)
				postbuildCommand(t, dir, "", "openssl", "pkey", "-in", privateKey, "-passin", "file:"+passfile, "-pubout", "-out", "public.pem")
				seedDir := t.TempDir()
				var selections []string
				for i, device := range []string{"/dev/mmcblk0p7", "/dev/mmcblk0p8"} {
					selections = append(selections, fmt.Sprintf(`copy%d: { images: ({ filename = %q; device = %q; type = "raw"; compressed = "zlib"; sha256 = "old"; }); };`, i+1, root+".gz", device))
				}
				description := `software = { stable: {` + strings.Join(selections, "\n") + `}; };`
				for member, data := range map[string]string{"sw-description": description, root + ".gz": postbuildCommand(t, dir, "old rootfs", "gzip", "-c")} {
					if err := os.WriteFile(filepath.Join(seedDir, member), []byte(data), 0600); err != nil {
						t.Fatal(err)
					}
				}
				seed := postbuildCommand(t, seedDir, "sw-description\n"+root+".gz\n", "cpio", "-o", "-H", "newc")
				if err := os.WriteFile(filepath.Join(images, name+".swu"), []byte(seed), 0600); err != nil {
					t.Fatal(err)
				}
				hook := "#!/bin/sh\nexit 0\n"
				embeddedUpdater := "opaque seed updater"
				combined := `software = { stable: {` + strings.Join(selections, "\n") + `}; extra: { images: ({ filename = "fip-ota.bin"; type = "raw"; sha256 = "old"; }); scripts: ({ filename = "seed-hook.sh"; type = "postinstall"; sha256 = "` + fmt.Sprintf("%x", sha256.Sum256([]byte(hook))) + `"; }); }; };`
				for member, data := range map[string]string{"sw-description": combined, "fip-ota.bin": "old FIP", "seed-hook.sh": hook, "proto-swupdate": embeddedUpdater} {
					if err := os.WriteFile(filepath.Join(seedDir, member), []byte(data), 0755); err != nil {
						t.Fatal(err)
					}
				}
				if mode != "missing companion" {
					combinedSeed := postbuildCommand(t, seedDir, "sw-description\n"+root+".gz\nfip-ota.bin\nseed-hook.sh\nproto-swupdate\n", "cpio", "-o", "-H", "newc")
					if err := os.WriteFile(filepath.Join(images, name+"-with-fip.swu"), []byte(combinedSeed), 0600); err != nil {
						t.Fatal(err)
					}
				}
				links := []string{"miner-image-" + variant + "-c3-p1-latest", "miner-image-latest", "miner-image-latest.zip"}
				for _, link := range links {
					if err := os.Symlink("previous", filepath.Join(artifacts, link)); err != nil {
						t.Fatal(err)
					}
				}
				cfg := &config.Config{
					Defaults:  config.Defaults{Machine: "unknown"},
					Artifacts: config.Artifacts{RootDir: "artifacts", ImagesBasePath: "images", Files: []string{"{target}-{machine}-{version}.rootfs.ext4", "{target}-{machine}-{version}.swu", "{target}-{machine}-{version}-with-fip.swu", "fip/", "sha.txt", "{target}-{machine}-{version}.rootfs.ext4.avb", "{target}-{machine}-{version}.rootfs.ext4.avb.footer"}},
					AVB:       map[string]config.AVBKeys{"c3-p1": {PrivateKey: key, PublicKey: key, AVBPublicKey: key}},
				}
				cfg.Artifacts.LocalFiles = []string{"local/proto-swupdate"}
				updater := "#!/bin/sh\nprintf updater\n"
				if err := os.Mkdir(filepath.Join(dir, "local"), 0755); err != nil {
					t.Fatal(err)
				}
				if mode != "missing updater" {
					if err := os.WriteFile(filepath.Join(dir, "local", "proto-swupdate"), []byte(updater), 0755); err != nil {
						t.Fatal(err)
					}
				}
				cfg.Signing = config.Signing{KeyDir: dir, PrivateKey: "private.pem", Passfile: "passfile"}
				if mode == "zero packages" {
					cfg.Defaults.Arch = "armv7"
					cfg.Injection.Enabled = true
					cfg.Injection.Variants = map[string]config.InjectionVariant{variant: {}}
				}
				cfg.Docker.Injector.Image = "test-injector"
				err = ProcessPostBuild(cfg, filepath.Join(dir, "distro.yaml"), dir, "c3-p1", "proto", variant, "1.2.3", "")
				folder := filepath.Join(artifacts, name)
				if fail || mode == "missing companion" || mode == "missing updater" {
					if err == nil {
						t.Fatal("invalid build inputs succeeded")
					}
					for _, link := range links {
						assertSymlink(t, filepath.Join(artifacts, link), "previous")
					}
					paths := []string{folder + ".zip", filepath.Join(folder, "proto-swupdate")}
					if fail {
						paths = append(paths, filepath.Join(folder, root+".avb.body"), filepath.Join(folder, root+".avb.tail"))
					}
					for _, path := range paths {
						if _, err := os.Stat(path); !os.IsNotExist(err) {
							t.Errorf("failure published %s: %v", path, err)
						}
					}
					if mode != "missing companion" {
						checksums, readErr := os.ReadFile(filepath.Join(folder, "sha.txt"))
						if readErr != nil || string(checksums) != "old checksum sentinel" {
							t.Fatalf("failed finalization changed checksums: %q: %v", checksums, readErr)
						}
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				for path, want := range map[string]string{
					filepath.Join(folder, root):             plain,
					filepath.Join(folder, root+".avb.body"): string(body),
					filepath.Join(folder, root+".avb.tail"): string(tail),
					filepath.Join(images, root):             plain,
				} {
					data, err := os.ReadFile(path)
					if err != nil || string(data) != want {
						t.Fatalf("%s = %q, want %q: %v", path, data, want, err)
					}
				}
				info, err := os.Stat(filepath.Join(folder, "proto-swupdate"))
				if err != nil || info.Mode().Perm() != 0755 {
					t.Fatalf("updater executable mode: %v: %v", info, err)
				}
				exportedUpdater, err := os.ReadFile(filepath.Join(folder, "proto-swupdate"))
				if err != nil || string(exportedUpdater) != updater {
					t.Fatalf("exported updater differs from configured source: %v", err)
				}
				checksums, err := os.ReadFile(filepath.Join(folder, "sha.txt"))
				if err != nil {
					t.Fatal(err)
				}
				for suffix, content := range map[string]string{"": plain, ".avb.body": string(body), ".avb.tail": string(tail)} {
					want := fmt.Sprintf("%x  %s%s\n", sha256.Sum256([]byte(content)), root, suffix)
					if !strings.Contains(string(checksums), want) {
						t.Errorf("%s missing from final checksums", suffix)
					}
				}
				if !strings.Contains(string(checksums), fmt.Sprintf("%x  proto-swupdate\n", sha256.Sum256([]byte(updater)))) {
					t.Fatal("updater missing from checksums")
				}
				if err := filepath.WalkDir(folder, func(path string, entry os.DirEntry, err error) error {
					if err == nil && strings.HasSuffix(entry.Name(), ".original") {
						t.Errorf("build published recovery copy: %s", path)
					}
					return err
				}); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(checksums), ".original") {
					t.Fatal("checksums contain recovery copies")
				}
				assertSymlink(t, filepath.Join(artifacts, "miner-image-latest"), name)
				assertSymlink(t, filepath.Join(artifacts, "miner-image-latest.zip"), name+".zip")
				for suffix, want := range map[string]string{".avb": "legacy full sentinel", ".avb.footer": "legacy footer sentinel"} {
					data, err := os.ReadFile(filepath.Join(folder, root+suffix))
					if err != nil || string(data) != want {
						t.Fatalf("legacy %s changed: %v", suffix, err)
					}
					if !strings.Contains(string(checksums), fmt.Sprintf("%x  %s%s\n", sha256.Sum256(data), root, suffix)) {
						t.Fatalf("checksums omit legacy %s", suffix)
					}
				}
				archive, err := zip.OpenReader(folder + ".zip")
				if err != nil {
					t.Fatal(err)
				}
				defer archive.Close()
				for _, member := range archive.File {
					if strings.HasSuffix(member.Name, ".original") {
						t.Errorf("ZIP contains recovery copy: %s", member.Name)
					}
					if member.Name == "proto-swupdate" {
						reader, err := member.Open()
						if err != nil {
							t.Fatal(err)
						}
						data, err := io.ReadAll(reader)
						reader.Close()
						if err != nil || string(data) != updater || member.Mode().Perm() != 0755 {
							t.Fatalf("ZIP updater bytes or mode differ: %v", err)
						}
					}
				}
				assertZipContains(t, folder+".zip", root+".avb.body")
				assertZipContains(t, folder+".zip", root+".avb.tail")
				assertZipContains(t, folder+".zip", root+".avb")
				assertZipContains(t, folder+".zip", root+".avb.footer")
				assertZipContains(t, folder+".zip", "proto-swupdate")
				for _, suffix := range []string{".swu", "-with-fip.swu"} {
					assertZipContains(t, folder+".zip", name+suffix)
					data, err := os.ReadFile(filepath.Join(folder, name+suffix))
					if err != nil {
						t.Fatal(err)
					}
					extracted := t.TempDir()
					postbuildCommand(t, extracted, string(data), "cpio", "-idm")
					postbuildCommand(t, extracted, "", "openssl", "dgst", "-sha256", "-verify", filepath.Join(dir, "public.pem"), "-signature", "sw-description.sig", "sw-description")
					if got := postbuildCommand(t, extracted, "", "gzip", "-dc", root+".gz"); got != plain {
						t.Fatal("SWU rootfs differs")
					}
					for part, want := range map[string][]byte{"body": body, "tail": tail} {
						got, err := os.ReadFile(filepath.Join(extracted, root+".avb."+part))
						if err != nil || !bytes.Equal(got, want) {
							t.Fatalf("SWU %s differs: %v", part, err)
						}
					}
					fip, err := os.ReadFile(filepath.Join(extracted, "fip-ota.bin"))
					if suffix == ".swu" && !os.IsNotExist(err) || suffix != ".swu" && (err != nil || string(fip) != "exact deployed FIP") {
						t.Fatalf("FIP membership: %v", err)
					}
					if suffix != ".swu" {
						for member, want := range map[string]string{"seed-hook.sh": hook, "proto-swupdate": embeddedUpdater} {
							got, err := os.ReadFile(filepath.Join(extracted, member))
							if err != nil || string(got) != want {
								t.Fatalf("opaque seed member %s changed: %v", member, err)
							}
						}
					}
				}
			})
		}
	}
}

func postbuildCommand(t *testing.T, directory, input, name string, args ...string) string {
	t.Helper()
	command := exec.Command(name, args...)
	command.Dir = directory
	command.Stdin = strings.NewReader(input)
	var stderr strings.Builder
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("%s: %v: %s", name, err, stderr.String())
	}
	return string(output)
}

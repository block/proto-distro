package swurepackage

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func writeTest(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func readTest(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func runTest(t *testing.T, dir string, stdin []byte, name string, args ...string) []byte {
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
func imageFieldsTest(t *testing.T, description string) []map[string]string {
	t.Helper()
	tokens, err := configTokens(description)
	if err != nil {
		t.Fatal(err)
	}
	var result []map[string]string
	for i := 0; i+2 < len(tokens); i++ {
		if tokens[i].text != "images" {
			continue
		}
		close := tokens[i+2].mate
		for j := i + 3; j < close; {
			if tokens[j].text == "," {
				j++
				continue
			}
			r, err := recordFields(tokens, j)
			if err != nil {
				t.Fatal(err)
			}
			fields := map[string]string{}
			for key, f := range r.fields {
				value := tokens[f.value].text
				if decoded, err := strconv.Unquote(value); err == nil {
					value = decoded
				}
				fields[key] = value
			}
			result = append(result, fields)
			j = r.end + 1
		}
		i = close
	}
	return result
}

func TestRunRepackagesRootfsAndAVB(t *testing.T) {
	for _, name := range []string{"gzip", "cpio", "openssl"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("requires %s: %v", name, err)
		}
	}
	workspace := t.TempDir()
	privateKey, publicKey, passfile := filepath.Join(workspace, "private.pem"), filepath.Join(workspace, "public.pem"), filepath.Join(workspace, "passphrase")
	writeTest(t, passfile, []byte("test-passphrase\n"))
	runTest(t, workspace, nil, "openssl", "genpkey", "-algorithm", "RSA", "-pkeyopt", "rsa_keygen_bits:2048", "-aes-256-cbc", "-pass", "file:"+passfile, "-out", privateKey)
	runTest(t, workspace, nil, "openssl", "pkey", "-in", privateKey, "-passin", "file:"+passfile, "-pubout", "-out", publicKey)
	fixture := t.TempDir()
	oldRoot := "miner-image-release-c3-p1-0.0.1.rootfs.ext4"
	oldPayload := runTest(t, fixture, []byte("old plain rootfs\n"), "gzip", "-c")
	writeTest(t, filepath.Join(fixture, oldRoot+".gz"), oldPayload)
	template := string(readTest(t, "../../../../sources/meta-miner/recipes-core/images/files/sw-description.in"))
	template = strings.NewReplacer("@@SWUPDATE_ROOTFS_SCRIPTS@@", "", "@@SWUPDATE_FIP_DESCRIPTION@@", "").Replace(template)
	description := strings.NewReplacer("@@MEMFAULT_SOFTWARE_VERSION@@", "0.0.1", "@@VARIANT@@", "release", "@@MEMFAULT_HARDWARE_VERSION@@", "c3-p1", "@@SWUPDATE_IMAGES@@", oldRoot+".gz", "@@UBOOT_ENV_PARTITION_A@@", "7", "@@UBOOT_ENV_PARTITION_B@@", "8").Replace(template)
	description = strings.ReplaceAll(description, "$swupdate_get_sha256("+oldRoot+".gz)", fmt.Sprintf("%x", sha256.Sum256(oldPayload)))
	fip := []byte("unrelated boot firmware")
	fipHash := fmt.Sprintf("%x", sha256.Sum256(fip))
	fipRecord := fmt.Sprintf(`{ filename = "fip.bin"; sha256 = "%s"; device = "/dev/mmcblk0p3"; type = "raw"; properties = { note = "quoted } // braces"; }; },`, fipHash)
	description = strings.ReplaceAll(description, "images: (", "images: (\n"+fipRecord)
	script := fmt.Sprintf(`scripts: ({ filename = "scripts/post.sh"; type = "shellscript"; sha256 = "%s"; });`, fipHash)
	description = strings.Replace(description, "stable: {", script+"\nstable: {", 1)
	writeTest(t, filepath.Join(fixture, "fip.bin"), fip)
	if err := os.Mkdir(filepath.Join(fixture, "scripts"), 0700); err != nil {
		t.Fatal(err)
	}
	writeTest(t, filepath.Join(fixture, "scripts/post.sh"), fip)
	writeTest(t, filepath.Join(fixture, "sw-description"), []byte(description))
	if err := signFile(filepath.Join(fixture, "sw-description"), filepath.Join(fixture, "sw-description.sig"), privateKey, passfile); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(workspace, "seed.swu")
	// Build the input independently so a broken createCPIO cannot omit the fixture.
	members := "sw-description\nsw-description.sig\n" + oldRoot + ".gz\nfip.bin\nscripts\nscripts/post.sh\n"
	writeTest(t, archive, runTest(t, fixture, []byte(members), "cpio", "-o", "-H", "newc"))
	seedArchive := archive
	var signedArchive string
	for _, step := range []struct {
		name, version string
		fill          byte
	}{
		{"plain", "0.0.3", 0x44},
		{"split", "0.0.2", 0xa5},
		{"identical", "0.0.2", 0xa5},
		{"changed-version", "0.0.3", 0x5a},
	} {
		t.Run(step.name, func(t *testing.T) {
			root := oldRoot
			raw := bytes.Repeat([]byte{step.fill}, 61952)
			source := filepath.Join(workspace, "miner-image-release-c3-p1-"+step.version+".rootfs.ext4")
			writeTest(t, source, raw)
			body := bytes.Repeat([]byte{step.fill}, 8192)
			tail := bytes.Repeat([]byte{step.fill ^ 0xff}, 512)
			bodyPath, tailPath := source+".avb.body", source+".avb.tail"
			writeTest(t, bodyPath, body)
			writeTest(t, tailPath, tail)
			out := filepath.Join(workspace, step.name+"-"+step.version+".swu")
			if err := Run(Options{OldSWU: archive, NewRootfs: source, AVBBody: bodyPath, AVBTail: tailPath, PrivateKey: privateKey, Passfile: passfile, Output: out}); err != nil {
				t.Fatal(err)
			}
			archiveBytes := readTest(t, out)
			members := strings.Fields(string(runTest(t, workspace, archiveBytes, "cpio", "-it")))
			want := []string{"sw-description", "sw-description.sig", root + ".gz", root + ".avb.body", root + ".avb.tail", "fip.bin", "scripts", "scripts/post.sh"}
			if !reflect.DeepEqual(members, want) {
				t.Fatalf("members/order %q want %q", members, want)
			}
			extracted := t.TempDir()
			runTest(t, extracted, archiveBytes, "cpio", "-idm")
			if !bytes.Equal(readTest(t, filepath.Join(extracted, "scripts/post.sh")), fip) {
				t.Fatal("nested script bytes changed")
			}
			if got := runTest(t, extracted, nil, "gzip", "-dc", root+".gz"); !bytes.Equal(got, raw) {
				t.Fatal("gzip contains more than plain rootfs")
			}
			updated := string(readTest(t, filepath.Join(extracted, "sw-description")))
			if !strings.Contains(updated, script) {
				t.Fatal("unrelated script changed")
			}
			fields := imageFieldsTest(t, updated)
			perSlot := 4
			if len(fields) != 2*perSlot {
				t.Fatalf("image records: %v", fields)
			}
			for slot := range 2 {
				if fields[slot*perSlot]["sha256"] != fipHash {
					t.Fatal("unrelated image hash changed")
				}
				for j := 1; j < perSlot; j++ {
					image := fields[slot*perSlot+j]
					if image["device"] != fmt.Sprintf("/dev/mmcblk0p%d", 7+slot) || image["type"] != "raw" || image["installed-directly"] != "true" {
						t.Fatalf("incorrect selection: %v", image)
					}
					member := readTest(t, filepath.Join(extracted, image["filename"]))
					if image["sha256"] != fmt.Sprintf("%x", sha256.Sum256(member)) {
						t.Fatal("payload hash mismatch")
					}
					wantOffset := []string{"0", "61952", "1073741312"}[j-1]
					if image["offset"] != wantOffset {
						t.Fatalf("offset %s want %s", image["offset"], wantOffset)
					}
					if j == 1 {
						if image["compressed"] != "zlib" {
							t.Fatal("root not compressed")
						}
					} else if _, ok := image["compressed"]; ok {
						t.Fatal("metadata compression")
					}
				}
			}
			if !bytes.Equal(readTest(t, filepath.Join(extracted, root+".avb.body")), body) {
				t.Fatal("body bytes changed")
			}
			if !bytes.Equal(readTest(t, filepath.Join(extracted, root+".avb.tail")), tail) {
				t.Fatal("tail bytes differ")
			}
			signedArchive = out
			runTest(t, extracted, nil, "openssl", "dgst", "-sha256", "-verify", publicKey, "-signature", "sw-description.sig", "sw-description")
			archive = out
		})
	}
	// Invalid inputs must fail strict batch validation without creating output.
	// Archive and description ownership must agree before metadata is written.
	for _, kind := range []string{"missing-tail", "duplicate-body", "orphan-body", "ambiguous-root", "unaligned-body", "short-tail", "body-option-only", "tail-option-only", "full-image", "gzip-pair", "avb-downgrade"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			input := signedArchive
			if kind == "orphan-body" {
				input = seedArchive
			}
			runTest(t, dir, readTest(t, input), "cpio", "-idm")
			components, err := Inspect(input)
			if err != nil {
				t.Fatal(err)
			}
			root := strings.TrimSuffix(components.Rootfs, ".gz")
			order := []string{root + ".gz"}
			switch kind {
			case "missing-tail":
				if err := os.Remove(filepath.Join(dir, root+".avb.tail")); err != nil {
					t.Fatal(err)
				}
			case "duplicate-body":
				order = append(order, root+".avb.body", root+".avb.body")
			case "orphan-body":
				writeTest(t, filepath.Join(dir, root+".avb.body"), []byte("orphan"))
			case "ambiguous-root":
				writeTest(t, filepath.Join(dir, "other.rootfs.ext4.gz"), []byte("ambiguous"))
			}
			badArchive := filepath.Join(workspace, kind+"-input.swu")
			if _, err := createCPIO(dir, badArchive, order); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(workspace, root)
			writeTest(t, source, bytes.Repeat([]byte{1}, 61952))
			bodyPath, tailPath := source+".avb.body", source+".avb.tail"
			writeTest(t, bodyPath, bytes.Repeat([]byte{2}, 8192))
			writeTest(t, tailPath, bytes.Repeat([]byte{3}, 512))
			switch kind {
			case "unaligned-body":
				writeTest(t, bodyPath, []byte("bad"))
			case "short-tail":
				writeTest(t, tailPath, []byte("bad"))
			case "body-option-only":
				tailPath = ""
			case "tail-option-only":
				bodyPath = ""
			case "avb-downgrade":
				bodyPath, tailPath = "", ""
			}
			if kind == "full-image" {
				source += ".avb"
				writeTest(t, source, []byte("legacy"))
			}
			if kind == "gzip-pair" {
				source += ".gz"
				writeTest(t, source, runTest(t, workspace, []byte("raw"), "gzip", "-c"))
			}
			out := filepath.Join(workspace, kind+"-out.swu")
			if err := RebuildAll([]Options{{OldSWU: badArchive, NewRootfs: source, AVBBody: bodyPath, AVBTail: tailPath, PrivateKey: privateKey, Passfile: passfile, Output: out}}); err == nil {
				t.Fatal("accepted invalid input")
			}
			if _, err := os.Lstat(out); !os.IsNotExist(err) {
				t.Fatalf("failure left output: %v", err)
			}
		})
	}
	for _, tc := range []struct {
		name, suffix      string
		symlink, dangling bool
	}{
		{name: "body-file-collision", suffix: ".avb.body"},
		{name: "body-symlink-collision", suffix: ".avb.body", symlink: true},
		{name: "tail-dangling-symlink-collision", suffix: ".avb.tail", symlink: true, dangling: true},
		{name: "rootfs-compression-collision", suffix: ".gz", symlink: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			runTest(t, dir, readTest(t, seedArchive), "cpio", "-idm")
			root := oldRoot
			destination := filepath.Join(dir, root+tc.suffix)
			outside := filepath.Join(t.TempDir(), "target")
			if !tc.dangling {
				writeTest(t, outside, []byte("untouched"))
			}
			if tc.symlink {
				if tc.suffix == ".gz" {
					if err := os.Remove(destination); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(outside, destination); err != nil {
					t.Fatal(err)
				}
			} else {
				writeTest(t, destination, []byte("unrelated member"))
			}
			input := filepath.Join(t.TempDir(), "input.swu")
			if _, err := createCPIO(dir, input, []string{oldRoot + ".gz"}); err != nil {
				t.Fatal(err)
			}
			inputBytes := readTest(t, input)
			source := filepath.Join(t.TempDir(), root)
			writeTest(t, source, make([]byte, 1024))
			body, tail := source+".avb.body", source+".avb.tail"
			writeTest(t, body, make([]byte, 512))
			writeTest(t, tail, make([]byte, 512))
			out := filepath.Join(t.TempDir(), "output.swu")
			err := RebuildAll([]Options{{OldSWU: input, NewRootfs: source, AVBBody: body, AVBTail: tail, PrivateKey: privateKey, Passfile: passfile, Output: out}})
			if err == nil {
				t.Fatal("accepted unsafe or colliding archive member")
			}
			if tc.dangling {
				if _, err := os.Lstat(outside); !os.IsNotExist(err) {
					t.Fatal("created dangling symlink target")
				}
			} else if string(readTest(t, outside)) != "untouched" {
				t.Fatal("overwrote external symlink target")
			}
			if !bytes.Equal(readTest(t, input), inputBytes) {
				t.Fatal("collision changed input archive")
			}
			if _, err := os.Lstat(out); !os.IsNotExist(err) {
				t.Fatalf("collision left output: %v", err)
			}
		})
	}
}

func TestRecreationPreservesUnauthenticatedCompanions(t *testing.T) {
	opts, public := fipFixture(t)
	writeSeedFixture(t, opts, "rootfs")
	stage := t.TempDir()
	names, err := extractCPIO(opts.OldSWU, stage)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stage, "sw-description")
	record := fmt.Sprintf(`scripts=({filename="trusted-hook.sh";sha256=%q;type="postinstall";},{filename="missing.sh";sha256=%q;type="postinstall";});`, strings.Repeat("0", 64), strings.Repeat("0", 64))
	source := strings.ReplaceAll(string(readTest(t, path)), "images = (", "trusted={"+record+"}; images = (")
	writeTest(t, path, []byte(source))
	hook := []byte("#!/bin/sh\n# seed-owned hook\nexit 0\n")
	writeTest(t, filepath.Join(stage, "trusted-hook.sh"), hook)
	writeTest(t, filepath.Join(stage, "opaque-updater"), []byte("opaque updater"))
	names = append(names, "trusted-hook.sh", "opaque-updater")
	seed := filepath.Join(t.TempDir(), "seed.swu")
	if _, err := createCPIO(stage, seed, names[2:]); err != nil {
		t.Fatal(err)
	}
	opts.OldSWU = seed
	opts.NewFIP = ""
	if err := Run(opts); err != nil {
		t.Fatal(err)
	}
	output := t.TempDir()
	if _, err := extractCPIO(opts.Output, output); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readTest(t, filepath.Join(output, "trusted-hook.sh")), hook) ||
		string(readTest(t, filepath.Join(output, "opaque-updater"))) != "opaque updater" {
		t.Fatal("seed companion bytes changed")
	}
	description := string(readTest(t, filepath.Join(output, "sw-description")))
	if !strings.Contains(description, record) {
		t.Fatal("seed companion records changed")
	}
	runTest(t, output, nil, "openssl", "dgst", "-sha256", "-verify", public, "-signature", "sw-description.sig", "sw-description")
}

func TestValidAVBGeometry(t *testing.T) {
	for _, tc := range []struct {
		name            string
		raw, body, tail uint64
		valid           bool
	}{
		{"aligned", 512, 512, 512, true},
		{"adjacent tail", uint64(avbPartitionSize - 1024), 512, 512, true},
		{"empty raw", 0, 512, 512, false},
		{"empty body", 512, 0, 512, false},
		{"unaligned raw", 513, 512, 512, false},
		{"unaligned body", 512, 513, 512, false},
		{"short tail", 512, 512, 511, false},
		{"long tail", 512, 512, 1024, false},
		{"body overlaps tail", uint64(avbPartitionSize - 1024), 1024, 512, false},
		{"raw exceeds partition", uint64(avbPartitionSize), 512, 512, false},
		{"body exceeds partition", 512, uint64(avbPartitionSize), 512, false},
		{"uint64 raw", ^uint64(0) - 511, 512, 512, false},
		{"uint64 body", 512, ^uint64(0) - 511, 512, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidAVBGeometry(tc.raw, tc.body, tc.tail); got != tc.valid {
				t.Fatalf("ValidAVBGeometry(%d, %d, %d) = %t, want %t", tc.raw, tc.body, tc.tail, got, tc.valid)
			}
		})
	}
}

func TestAVBFileGeometry(t *testing.T) {
	for _, tc := range []struct {
		name            string
		raw, body, tail int64
		valid           bool
	}{
		{"generic bytes", 61952, 8192, 512, true},
		{"adjacent tail", avbPartitionSize - 1024, 512, 512, true},
		{"unaligned raw", 65535, 8192, 512, false},
		{"unaligned body", 4096, 513, 512, false},
		{"short tail", 4096, 512, 511, false},
		{"long tail", 4096, 512, 1024, false},
		{"body overlaps tail", avbPartitionSize - 1024, 1024, 512, false},
		{"raw exceeds partition", avbPartitionSize, 512, 512, false},
		{"body exceeds partition", 512, avbPartitionSize, 512, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "test.rootfs.ext4")
			for suffix, size := range map[string]int64{"": tc.raw, ".avb.body": tc.body, ".avb.tail": tc.tail} {
				writeTest(t, root+suffix, []byte("arbitrary bytes, no AVB profile"))
				if err := os.Truncate(root+suffix, size); err != nil {
					t.Fatal(err)
				}
			}
			dest := t.TempDir()
			payloads, err := copyAVBMetadata(root, root+".avb.body", root+".avb.tail", dest, filepath.Base(root))
			if !tc.valid {
				if err == nil {
					t.Fatal("accepted invalid file geometry")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if payloads[0].offset != tc.raw || payloads[1].offset != avbPartitionSize-512 {
				t.Fatalf("incorrect placement: %v", payloads)
			}
			for _, p := range payloads {
				if !bytes.Equal(readTest(t, filepath.Join(dest, p.name)), readTest(t, filepath.Join(filepath.Dir(root), p.name))) {
					t.Fatal("metadata bytes changed")
				}
			}
		})
	}
	for _, suffix := range []string{"", ".avb.body", ".avb.tail"} {
		for _, kind := range []string{"missing", "directory", "symlink", "empty", "wrong basename"} {
			if suffix == "" && kind == "wrong basename" {
				continue
			}
			t.Run(suffix+"/"+kind, func(t *testing.T) {
				root := filepath.Join(t.TempDir(), "test.rootfs.ext4")
				for _, part := range []string{"", ".avb.body", ".avb.tail"} {
					if part != suffix {
						writeTest(t, root+part, make([]byte, 512))
					}
				}
				body, tail := root+".avb.body", root+".avb.tail"
				var err error
				switch kind {
				case "directory":
					err = os.Mkdir(root+suffix, 0700)
				case "symlink":
					target := filepath.Join(t.TempDir(), "target")
					writeTest(t, target, make([]byte, 512))
					err = os.Symlink(target, root+suffix)
				case "empty":
					writeTest(t, root+suffix, nil)
				case "wrong basename":
					other := filepath.Join(filepath.Dir(root), "other"+suffix)
					writeTest(t, other, make([]byte, 512))
					if suffix == ".avb.body" {
						body = other
					} else {
						tail = other
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, err := copyAVBMetadata(root, body, tail, t.TempDir(), filepath.Base(root)); err == nil {
					t.Fatal("accepted invalid file")
				}
			})
		}
	}
}

func TestScopedDescriptionEditor(t *testing.T) {
	root := `{ device = "/dev/mmcblk0p7"; /* sha256 = "not a field"; */ properties = { note = "} \\\" {"; sha256 = "nested"; }; sha256 = "old"; filename = "old.rootfs.ext4.gz"; type = "raw"; }`
	payloads := []imagePayload{{name: "new.rootfs.ext4.gz", hash: "root-hash", compressed: true}, {name: "new.rootfs.ext4.avb.body", hash: "body-hash", offset: 4096}, {name: "new.rootfs.ext4.avb.tail", hash: "tail-hash", offset: avbPartitionSize - 512}}
	source := "# comment with images: ({\nsoftware={ images = ( // slot\n" + root + `); scripts: ({ sha256 = "unrelated"; }); uboot: ({name="rootpart";value="7";}); };`
	updated, _, err := rewriteDescription(source, "old.rootfs.ext4.gz", payloads)
	if err != nil {
		t.Fatal(err)
	}
	for _, untouched := range []string{`sha256 = "nested"`, `sha256 = "unrelated"`, `name="rootpart";value="7"`, "# comment with images: ({", "// slot", `/* sha256 = "not a field"; */`} {
		if !strings.Contains(updated, untouched) {
			t.Fatalf("lost %s", untouched)
		}
	}
	fields := imageFieldsTest(t, updated)
	if len(fields) != 3 || fields[0]["filename"] != payloads[0].name || fields[1]["offset"] != "4096" {
		t.Fatalf("wrong records %v", fields)
	}
	plain, owned, err := rewriteDescription(updated, "new.rootfs.ext4.gz", payloads[:1])
	if err != nil || !owned {
		t.Fatalf("remove owned pair: %v %v", owned, err)
	}
	if len(imageFieldsTest(t, plain)) != 1 {
		t.Fatal("owned pair not removed")
	}
	for _, tc := range []struct{ name, record string }{
		{"missing-device", strings.Replace(root, `device = "/dev/mmcblk0p7";`, "", 1)},
		{"duplicate-device", strings.Replace(root, "device =", `device="other"; device =`, 1)},
		{"missing-hash", strings.Replace(root, `sha256 = "old";`, "", 1)},
		{"duplicate-hash", strings.Replace(root, `sha256 = "old";`, `sha256="x";sha256="old";`, 1)},
		{"duplicate-filename", strings.Replace(root, `filename =`, `filename="duplicate"; filename =`, 1)},
		{"missing-type", strings.Replace(root, `type = "raw";`, "", 1)},
		{"wrong-type", strings.Replace(root, `type = "raw";`, `type = "ubivol";`, 1)},
		{"duplicate-root", root + "," + root},
		{"no-root", strings.Replace(root, "old.rootfs", "other.rootfs", 1)},
		{"partial-pair", root + `,{filename="old.rootfs.ext4.avb.body";sha256="body";device="/dev/mmcblk0p7";}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := rewriteDescription("images:("+tc.record+");", "old.rootfs.ext4.gz", payloads); err == nil {
				t.Fatal("accepted ambiguous description")
			}
		})
	}
	for _, field := range []string{`hook="check";`, `ref="other";`, `install-if-different=true;`, `install-if-higher=true;`, `encrypted=true;`, `ivt="value";`} {
		t.Run(field, func(t *testing.T) {
			record := strings.Replace(root, "{", "{"+field, 1)
			if _, _, err := rewriteDescription("images:("+record+");", "old.rootfs.ext4.gz", payloads); err == nil {
				t.Fatal("accepted conditional split")
			}
		})
	}
	body := metadataRecord(imagePayload{name: "old.rootfs.ext4.avb.body", hash: "b"}, "/dev/mmcblk0p7")
	tail := metadataRecord(imagePayload{name: "old.rootfs.ext4.avb.tail", hash: "t"}, "/dev/mmcblk0p7")
	for _, records := range []string{root + "," + body + "," + body + "," + tail, root + "," + tail + "," + body, root + "," + strings.Replace(body, "p7", "p8", 1) + "," + tail, body + "," + tail} {
		if _, _, err := rewriteDescription("images:("+records+");", "old.rootfs.ext4.gz", payloads); err == nil {
			t.Fatal("accepted invalid owned pair")
		}
	}
}

func TestInspect(t *testing.T) {
	if _, err := exec.LookPath("cpio"); err != nil {
		t.Skipf("requires cpio: %v", err)
	}
	workspace := t.TempDir()
	fixture := filepath.Join(workspace, "fixture")
	if err := os.Mkdir(fixture, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sw-description", "image.rootfs.ext4.gz", "other.rootfs.ext4.gz", "image.rootfs.ext4", "image.rootfs.ext4.avb.body", "image.rootfs.ext4.avb.tail", "fip-custom.bin", "fip-ota.bin"} {
		writeTest(t, filepath.Join(fixture, name), []byte("fixture"))
	}
	outside := filepath.Join(workspace, "outside")
	writeTest(t, outside, []byte("untouched"))
	for _, tc := range []struct {
		name, members string
		valid         bool
	}{
		{"unsigned-seed", "./sw-description\n./image.rootfs.ext4.gz\n", true},
		{"unsigned-seed-with-AVB", "./sw-description\n./image.rootfs.ext4.gz\n./image.rootfs.ext4.avb.body\n./image.rootfs.ext4.avb.tail\n", true},
		{"other-root-name", "sw-description\nother.rootfs.ext4.gz\n", true},
		{"fip-only", "sw-description\nfip-custom.bin\n", true},
		{"both", "sw-description\nimage.rootfs.ext4.gz\nfip-custom.bin\n", true},
		{"ambiguous-fip", "sw-description\nfip-custom.bin\nfip-ota.bin\n", false},
		{"missing-root", "sw-description\nimage.rootfs.ext4.avb.body\nimage.rootfs.ext4.avb.tail\n", false},
		{"opaque-plain-root-alongside-gzip", "sw-description\nimage.rootfs.ext4.gz\nimage.rootfs.ext4\n", true},
		{"duplicate-normalized", "sw-description\n./sw-description\nimage.rootfs.ext4.gz\n", false},
		{"parent-path", "sw-description\nimage.rootfs.ext4.gz\n../outside\n", false},
		{"absolute-path", "sw-description\nimage.rootfs.ext4.gz\n" + outside + "\n", false},
		{"missing-description", "image.rootfs.ext4.gz\n", false},
		{"ambiguous-root", "sw-description\nimage.rootfs.ext4.gz\nother.rootfs.ext4.gz\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := filepath.Join(workspace, tc.name+".swu")
			writeTest(t, archive, runTest(t, fixture, []byte(tc.members), "cpio", "-o", "-H", "newc"))
			components, err := Inspect(archive)
			if (err == nil) != tc.valid {
				t.Fatalf("seed valid=%v: %v", tc.valid, err)
			}
			if tc.valid {
				if components.Rootfs == "" && components.FIP == "" {
					t.Fatal("no components found")
				}
				for _, name := range []string{components.Rootfs, components.FIP} {
					if name != "" && !strings.Contains(tc.members, name+"\n") {
						t.Fatalf("component name not preserved: %q", name)
					}
				}
			}
		})
	}
}

func TestCPIORejectsLinksAndSpecialFiles(t *testing.T) {
	if _, err := exec.LookPath("cpio"); err != nil {
		t.Skipf("requires cpio: %v", err)
	}
	for _, kind := range []string{"signature-symlink", "directory-symlink", "hardlink", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			workspace := t.TempDir()
			fixture := filepath.Join(workspace, "fixture")
			destination := filepath.Join(workspace, "extracted")
			for _, dir := range []string{fixture, destination} {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			outside := filepath.Join(workspace, "outside")
			writeTest(t, outside, []byte("must remain unchanged"))
			writeTest(t, filepath.Join(fixture, "sw-description"), []byte("manifest"))
			writeTest(t, filepath.Join(fixture, "image.rootfs.ext4.gz"), []byte("rootfs"))
			member := "sw-description.sig"
			switch kind {
			case "signature-symlink":
				if err := os.Symlink(outside, filepath.Join(fixture, member)); err != nil {
					t.Fatal(err)
				}
			case "directory-symlink":
				member = "scripts"
				if err := os.Symlink(workspace, filepath.Join(fixture, member)); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(outside, filepath.Join(fixture, member)); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				runTest(t, fixture, nil, "mkfifo", member)
			}
			archive := filepath.Join(workspace, "seed.swu")
			writeTest(t, archive, runTest(t, fixture, []byte("sw-description\nimage.rootfs.ext4.gz\n"+member+"\n"), "cpio", "-o", "-H", "newc"))
			if _, err := listCPIO(archive); err == nil {
				t.Fatal("unsafe member accepted by preview inspection")
			}
			if _, err := Inspect(archive); err == nil {
				t.Fatal("unsafe member accepted by seed validation")
			}
			if _, err := extractCPIO(archive, destination); err == nil {
				t.Fatal("unsafe member extracted")
			}
			entries, err := os.ReadDir(destination)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatal("rejection happened after extraction started")
			}
			if got := string(readTest(t, outside)); got != "must remain unchanged" {
				t.Fatal("outside file changed")
			}
		})
	}
}

func fipFixture(t *testing.T) (Options, string) {
	t.Helper()
	for _, name := range []string{"cpio", "gzip", "openssl"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("requires %s", name)
		}
	}
	dir := t.TempDir()
	opts := Options{OldSWU: filepath.Join(dir, "seed.swu"), NewRootfs: filepath.Join(dir, "image.rootfs.ext4"), NewFIP: filepath.Join(dir, "input.bin"), PrivateKey: filepath.Join(dir, "private.pem"), Passfile: filepath.Join(dir, "pass"), Output: filepath.Join(dir, "output.swu")}
	opts.AVBBody, opts.AVBTail = opts.NewRootfs+".avb.body", opts.NewRootfs+".avb.tail"
	for _, path := range []string{opts.NewRootfs, opts.AVBBody, opts.AVBTail} {
		writeTest(t, path, bytes.Repeat([]byte{byte(len(path))}, 512))
	}
	writeTest(t, opts.NewFIP, []byte("explicit production FIP"))
	writeTest(t, opts.Passfile, []byte("secret"))
	runTest(t, dir, nil, "openssl", "genpkey", "-algorithm", "RSA", "-pkeyopt", "rsa_keygen_bits:2048", "-out", opts.PrivateKey)
	public := filepath.Join(dir, "public.pem")
	runTest(t, dir, nil, "openssl", "pkey", "-in", opts.PrivateKey, "-pubout", "-out", public)
	writeSeedFixture(t, opts, "both")
	return opts, public
}

func TestRequiredInputs(t *testing.T) {
	original, _ := fipFixture(t)
	for _, kind := range []string{"root-missing", "root-empty", "root-directory", "body-missing", "body-empty", "tail-missing", "tail-empty", "fip-empty", "fip-directory", "fip-symlink", "gzip", "wrong-body-name"} {
		t.Run(kind, func(t *testing.T) {
			opts := original
			empty := filepath.Join(t.TempDir(), "empty")
			writeTest(t, empty, nil)
			switch kind {
			case "root-missing":
				opts.NewRootfs = ""
			case "root-empty":
				opts.NewRootfs = empty
			case "root-directory":
				opts.NewRootfs = t.TempDir()
			case "body-missing":
				opts.AVBBody = ""
			case "body-empty":
				opts.AVBBody = empty
			case "tail-missing":
				opts.AVBTail = ""
			case "tail-empty":
				opts.AVBTail = empty
			case "fip-empty":
				opts.NewFIP = empty
			case "fip-directory":
				opts.NewFIP = t.TempDir()
			case "fip-symlink":
				opts.NewFIP = filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(original.NewFIP, opts.NewFIP); err != nil {
					t.Fatal(err)
				}
			case "gzip":
				opts.NewRootfs = filepath.Join(t.TempDir(), "image.rootfs.ext4")
				writeTest(t, opts.NewRootfs, runTest(t, "", readTest(t, original.NewRootfs), "gzip", "-c"))
			case "wrong-body-name":
				opts.AVBBody = original.AVBTail
			}
			if err := RebuildAll([]Options{opts}); err == nil {
				t.Fatal("run accepted invalid input")
			}
			if _, err := os.Stat(opts.Output); !os.IsNotExist(err) {
				t.Fatal("failure created output")
			}
		})
	}
}

func writeSeedFixture(t *testing.T, opts Options, kind string) {
	t.Helper()
	stage := t.TempDir()
	var records, members []string
	if kind != "fip" {
		root := "image.rootfs.ext4.gz"
		payload := runTest(t, stage, []byte("old rootfs"), "gzip", "-c")
		writeTest(t, filepath.Join(stage, root), payload)
		members = append(members, root)
		records = append(records, fmt.Sprintf(`{filename=%q;sha256="%x";device="/dev/rootfs";type="raw";compressed="zlib";}`, root, sha256.Sum256(payload)))
	}
	if kind != "rootfs" {
		payload := []byte("old firmware")
		writeTest(t, filepath.Join(stage, "fip-custom.bin"), payload)
		members = append(members, "fip-custom.bin")
		records = append(records, fmt.Sprintf(`{filename="fip-custom.bin";sha256="%x";device="/dev/firmware";type="raw";}`, sha256.Sum256(payload)))
	}
	script := []byte("#!/bin/sh\n# arbitrary seed policy\nexit 0\n")
	for _, name := range []string{"custom-hook.sh", "proto-swupdate", "opaque.bin"} {
		writeTest(t, filepath.Join(stage, name), script)
		if err := os.Chmod(filepath.Join(stage, name), 0751); err != nil {
			t.Fatal(err)
		}
		members = append(members, name)
	}
	description := fmt.Sprintf(`software = { version = "1"; images = (%s); scripts = ({filename="custom-hook.sh";sha256="%x";type="postinstall";}); };`, strings.Join(records, ","), sha256.Sum256(script))
	writeTest(t, filepath.Join(stage, "sw-description"), []byte(description))
	writeTest(t, filepath.Join(stage, "sw-description.sig"), []byte("old signature"))
	if err := os.Remove(opts.OldSWU); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := createCPIO(stage, opts.OldSWU, members); err != nil {
		t.Fatal(err)
	}
}

func TestRebuildAllPreservesSeedComponents(t *testing.T) {
	base, public := fipFixture(t)
	var batch []Options
	for i, kind := range []string{"rootfs", "both", "fip", "rootfs", "both"} {
		opts := base
		opts.OldSWU = filepath.Join(t.TempDir(), "arbitrary-seed.swu")
		opts.Output = filepath.Join(t.TempDir(), fmt.Sprintf("version-99-%d.swu", i))
		writeSeedFixture(t, opts, kind)
		if kind == "rootfs" {
			opts.NewFIP = ""
		}
		if kind == "fip" {
			opts.NewRootfs, opts.AVBBody, opts.AVBTail = "", "", ""
		}
		batch = append(batch, opts)
	}
	if err := RebuildAll(batch); err != nil {
		t.Fatal(err)
	}
	for _, opts := range batch {
		want, err := Inspect(opts.OldSWU)
		if err != nil {
			t.Fatal(err)
		}
		for repeat := range 2 {
			got, err := Inspect(opts.Output)
			if err != nil || got != want {
				t.Fatalf("membership changed: %+v want %+v: %v", got, want, err)
			}
			stage := t.TempDir()
			names, err := extractCPIO(opts.Output, stage)
			if err != nil {
				t.Fatal(err)
			}
			if len(names) < 2 || names[0] != "sw-description" || names[1] != "sw-description.sig" {
				t.Fatal("manifest/signature order changed", names)
			}
			runTest(t, stage, nil, "openssl", "dgst", "-sha256", "-verify", public, "-signature", "sw-description.sig", "sw-description")
			seedStage := t.TempDir()
			if _, err := extractCPIO(opts.OldSWU, seedStage); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"custom-hook.sh", "proto-swupdate", "opaque.bin"} {
				if !bytes.Equal(readTest(t, filepath.Join(stage, name)), readTest(t, filepath.Join(seedStage, name))) {
					t.Fatal("opaque member changed", name)
				}
				info, err := os.Stat(filepath.Join(stage, name))
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != 0751 {
					t.Fatal("member mode changed", name)
				}
			}
			if want.Rootfs != "" {
				if !bytes.Equal(runTest(t, stage, nil, "gzip", "-dc", want.Rootfs), readTest(t, opts.NewRootfs)) {
					t.Fatal("replacement rootfs mismatch")
				}
				root := strings.TrimSuffix(want.Rootfs, ".gz")
				for _, suffix := range []string{".avb.body", ".avb.tail"} {
					if !bytes.Equal(readTest(t, filepath.Join(stage, root+suffix)), readTest(t, opts.NewRootfs+suffix)) {
						t.Fatal("replacement AVB mismatch")
					}
				}
			}
			if want.FIP != "" && !bytes.Equal(readTest(t, filepath.Join(stage, want.FIP)), readTest(t, opts.NewFIP)) {
				t.Fatal("replacement FIP mismatch")
			}
			if repeat == 0 {
				opts.OldSWU, opts.Output = opts.Output, filepath.Join(t.TempDir(), "next-version.swu")
				if _, _, err := Preview(opts); err != nil {
					t.Fatal(err)
				}
				if err := Run(opts); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestRebuildAllOutputContract(t *testing.T) {
	if err := RebuildAll(nil); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"missing", "duplicate", "existing"} {
		t.Run(kind, func(t *testing.T) {
			opts, _ := fipFixture(t)
			source := readTest(t, opts.OldSWU)
			batch := []Options{opts, opts}
			batch[1].Output = filepath.Join(t.TempDir(), "second.swu")
			switch kind {
			case "missing":
				batch[1].Output = ""
			case "duplicate":
				batch[1].Output = opts.Output
			case "existing":
				writeTest(t, batch[1].Output, []byte("sentinel"))
			}
			if err := RebuildAll(batch); err == nil {
				t.Fatal("accepted invalid output contract")
			}
			if _, err := os.Stat(opts.Output); !os.IsNotExist(err) {
				t.Fatal("created first output")
			}
			if kind == "existing" && string(readTest(t, batch[1].Output)) != "sentinel" {
				t.Fatal("changed existing output")
			}
			if !bytes.Equal(source, readTest(t, opts.OldSWU)) {
				t.Fatal("changed source")
			}
		})
	}
}

func TestRebuildAllRemovesPartialOutputs(t *testing.T) {
	for _, kind := range []string{"signing", "corrupt-script", "missing-reference", "corrupt-opaque-reference"} {
		t.Run(kind, func(t *testing.T) {
			first, _ := fipFixture(t)
			second := first
			second.OldSWU = filepath.Join(t.TempDir(), "later.swu")
			second.Output = filepath.Join(t.TempDir(), "later-output.swu")
			writeSeedFixture(t, second, "both")
			if kind == "signing" {
				second.PrivateKey = filepath.Join(t.TempDir(), "invalid.pem")
				writeTest(t, second.PrivateKey, []byte("not a private key"))
			} else {
				stage := t.TempDir()
				names, err := extractCPIO(second.OldSWU, stage)
				if err != nil {
					t.Fatal(err)
				}
				description := string(readTest(t, filepath.Join(stage, "sw-description")))
				switch kind {
				case "corrupt-script":
					writeTest(t, filepath.Join(stage, "custom-hook.sh"), []byte("corrupt"))
				case "missing-reference":
					description = strings.ReplaceAll(description, "fip-custom.bin", "unrecognized.bin")
				case "corrupt-opaque-reference":
					description = strings.Replace(description, "version =", `files=({filename="opaque.bin";sha256="`+strings.Repeat("0", 64)+`";}); version =`, 1)
				}
				writeTest(t, filepath.Join(stage, "sw-description"), []byte(description))
				if err := os.Remove(second.OldSWU); err != nil {
					t.Fatal(err)
				}
				if _, err := createCPIO(stage, second.OldSWU, names[2:]); err != nil {
					t.Fatal(err)
				}
			}
			before := [][]byte{readTest(t, first.OldSWU), readTest(t, second.OldSWU)}
			if err := RebuildAll([]Options{first, second}); err == nil {
				t.Fatal("accepted invalid later archive")
			}
			for i, opts := range []Options{first, second} {
				if _, err := os.Stat(opts.Output); !os.IsNotExist(err) {
					t.Fatal("retained partial output", opts.Output)
				}
				if !bytes.Equal(before[i], readTest(t, opts.OldSWU)) {
					t.Fatal("changed source archive")
				}
			}
		})
	}
}

func TestVerifyRejectsGenericTampering(t *testing.T) {
	opts, _ := fipFixture(t)
	if err := RebuildAll([]Options{opts}); err != nil {
		t.Fatal(err)
	}
	components, err := Inspect(opts.Output)
	if err != nil {
		t.Fatal(err)
	}
	root := strings.TrimSuffix(components.Rootfs, ".gz")
	expected := map[string]string{
		root:               fmt.Sprintf("%x", sha256.Sum256(readTest(t, opts.NewRootfs))),
		root + ".avb.body": fmt.Sprintf("%x", sha256.Sum256(readTest(t, opts.AVBBody))),
		root + ".avb.tail": fmt.Sprintf("%x", sha256.Sum256(readTest(t, opts.AVBTail))),
	}
	fipHash := fmt.Sprintf("%x", sha256.Sum256(readTest(t, opts.NewFIP)))
	for _, kind := range []string{"root-bytes", "fip-bytes", "body-bytes", "tail-bytes", "script-bytes", "offset", "type", "compression", "adjacency", "signature-order"} {
		t.Run(kind, func(t *testing.T) {
			stage := t.TempDir()
			names, err := extractCPIO(opts.Output, stage)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(stage, "sw-description")
			description := string(readTest(t, path))
			switch kind {
			case "root-bytes":
				writeTest(t, filepath.Join(stage, components.Rootfs), runTest(t, stage, []byte("wrong rootfs"), "gzip", "-c"))
			case "fip-bytes":
				writeTest(t, filepath.Join(stage, components.FIP), []byte("wrong FIP"))
			case "body-bytes":
				writeTest(t, filepath.Join(stage, root+".avb.body"), bytes.Repeat([]byte{0xee}, 512))
			case "tail-bytes":
				writeTest(t, filepath.Join(stage, root+".avb.tail"), []byte("short"))
			case "script-bytes":
				writeTest(t, filepath.Join(stage, "custom-hook.sh"), []byte("wrong script"))
			case "offset":
				description = strings.Replace(description, `offset = "0"`, `offset = "512"`, 1)
			case "type":
				description = strings.Replace(description, `type="raw"`, `type="file"`, 1)
			case "compression":
				description = strings.Replace(description, `"zlib"`, `"gzip"`, 1)
			case "adjacency":
				description = strings.NewReplacer(root+".avb.body", root+".avb.tail", root+".avb.tail", root+".avb.body").Replace(description)
			case "signature-order":
				names[0], names[1] = names[1], names[0]
			}
			writeTest(t, path, []byte(description))
			archive := filepath.Join(t.TempDir(), "tampered.swu")
			writeTest(t, archive, runTest(t, stage, []byte(strings.Join(names, "\n")+"\n"), "cpio", "-o", "-H", "newc"))
			if err := verify(archive, components, expected, fipHash); err == nil {
				t.Fatal("accepted tampered SWU", kind)
			}
		})
	}
}

func TestPreviewRejectsComponentMismatch(t *testing.T) {
	for _, kind := range []string{"rootfs", "fip", "both"} {
		opts, _ := fipFixture(t)
		writeSeedFixture(t, opts, kind)
		if kind == "rootfs" {
			opts.NewFIP = ""
		}
		if kind == "fip" {
			opts.NewRootfs, opts.AVBBody, opts.AVBTail = "", "", ""
		}
		if _, _, err := Preview(opts); err != nil {
			t.Fatal(err)
		}
		for _, component := range []string{"rootfs", "fip"} {
			bad := opts
			if component == "rootfs" {
				if bad.NewRootfs == "" {
					bad.NewRootfs = "extra.ext4"
				} else {
					bad.NewRootfs, bad.AVBBody, bad.AVBTail = "", "", ""
				}
			} else if bad.NewFIP == "" {
				bad.NewFIP = "extra.bin"
			} else {
				bad.NewFIP = ""
			}
			if _, _, err := Preview(bad); err == nil {
				t.Fatal("preview accepted component mismatch", kind, component)
			}
			if err := Run(bad); err == nil {
				t.Fatal("run accepted component mismatch", kind, component)
			}
			if _, err := os.Stat(bad.Output); !os.IsNotExist(err) {
				t.Fatal("component mismatch created output")
			}
		}
	}
}

func TestRebuildAllUpdatesEverySidecarReference(t *testing.T) {
	opts, _ := fipFixture(t)
	if err := RebuildAll([]Options{opts}); err != nil {
		t.Fatal(err)
	}
	stage := t.TempDir()
	names, err := extractCPIO(opts.Output, stage)
	if err != nil {
		t.Fatal(err)
	}
	body := "image.rootfs.ext4.avb.body"
	previousHash := fmt.Sprintf("%x", sha256.Sum256(readTest(t, opts.AVBBody)))
	path := filepath.Join(stage, "sw-description")
	extra := fmt.Sprintf(`audit={filename=%q;sha256=%q;type="raw";offset="512";};`, body, previousHash)
	source := strings.Replace(string(readTest(t, path)), "software = {", "software = {"+extra, 1)
	writeTest(t, path, []byte(source))
	opts.OldSWU = filepath.Join(t.TempDir(), "seed.swu")
	if _, err := createCPIO(stage, opts.OldSWU, names[2:]); err != nil {
		t.Fatal(err)
	}
	replacement := bytes.Repeat([]byte("b"), 512)
	writeTest(t, opts.AVBBody, replacement)
	opts.Output = filepath.Join(t.TempDir(), "rebuilt.swu")
	if err := RebuildAll([]Options{opts}); err != nil {
		t.Fatal(err)
	}
	output := t.TempDir()
	if _, err := extractCPIO(opts.Output, output); err != nil {
		t.Fatal(err)
	}
	tokens, err := configTokens(string(readTest(t, filepath.Join(output, "sw-description"))))
	if err != nil {
		t.Fatal(err)
	}
	references := 0
	for i, token := range tokens {
		if token.text != "{" {
			continue
		}
		record, err := recordFields(tokens, i)
		if err != nil {
			t.Fatal(err)
		}
		name, _ := fieldString(tokens, record, "filename")
		if name != body {
			continue
		}
		references++
		hash, err := fieldString(tokens, record, "sha256")
		if err != nil || hash != fmt.Sprintf("%x", sha256.Sum256(replacement)) {
			t.Fatalf("sidecar reference has stale hash %q: %v", hash, err)
		}
	}
	if references != 2 {
		t.Fatalf("sidecar references lost: %d", references)
	}
}

func TestPackingPreservesExistingDestinations(t *testing.T) {
	stage := t.TempDir()
	writeTest(t, filepath.Join(stage, "sw-description"), []byte("manifest"))
	writeTest(t, filepath.Join(stage, "sw-description.sig"), []byte("signature"))
	for _, symlink := range []bool{false, true} {
		t.Run(fmt.Sprintf("symlink=%t", symlink), func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "existing.swu")
			sentinel := []byte("existing destination")
			writeTest(t, target, sentinel)
			output := target
			if symlink {
				output = filepath.Join(t.TempDir(), "output.swu")
				if err := os.Symlink(target, output); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := createCPIO(stage, output, nil); err == nil {
				t.Fatal("packed over existing destination")
			}
			if !bytes.Equal(readTest(t, target), sentinel) {
				t.Fatal("changed existing destination bytes")
			}
		})
	}
}

func TestRebuildAllPreservesLateDestination(t *testing.T) {
	opts, _ := fipFixture(t)
	openssl, err := exec.LookPath("openssl")
	if err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = dgst ]; then printf 'late destination' > %q; fi\nexec %q \"$@\"\n", opts.Output, openssl)
	if err := os.WriteFile(filepath.Join(tools, "openssl"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := RebuildAll([]Options{opts}); err == nil {
		t.Fatal("accepted destination created after preflight")
	}
	if got := string(readTest(t, opts.Output)); got != "late destination" {
		t.Fatalf("changed or removed foreign destination: %q", got)
	}
}

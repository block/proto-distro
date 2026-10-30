package distroinject

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"proto-distro/tools/distro/internal/config"
)

var legacyTestSuffixes = []string{".avb", ".avb.footer"}
var sidecarTestSuffixes = []string{".avb.body", ".avb.tail", ".avb", ".avb.footer"}

func testDocker(t *testing.T) {
	t.Helper()
	shim, err := os.ReadFile("testdata/docker")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), shim, 0755); err != nil {
		t.Fatal(err)
	}
	// Opaque placement fixtures: Go consumers must not decode AVB metadata.
	for name, data := range map[string]string{"BODY": strings.Repeat("b", 4096), "TAIL": strings.Repeat("t", 512)} {
		path := filepath.Join(bin, name)
		writeTestFile(t, path, data)
		t.Setenv("DISTRO_TEST_"+name, path)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func testConfig(t *testing.T) (*config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{Defaults: config.Defaults{Variant: "release", Machine: "c3-p1"}}
	cfg.Docker.Injector.Image = "test-injector"
	key := filepath.Join(dir, "c3")
	writeTestFile(t, key, "c3")
	cfg.AVB = map[string]config.AVBKeys{"c3-p1": {PrivateKey: key, PublicKey: key, AVBPublicKey: key}}
	return cfg, filepath.Join(dir, "distro.yaml")
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func alignedTestData(data string) string {
	return data + strings.Repeat("\x00", (512-len(data)%512)%512)
}

func testInput(t *testing.T, files map[string]string, zipped bool) string {
	t.Helper()
	input := t.TempDir()
	if zipped {
		input = filepath.Join(input, "arbitrary-name.zip")
		if err := createTestZip(input, files); err != nil {
			t.Fatal(err)
		}
	} else {
		for name, content := range files {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(input, name)), 0700); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(input, name), content)
		}
	}
	return input
}

func testFiles(t *testing.T, path string, zipped bool) map[string]string {
	t.Helper()
	if zipped {
		files, err := readTestZip(path)
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	files := map[string]string{}
	err := filepath.Walk(path, func(member string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			name, err := filepath.Rel(path, member)
			if err != nil {
				return err
			}
			files[filepath.ToSlash(name)] = readTestFile(t, member)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func assertNoRecoveryCopies(t *testing.T, files map[string]string) {
	t.Helper()
	for name := range files {
		if strings.HasSuffix(name, ".original") {
			t.Errorf("unexpected recovery copy %s", name)
		}
	}
}

func assertNoAVBStage(t *testing.T, dir string) {
	t.Helper()
	for _, pattern := range []string{".distro-avb-*", ".distro-swu-*"} {
		matches, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil || len(matches) != 0 {
			t.Fatalf("private staging leaked: %v, %v", matches, err)
		}
	}
}

func assertChecksums(t *testing.T, files map[string]string) {
	t.Helper()
	for name, content := range files {
		if name == "sha.txt" {
			continue
		}
		want := fmt.Sprintf("%x  %s\n", sha256.Sum256([]byte(content)), name)
		if !strings.Contains(files["sha.txt"], want) {
			t.Errorf("sha.txt missing final checksum for %s", name)
		}
	}
}

func TestArtifactPipeline(t *testing.T) {
	for _, zipped := range []bool{false, true} {
		for _, tc := range []struct {
			name, variant, version    string
			packages, oldPair, legacy bool
		}{
			{name: "packages and stale pair", variant: "release", packages: true, oldPair: true, legacy: true},
			{name: "legacy only", variant: "release", legacy: true},
			{name: "fresh release version only", variant: "release", version: "2.0.0"},
			{name: "AVB only", variant: "release"},
			{name: "mfg version only", variant: "mfg", version: "2.0.0"},
			{name: "mfg AVB only", variant: "mfg"},
		} {
			t.Run(fmt.Sprintf("%s/zip=%t", tc.name, zipped), func(t *testing.T) {
				testDocker(t)
				cfg, configPath := testConfig(t)
				root := "miner-image-" + tc.variant + "-c3-p1-1.0.0.rootfs.ext4"
				initial := map[string]string{root: alignedTestData("plain\n"), "sha.txt": "stale checksum\n"}
				for _, suffix := range legacyTestSuffixes {
					initial["unrelated"+suffix] = "unrelated " + suffix
					initial[root+suffix+".keep"] = "unrecognized " + suffix
					if tc.legacy {
						initial[root+suffix] = "old " + suffix
					}
				}
				if tc.oldPair {
					initial[root+".avb.body"] = "old body"
					initial[root+".avb.tail"] = "old tail"
				}
				input := testInput(t, initial, zipped)
				params := Params{Config: cfg, ConfigPath: configPath, InputPath: input, Version: tc.version}
				want := initial[root]
				if tc.packages {
					ipk := filepath.Join(t.TempDir(), "marker_all_1.0.0.ipk")
					writeTestFile(t, ipk, "injected marker\n")
					params.IPKPaths = []string{ipk}
					want += "injected marker\n"
				}
				if tc.version != "" {
					want += "VERSION=" + tc.version + "\n"
				}
				want = alignedTestData(want)
				if _, err := RunWithConfig(context.Background(), params); err != nil {
					t.Fatal(err)
				}
				files := testFiles(t, input, zipped)
				if files[root] != want {
					t.Fatal("plain rootfs differs from final injected bytes")
				}
				{
					for _, part := range []string{"body", "tail"} {
						if files[root+".avb."+part] != readTestFile(t, os.Getenv("DISTRO_TEST_"+strings.ToUpper(part))) {
							t.Fatalf("generated %s not published verbatim", part)
						}
					}
				}
				for _, suffix := range legacyTestSuffixes {
					if files[root+suffix] != initial[root+suffix] {
						t.Fatalf("legacy sidecar %s changed", suffix)
					}
				}
				if tc.packages && files["application_manifest.txt"] != "marker 1.0.0\n" {
					t.Fatalf("manifest = %q", files["application_manifest.txt"])
				}
				assertNoRecoveryCopies(t, files)
				if !zipped {
					assertNoAVBStage(t, input)
				}
				for name, content := range initial {
					if strings.HasPrefix(name, "unrelated") || strings.HasSuffix(name, ".keep") {
						if files[name] != content {
							t.Fatalf("removed unrelated %s", name)
						}
					}
				}
				assertChecksums(t, files)
			})
		}
	}
}

func TestSignerFailurePreservesUnfinalizedArtifacts(t *testing.T) {
	for _, zipped := range []bool{false, true} {
		t.Run(fmt.Sprintf("zip=%t", zipped), func(t *testing.T) {
			testDocker(t)
			t.Setenv("DISTRO_TEST_SIGNER_FAIL", "1")
			cfg, configPath := testConfig(t)
			root := "miner-image-release-c3-p1-1.rootfs.ext4"
			initial := map[string]string{root: alignedTestData("plain\n"), "sha.txt": "old checksum"}
			for _, suffix := range append([]string{".avb.body", ".avb.tail"}, legacyTestSuffixes...) {
				initial[root+suffix] = "old " + suffix
			}
			input := testInput(t, initial, zipped)
			var original string
			if zipped {
				original = readTestFile(t, input)
			}
			_, err := RunWithConfig(context.Background(), Params{Config: cfg, ConfigPath: configPath, InputPath: input, Version: "2"})
			if err == nil || !strings.Contains(err.Error(), "creating AVB") {
				t.Fatalf("signer failure = %v", err)
			}
			if zipped && readTestFile(t, input) != original {
				t.Fatal("failed ZIP changed")
			}
			files := testFiles(t, input, zipped)
			for name, content := range initial {
				if name != root && files[name] != content {
					t.Errorf("%s finalized after signer failure", name)
				}
			}
			assertNoRecoveryCopies(t, files)
			if !zipped {
				if files[root] != alignedTestData(initial[root]+"VERSION=2\n") {
					t.Fatal("fixture did not reach post-mutation signer failure")
				}
				assertNoAVBStage(t, input)
			}
		})
	}
}

func TestUnsignedNoWorkPreservesZIP(t *testing.T) {
	testDocker(t)
	t.Setenv("DISTRO_TEST_NO_DOCKER", "1")
	cfg, configPath := testConfig(t)
	cfg.Artifacts.LocalFiles = []string{"missing/proto-swupdate"}
	for _, root := range []string{"generic.rootfs.ext4"} {
		t.Run(root, func(t *testing.T) {
			input := testInput(t, map[string]string{root: "plain", "sha.txt": "unchanged"}, true)
			before := readTestFile(t, input)
			if _, err := RunWithConfig(context.Background(), Params{Config: cfg, ConfigPath: configPath, InputPath: input}); err != nil {
				t.Fatal(err)
			}
			if readTestFile(t, input) != before {
				t.Fatal("no-work ZIP changed")
			}
		})
	}
}

func TestUnsafeAVBOutputRejectedBeforeMutation(t *testing.T) {
	for _, suffix := range sidecarTestSuffixes {
		for _, kind := range []string{"symlink", "directory", "rootfs hardlink", "external hardlink", "output hardlink"} {
			t.Run(suffix+"/"+kind, func(t *testing.T) {
				testDocker(t)
				t.Setenv("DISTRO_TEST_NO_DOCKER", "1")
				cfg, configPath := testConfig(t)
				dir := t.TempDir()
				root := filepath.Join(dir, "miner-image-release-c3-p1-1.rootfs.ext4")
				writeTestFile(t, root, alignedTestData("plain"))
				external := filepath.Join(t.TempDir(), "external")
				writeTestFile(t, external, "external")
				otherSuffix := ".avb.body"
				if suffix == otherSuffix {
					otherSuffix = ".avb.tail"
				}
				writeTestFile(t, root+otherSuffix, "old signed output")
				var err error
				switch kind {
				case "symlink":
					err = os.Symlink(external, root+suffix)
				case "directory":
					err = os.Mkdir(root+suffix, 0700)
				case "rootfs hardlink":
					err = os.Link(root, root+suffix)
				case "external hardlink":
					err = os.Link(external, root+suffix)
				case "output hardlink":
					err = os.Link(root+otherSuffix, root+suffix)
				}
				if err != nil {
					t.Fatal(err)
				}
				_, err = RunWithConfig(context.Background(), Params{Config: cfg, ConfigPath: configPath, InputPath: dir, Version: "2"})
				if err == nil || !strings.Contains(err.Error(), "AVB output") {
					t.Fatalf("unsafe output rejection = %v", err)
				}
				for path, want := range map[string]string{root: alignedTestData("plain"), external: "external", root + otherSuffix: "old signed output"} {
					if readTestFile(t, path) != want {
						t.Fatalf("%s modified", path)
					}
				}
				assertNoRecoveryCopies(t, testFiles(t, dir, false))
				assertNoAVBStage(t, dir)
			})
		}
	}
}

func TestSidecarUnsignedInputRejectedBeforeMutation(t *testing.T) {
	for _, root := range []string{"generic.rootfs.ext4"} {
		for _, suffix := range sidecarTestSuffixes {
			for _, zipped := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/zip=%t", root, suffix, zipped), func(t *testing.T) {
					testDocker(t)
					t.Setenv("DISTRO_TEST_NO_DOCKER", "1")
					cfg, configPath := testConfig(t)
					initial := map[string]string{root: alignedTestData("plain"), root + suffix: "old sidecar", "sha.txt": "old checksums"}
					input := testInput(t, initial, zipped)
					if _, err := RunWithConfig(context.Background(), Params{Config: cfg, ConfigPath: configPath, InputPath: input, Version: "2"}); err == nil || !strings.Contains(err.Error(), "not identified as release") {
						t.Fatalf("unidentified signed input rejection = %v", err)
					}
					files := testFiles(t, input, zipped)
					if len(files) != len(initial) {
						t.Fatalf("rejected input gained artifacts: %v", files)
					}
					for name, want := range initial {
						if files[name] != want {
							t.Errorf("rejected input changed %s", name)
						}
					}
				})
			}
		}
	}
}

func TestInvalidSignerOutputStopsFinalization(t *testing.T) {
	cases := []struct{ name, variable, value string }{
		{"missing body", "DISTRO_TEST_NO_BODY", "1"},
		{"missing tail", "DISTRO_TEST_NO_TAIL", "1"},
		{"historical full only", "DISTRO_TEST_FULL_ONLY", "1"},
		{"historical compact only", "DISTRO_TEST_COMPACT_ONLY", "1"},
		{"cached dual output", "DISTRO_TEST_DUAL_OUTPUT", "1"},
	}
	for _, part := range []string{"BODY", "TAIL"} {
		for _, kind := range []string{"empty", "directory", "symlink", "unaligned", "oversized"} {
			cases = append(cases, struct{ name, variable, value string }{kind + " " + part, "DISTRO_TEST_" + part + "_KIND", kind})
		}
	}
	for _, tc := range cases {
		for _, zipped := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/zip=%t", tc.name, zipped), func(t *testing.T) {
				testDocker(t)
				t.Setenv(tc.variable, tc.value)
				cfg, configPath := testConfig(t)
				root := "miner-image-release-c3-p1-1.rootfs.ext4"
				initial := map[string]string{root: alignedTestData("plain\n"), "sha.txt": "old checksum"}
				for _, suffix := range append([]string{".avb.body", ".avb.tail"}, legacyTestSuffixes...) {
					initial[root+suffix] = "old " + suffix
				}
				input := testInput(t, initial, zipped)
				var original string
				if zipped {
					original = readTestFile(t, input)
				}
				_, err := RunWithConfig(context.Background(), Params{Config: cfg, ConfigPath: configPath, InputPath: input})
				if err == nil || !strings.Contains(err.Error(), "rebuild the injector") {
					t.Fatalf("invalid signer output error = %v", err)
				}
				files := testFiles(t, input, zipped)
				for name, want := range initial {
					if files[name] != want {
						t.Errorf("%s changed after incomplete signing", name)
					}
				}
				assertNoRecoveryCopies(t, files)
				if zipped {
					if readTestFile(t, input) != original {
						t.Fatal("invalid signer output changed ZIP")
					}
				} else {
					assertNoAVBStage(t, input)
				}
			})
		}
	}
}

func TestSWUFailurePreservesLegacyOutputs(t *testing.T) {
	for _, zipped := range []bool{false, true} {
		t.Run(fmt.Sprintf("zip=%t", zipped), func(t *testing.T) {
			testDocker(t)
			cfg, configPath := testConfig(t)
			root := "miner-image-release-c3-p1-1.rootfs.ext4"
			initial := map[string]string{root: alignedTestData("plain"), strings.TrimSuffix(root, ".rootfs.ext4") + ".swu": "invalid archive", "proto-swupdate": "raw updater", "sha.txt": "old checksum"}
			for _, suffix := range legacyTestSuffixes {
				initial[root+suffix] = "old " + suffix
			}
			input := testInput(t, initial, zipped)
			_, err := RunWithConfig(context.Background(), Params{Config: cfg, ConfigPath: configPath, InputPath: input})
			if err == nil {
				t.Fatal("accepted invalid seed archive")
			}
			files := testFiles(t, input, zipped)
			assertNoRecoveryCopies(t, files)
			for name, want := range initial {
				if files[name] != want {
					t.Errorf("SWU failure changed %s", name)
				}
			}
			if !zipped {
				for _, part := range []string{"body", "tail"} {
					if _, exists := files[root+".avb."+part]; exists {
						t.Fatalf("invalid seed generated %s before preflight", part)
					}
				}
				assertNoAVBStage(t, input)
			}
		})
	}
}

func runPipelineCommand(t *testing.T, dir, input, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(input)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s: %v: %s", name, err, stderr.String())
	}
	return string(output)
}

func pipelineKeys(t *testing.T) (string, string, string) {
	t.Helper()
	for _, name := range []string{"gzip", "cpio", "openssl"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("requires %s: %v", name, err)
		}
	}
	keys := t.TempDir()
	privateKey, passfile := filepath.Join(keys, "private.pem"), filepath.Join(keys, "passphrase")
	writeTestFile(t, passfile, "test-passphrase\n")
	runPipelineCommand(t, keys, "", "openssl", "genpkey", "-algorithm", "RSA", "-pkeyopt", "rsa_keygen_bits:2048", "-aes-256-cbc", "-pass", "file:"+passfile, "-out", privateKey)
	publicKey := filepath.Join(keys, "public.pem")
	runPipelineCommand(t, keys, "", "openssl", "pkey", "-in", privateKey, "-passin", "file:"+passfile, "-pubout", "-out", publicKey)
	return privateKey, passfile, publicKey
}

var pipelineOpaque = map[string]struct {
	data string
	mode os.FileMode
}{
	"custom-install.sh": {"#!/bin/sh\nprintf 'seed policy\\n'\n", 0750},
	"proto-swupdate":    {"opaque embedded updater, independent of exported binary", 0751},
	"opaque-member":     {"archive-only bytes", 0604},
}

func pipelineSeed(t *testing.T, root string, fip bool) string {
	t.Helper()
	seed := t.TempDir()
	names := []string{"./sw-description", "./sw-description.sig"}
	var images []string
	if root != "" {
		payload := runPipelineCommand(t, seed, "old raw", "gzip", "-c")
		writeTestFile(t, filepath.Join(seed, root+".gz"), payload)
		names = append(names, "././"+root+".gz")
		images = append(images, fmt.Sprintf("{ filename = %q; device = \"/dev/custom-root\"; type = \"raw\"; compressed = \"zlib\"; sha256 = \"%x\"; }", root+".gz", sha256.Sum256([]byte(payload))))
	}
	if fip {
		writeTestFile(t, filepath.Join(seed, "fip-custom.bin"), "old FIP")
		names = append(names, "./fip-custom.bin")
		images = append(images, fmt.Sprintf("{ filename = \"fip-custom.bin\"; device = \"/dev/custom-firmware\"; type = \"raw\"; sha256 = \"%x\"; }", sha256.Sum256([]byte("old FIP"))))
	}
	for _, name := range []string{"custom-install.sh", "proto-swupdate", "opaque-member"} {
		payload := pipelineOpaque[name]
		writeTestFile(t, filepath.Join(seed, name), payload.data)
		if err := os.Chmod(filepath.Join(seed, name), payload.mode); err != nil {
			t.Fatal(err)
		}
		names = append(names, "./"+name)
	}
	description := fmt.Sprintf("software = { version = \"seed\"; custom = { update = { images: (%s); scripts: ({ filename = \"custom-install.sh\"; type = \"postinstall\"; sha256 = \"%x\"; }); }; }; };", strings.Join(images, ", "), sha256.Sum256([]byte(pipelineOpaque["custom-install.sh"].data)))
	writeTestFile(t, filepath.Join(seed, "sw-description"), description)
	writeTestFile(t, filepath.Join(seed, "sw-description.sig"), "old signature")
	return runPipelineCommand(t, seed, strings.Join(names, "\n")+"\n", "cpio", "-o", "-H", "newc")
}

func assertPipelineArchive(t *testing.T, archive, publicKey, embeddedRoot string, fip bool, files map[string]string, outerRoot, fipBytes string) {
	t.Helper()
	unpacked := t.TempDir()
	runPipelineCommand(t, unpacked, archive, "cpio", "-idm")
	runPipelineCommand(t, unpacked, "", "openssl", "dgst", "-sha256", "-verify", publicKey, "-signature", "sw-description.sig", "sw-description")
	names := strings.Fields(runPipelineCommand(t, unpacked, archive, "cpio", "-it"))
	wantNames := map[string]bool{"sw-description": true, "sw-description.sig": true}
	for name, payload := range pipelineOpaque {
		wantNames[name] = true
		path := filepath.Join(unpacked, name)
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != payload.mode {
			t.Fatalf("opaque %s mode differs: %v", name, err)
		}
		if readTestFile(t, path) != payload.data {
			t.Fatalf("opaque %s bytes differ", name)
		}
	}
	if embeddedRoot != "" {
		wantNames[embeddedRoot+".gz"] = true
		wantNames[embeddedRoot+".avb.body"] = true
		wantNames[embeddedRoot+".avb.tail"] = true
		if runPipelineCommand(t, unpacked, "", "gzip", "-dc", embeddedRoot+".gz") != files[outerRoot] {
			t.Fatal("SWU rootfs differs from final raw image")
		}
		for _, part := range []string{"body", "tail"} {
			want := readTestFile(t, os.Getenv("DISTRO_TEST_"+strings.ToUpper(part)))
			if files[outerRoot+".avb."+part] != want || readTestFile(t, filepath.Join(unpacked, embeddedRoot+".avb."+part)) != want {
				t.Fatalf("%s differs across signer, artifact and SWU", part)
			}
		}
	}
	if fip {
		wantNames["fip-board-optee.bin"] = true
		if readTestFile(t, filepath.Join(unpacked, "fip-board-optee.bin")) != fipBytes {
			t.Fatal("SWU FIP differs from exported FIP")
		}
	}
	if len(names) != len(wantNames) || len(names) < 2 || names[0] != "sw-description" || names[1] != "sw-description.sig" {
		t.Fatalf("archive membership/order differs: %v", names)
	}
	for i, name := range names {
		if !wantNames[name] {
			t.Fatalf("unexpected archive member %s", name)
		}
		delete(wantNames, name)
		if name == embeddedRoot+".gz" && (i+2 >= len(names) || names[i+1] != embeddedRoot+".avb.body" || names[i+2] != embeddedRoot+".avb.tail") {
			t.Fatal("rootfs sidecars not adjacent and ordered")
		}
	}
	if len(wantNames) != 0 {
		t.Fatalf("missing archive members: %v", wantNames)
	}
	description := readTestFile(t, filepath.Join(unpacked, "sw-description"))
	records := regexp.MustCompile(`(?s)\{[^{}]*filename\s*=\s*"([^"]+)"[^{}]*\}`).FindAllStringSubmatch(description, -1)
	wantRecords := 1
	if embeddedRoot != "" {
		wantRecords += 3
	}
	if fip {
		wantRecords++
	}
	if len(records) != wantRecords {
		t.Fatalf("manifest records = %d, want %d", len(records), wantRecords)
	}
	for _, record := range records {
		name := record[1]
		payload := readTestFile(t, filepath.Join(unpacked, name))
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(payload)))
		if !strings.Contains(record[0], `sha256 = "`+hash+`"`) {
			t.Fatalf("manifest hash differs for %s", name)
		}
		offset := ""
		switch name {
		case embeddedRoot + ".gz":
			offset = "0"
			if !strings.Contains(record[0], `compressed = "zlib"`) {
				t.Fatal("rootfs compression type differs")
			}
		case embeddedRoot + ".avb.body":
			offset = fmt.Sprint(len(files[outerRoot]))
		case embeddedRoot + ".avb.tail":
			offset = fmt.Sprint((1 << 30) - 512)
		}
		if offset != "" && (!strings.Contains(record[0], `offset = "`+offset+`"`) || !strings.Contains(record[0], `type = "raw"`)) {
			t.Fatalf("manifest offset/type differs for %s: %s", name, record[0])
		}
	}
	if !strings.Contains(description, `/dev/custom-root`) && embeddedRoot != "" || !strings.Contains(description, `/dev/custom-firmware`) && fip {
		t.Fatal("seed device policy changed")
	}
}

func TestSWUDiscoveredSeedsRebuiltInPlace(t *testing.T) {
	privateKey, passfile, publicKey := pipelineKeys(t)
	embeddedRoot := "seed-version.rootfs.ext4"
	seeds := map[string]struct {
		root string
		fip  bool
	}{
		"miner-image-release-c3-p1-1.swu": {embeddedRoot, false},
		"combined-arbitrary.swu":          {embeddedRoot, true},
		"third-firmware-only.swu":         {"", true},
		"first/same.swu":                  {embeddedRoot, false},
		"second/same.swu":                 {embeddedRoot, true},
	}
	archives := map[string]string{}
	for name, seed := range seeds {
		archives[name] = pipelineSeed(t, seed.root, seed.fip)
	}
	for _, zipped := range []bool{false, true} {
		t.Run(fmt.Sprintf("zip=%t", zipped), func(t *testing.T) {
			testDocker(t)
			cfg, configPath := testConfig(t)
			cfg.Artifacts.LocalFiles = []string{"configured-updater"}
			localUpdater := config.ResolvePath(configPath, cfg.Artifacts.LocalFiles[0])
			writeTestFile(t, localUpdater, "first local updater")
			root := "miner-image-release-c3-p1-2.rootfs.ext4"
			fipPath := "fip/fip-board-optee.bin"
			initial := map[string]string{root: alignedTestData("plain"), "sha.txt": "old checksum", fipPath: "deployed FIP", "proto-swupdate": "outer updater unchanged"}
			legacyRoot := root
			for _, suffix := range legacyTestSuffixes {
				initial[root+suffix] = "legacy sentinel " + suffix
			}
			for name, archive := range archives {
				initial[name] = archive
			}
			input := testInput(t, initial, zipped)
			for iteration := range 2 {
				params := Params{Config: cfg, ConfigPath: configPath, InputPath: input, Version: fmt.Sprint(iteration + 3), PrivateKeyOverride: privateKey, PassfileOverride: passfile}
				if _, err := RunWithConfig(context.Background(), params); err != nil {
					t.Fatal(err)
				}
				files := testFiles(t, input, zipped)
				for name := range files {
					if strings.HasSuffix(name, ".swu") {
						if _, ok := seeds[name]; !ok {
							t.Fatalf("synthesized archive %s", name)
						}
					}
				}
				for name, seed := range seeds {
					if files[name] == "" || files[name] == archives[name] {
						t.Fatalf("seed %s not rebuilt", name)
					}
					outputRoot := ""
					if seed.root != "" {
						outputRoot = root
					}
					assertPipelineArchive(t, files[name], publicKey, outputRoot, seed.fip, files, root, files[fipPath])
				}
				if files["proto-swupdate"] != initial["proto-swupdate"] {
					t.Fatal("shared injection exported configured local updater")
				}
				for _, suffix := range legacyTestSuffixes {
					if files[legacyRoot+suffix] != initial[legacyRoot+suffix] {
						t.Fatalf("legacy %s changed after SWU rebuilding", suffix)
					}
				}
				assertChecksums(t, files)
				assertNoRecoveryCopies(t, files)
				if !zipped {
					assertNoAVBStage(t, input)
				}
				if iteration == 0 {
					writeTestFile(t, localUpdater, "changed local updater")
					files[fipPath] = "new deployed FIP"
					// Rebuilt payload names follow the supplied outer artifact basename.
					nextRoot := "miner-image-release-c3-p1-3.rootfs.ext4"
					for _, suffix := range []string{"", ".avb.body", ".avb.tail"} {
						files[nextRoot+suffix] = files[root+suffix]
						delete(files, root+suffix)
					}
					if zipped {
						if err := createTestZip(input, files); err != nil {
							t.Fatal(err)
						}
					} else {
						writeTestFile(t, filepath.Join(input, fipPath), files[fipPath])
						for _, suffix := range []string{"", ".avb.body", ".avb.tail"} {
							if err := os.Rename(filepath.Join(input, root+suffix), filepath.Join(input, nextRoot+suffix)); err != nil {
								t.Fatal(err)
							}
						}
					}
					root = nextRoot
				}
			}
		})
	}
}

func TestRootOnlySWUNeedsNoExportedFIPOrUpdater(t *testing.T) {
	privateKey, passfile, publicKey := pipelineKeys(t)
	root := "miner-image-release-c3-p1-1.rootfs.ext4"
	archive := pipelineSeed(t, root, false)
	for _, zipped := range []bool{false, true} {
		t.Run(fmt.Sprintf("zip=%t", zipped), func(t *testing.T) {
			testDocker(t)
			cfg, configPath := testConfig(t)
			cfg.Artifacts.LocalFiles = []string{"missing-updater"}
			input := testInput(t, map[string]string{root: alignedTestData("plain"), "root-only.swu": archive}, zipped)
			if _, err := RunWithConfig(context.Background(), Params{Config: cfg, ConfigPath: configPath, InputPath: input, PrivateKeyOverride: privateKey, PassfileOverride: passfile}); err != nil {
				t.Fatal(err)
			}
			files := testFiles(t, input, zipped)
			assertPipelineArchive(t, files["root-only.swu"], publicKey, root, false, files, root, "")
			for name := range files {
				if strings.HasSuffix(name, ".swu") && name != "root-only.swu" {
					t.Fatalf("synthesized %s", name)
				}
			}
			if _, ok := files["proto-swupdate"]; ok {
				t.Fatal("shared injection exported updater")
			}
			assertChecksums(t, files)
		})
	}
}

func TestMissingFIPRejectedBeforeMutation(t *testing.T) {
	for _, variant := range []string{"release", "mfg"} {
		for _, zipped := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/zip=%t", variant, zipped), func(t *testing.T) {
				testDocker(t)
				t.Setenv("DISTRO_TEST_NO_DOCKER", "1")
				cfg, configPath := testConfig(t)
				root := "miner-image-" + variant + "-c3-p1-1.rootfs.ext4"
				initial := map[string]string{root: alignedTestData("plain"), "root-only.swu": pipelineSeed(t, root, false), "firmware.swu": pipelineSeed(t, "", true), "sha.txt": "old checksum"}
				input := testInput(t, initial, zipped)
				before := ""
				if zipped {
					before = readTestFile(t, input)
				}
				_, err := RunWithConfig(context.Background(), Params{Config: cfg, ConfigPath: configPath, InputPath: input, Version: "2"})
				if err == nil || !strings.Contains(err.Error(), "FIP") {
					t.Fatalf("missing artifact FIP error = %v", err)
				}
				assertPipelineUnchanged(t, initial, testFiles(t, input, zipped))
				if zipped && readTestFile(t, input) != before {
					t.Fatal("preflight changed ZIP")
				}
			})
		}
	}
}

func TestArtifactFIPSelection(t *testing.T) {
	for _, kind := range []string{"valid", "missing", "ambiguous", "empty", "directory", "symlink", "directory-symlink"} {
		t.Run(kind, func(t *testing.T) {
			artifact := t.TempDir()
			directory := filepath.Join(artifact, "fip")
			if err := os.Mkdir(directory, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "fip-board-optee.bin")
			switch kind {
			case "missing":
				writeTestFile(t, filepath.Join(directory, "unrelated.bin"), "not FIP")
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				outside := filepath.Join(t.TempDir(), "outside.bin")
				writeTestFile(t, outside, "FIP bytes")
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			case "directory-symlink":
				if err := os.Remove(directory); err != nil {
					t.Fatal(err)
				}
				outside := t.TempDir()
				writeTestFile(t, filepath.Join(outside, filepath.Base(path)), "FIP bytes")
				if err := os.Symlink(outside, directory); err != nil {
					t.Fatal(err)
				}
			default:
				data := "FIP bytes"
				if kind == "empty" {
					data = ""
				}
				writeTestFile(t, path, data)
				if kind == "ambiguous" {
					writeTestFile(t, filepath.Join(directory, "fip-other-optee.bin"), "other FIP")
				}
			}
			selected, err := findFIP(artifact)
			if kind == "valid" {
				if err != nil || selected != path || readTestFile(t, selected) != "FIP bytes" {
					t.Fatalf("selected FIP %q: %v", selected, err)
				}
			} else if err == nil {
				t.Fatalf("accepted %s artifact FIP", kind)
			}
		})
	}
}

func assertPipelineUnchanged(t *testing.T, before, after map[string]string) {
	t.Helper()
	if len(after) != len(before) {
		t.Fatalf("failed operation changed artifact count: got %d, want %d", len(after), len(before))
	}
	for name, want := range before {
		if after[name] != want {
			t.Errorf("failed operation changed %s", name)
		}
	}
}

func pipelineAlterSeed(t *testing.T, archive, kind string) string {
	t.Helper()
	directory := t.TempDir()
	runPipelineCommand(t, directory, archive, "cpio", "-idm")
	names := strings.Fields(runPipelineCommand(t, directory, archive, "cpio", "-it"))
	for i, name := range names {
		names[i] = filepath.ToSlash(filepath.Clean(name))
	}
	switch kind {
	case "script hash":
		writeTestFile(t, filepath.Join(directory, "custom-install.sh"), "corrupt script")
	case "ambiguous rootfs":
		name := "other.rootfs.ext4.gz"
		writeTestFile(t, filepath.Join(directory, name), runPipelineCommand(t, directory, "other rootfs", "gzip", "-c"))
		names = append(names, name)
	case "ambiguous FIP":
		name := "fip-other.bin"
		writeTestFile(t, filepath.Join(directory, name), "other FIP")
		names = append(names, name)
	case "symlink":
		path := filepath.Join(directory, "opaque-member")
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("proto-swupdate", path); err != nil {
			t.Fatal(err)
		}
	case "duplicate member":
		names = append(names, "./"+names[2])
	default:
		t.Fatalf("unknown seed mutation %q", kind)
	}
	return runPipelineCommand(t, directory, strings.Join(names, "\n")+"\n", "cpio", "-o", "-H", "newc")
}

func TestUnsafeOrAmbiguousSWUPreflightPreservesArtifacts(t *testing.T) {
	root := "miner-image-release-c3-p1-1.rootfs.ext4"
	seed := pipelineSeed(t, root, true)
	for _, kind := range []string{"ambiguous rootfs", "ambiguous FIP", "symlink", "duplicate member"} {
		archive := pipelineAlterSeed(t, seed, kind)
		for _, zipped := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/zip=%t", kind, zipped), func(t *testing.T) {
				testDocker(t)
				t.Setenv("DISTRO_TEST_NO_DOCKER", "1")
				cfg, configPath := testConfig(t)
				initial := map[string]string{
					root:                      alignedTestData("plain"),
					"a-valid.swu":             pipelineSeed(t, root, false),
					"nested/z-rejected.swu":   archive,
					"fip/fip-board-optee.bin": "deployed FIP",
					"sha.txt":                 "old checksum",
				}
				input := testInput(t, initial, zipped)
				before := ""
				if zipped {
					before = readTestFile(t, input)
				}
				if _, err := RunWithConfig(context.Background(), Params{Config: cfg, ConfigPath: configPath, InputPath: input, Version: "2"}); err == nil {
					t.Fatal("unsafe or ambiguous seed accepted")
				}
				assertPipelineUnchanged(t, initial, testFiles(t, input, zipped))
				if zipped && readTestFile(t, input) != before {
					t.Fatal("preflight changed ZIP")
				}
				if !zipped {
					assertNoAVBStage(t, input)
				}
			})
		}
	}
}

func TestLaterSWUFailurePreservesAllArchivesAndChecksums(t *testing.T) {
	privateKey, passfile, _ := pipelineKeys(t)
	realOpenSSL, err := exec.LookPath("openssl")
	if err != nil {
		t.Fatal(err)
	}
	root := "miner-image-release-c3-p1-1.rootfs.ext4"
	first := pipelineSeed(t, root, false)
	last := pipelineSeed(t, "", true)
	for _, failure := range []string{"signing", "script hash"} {
		for _, zipped := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/zip=%t", failure, zipped), func(t *testing.T) {
				testDocker(t)
				cfg, configPath := testConfig(t)
				lastArchive := last
				if failure == "script hash" {
					lastArchive = pipelineAlterSeed(t, lastArchive, failure)
				}
				initial := map[string]string{
					root:                      alignedTestData("plain"),
					"a-root.swu":              first,
					"nested/z-firmware.swu":   lastArchive,
					"fip/fip-board-optee.bin": "deployed FIP",
					"sha.txt":                 "old checksum",
				}
				for _, suffix := range append([]string{".avb.body", ".avb.tail"}, legacyTestSuffixes...) {
					initial[root+suffix] = "old " + suffix
				}
				input := testInput(t, initial, zipped)
				beforeZIP := ""
				if zipped {
					beforeZIP = readTestFile(t, input)
				}
				bin := t.TempDir()
				count := filepath.Join(bin, "count")
				shim := fmt.Sprintf("#!/bin/sh\nn=0\n[ ! -f %q ] || read n < %q\nn=$((n+1))\nprintf '%%s\\n' \"$n\" > %q\n", count, count, count)
				if failure == "signing" {
					shim += "[ \"$n\" -ne 2 ] || exit 97\n"
				}
				shim += fmt.Sprintf("exec %q \"$@\"\n", realOpenSSL)
				if err := os.WriteFile(filepath.Join(bin, "openssl"), []byte(shim), 0755); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
				_, err := RunWithConfig(context.Background(), Params{Config: cfg, ConfigPath: configPath, InputPath: input, Version: "next", PrivateKeyOverride: privateKey, PassfileOverride: passfile})
				wantError := "signing sw-description"
				if failure == "script hash" {
					wantError = "hash mismatch for custom-install.sh"
				}
				if err == nil || !strings.Contains(err.Error(), wantError) {
					t.Fatalf("later archive failure = %v", err)
				}
				if readTestFile(t, count) != "2\n" {
					t.Fatal("failure did not reach second archive")
				}
				files := testFiles(t, input, zipped)
				if !zipped {
					if files[root] != alignedTestData(initial[root]+"VERSION=next\n") {
						t.Fatal("folder rootfs did not retain documented nontransactional mutation")
					}
					files[root] = initial[root]
					// Folder AVB outputs publish before the SWU batch; only SWUs
					// and checksums have the all-or-nothing guarantee here.
					for _, suffix := range []string{".avb.body", ".avb.tail"} {
						files[root+suffix] = initial[root+suffix]
					}
					assertNoAVBStage(t, input)
				} else if readTestFile(t, input) != beforeZIP {
					t.Fatal("later archive failure changed ZIP")
				}
				assertPipelineUnchanged(t, initial, files)
				assertNoRecoveryCopies(t, files)
			})
		}
	}
}

func TestChecksumFinalizationUsesFullArtifactRoot(t *testing.T) {
	for _, zipped := range []bool{false, true} {
		for _, skip := range []bool{false, true} {
			t.Run(fmt.Sprintf("zip=%t/skip=%t", zipped, skip), func(t *testing.T) {
				testDocker(t)
				cfg, configPath := testConfig(t)
				root := "nested/miner-image-release-c3-p1-1.rootfs.ext4"
				initial := map[string]string{
					root:          alignedTestData("plain rootfs"),
					"outside.bin": "full artifact root",
					"sha.txt":     "checksum sentinel",
				}
				input := testInput(t, initial, zipped)
				if _, err := RunWithConfig(context.Background(), Params{
					Config: cfg, ConfigPath: configPath, InputPath: input, SkipChecksums: skip,
				}); err != nil {
					t.Fatal(err)
				}
				files := testFiles(t, input, zipped)
				if skip {
					if files["sha.txt"] != initial["sha.txt"] {
						t.Fatal("suppressed checksums changed")
					}
				} else {
					assertChecksums(t, files)
				}
				if _, ok := files["nested/sha.txt"]; ok {
					t.Fatal("checksums written below artifact root")
				}
			})
		}
	}
}

func TestVersionOverrideWhitespaceKeepsRootfsAndManifestConsistent(t *testing.T) {
	privateKey, passfile, publicKey := pipelineKeys(t)
	for _, zipped := range []bool{false, true} {
		for _, override := range []string{" 2.0.0 ", " \t "} {
			t.Run(fmt.Sprintf("zip=%t/version=%q", zipped, override), func(t *testing.T) {
				testDocker(t)
				cfg, configPath := testConfig(t)
				root := "miner-image-release-c3-p1-1.rootfs.ext4"
				plain := alignedTestData("VERSION=seed\n")
				input := testInput(t, map[string]string{
					root: plain, "image.swu": pipelineSeed(t, root, false),
				}, zipped)
				ipk := filepath.Join(t.TempDir(), "marker_1.0.0_all.ipk")
				writeTestFile(t, ipk, "package bytes\n")
				params := Params{Config: cfg, ConfigPath: configPath, InputPath: input,
					IPKPaths: []string{ipk}, Version: override,
					PrivateKeyOverride: privateKey, PassfileOverride: passfile}
				if _, err := RunWithConfig(context.Background(), params); err != nil {
					t.Fatal(err)
				}
				files := testFiles(t, input, zipped)
				want := plain + "package bytes\n"
				version := strings.TrimSpace(override)
				manifestVersion := "seed"
				if version != "" {
					want += "VERSION=" + version + "\n"
					manifestVersion = version
				}
				if files[root] != alignedTestData(want) {
					t.Fatal("rootfs did not use normalized version override")
				}
				extracted := t.TempDir()
				runPipelineCommand(t, extracted, files["image.swu"], "cpio", "-idm")
				description := readTestFile(t, filepath.Join(extracted, "sw-description"))
				if !strings.Contains(description, `version = "`+manifestVersion+`"`) {
					t.Fatalf("manifest version differs from rootfs override: %s", description)
				}
				assertPipelineArchive(t, files["image.swu"], publicKey, root, false, files, root, "")
			})
		}
	}
}

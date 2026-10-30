package swurepackage

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSelectiveReplacementAndPreview(t *testing.T) {
	base, public := fipFixture(t)
	if err := Run(base); err != nil {
		t.Fatal(err)
	}
	seed := t.TempDir()
	names, err := extractCPIO(base.Output, seed)
	if err != nil {
		t.Fatal(err)
	}
	source := string(readTest(t, filepath.Join(seed, "sw-description")))
	opaque := `files=({filename="opaque.bin";sha256="not authenticated";note="image.rootfs.ext4.gz fip-new.bin";}); /* keep exact text */`
	source = strings.Replace(source, "version =", opaque+" version =", 1)
	writeTest(t, filepath.Join(seed, "sw-description"), []byte(source))
	input := filepath.Join(t.TempDir(), "mixed.swu")
	if _, err := createCPIO(seed, input, names[2:]); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"rootfs", "fip", "both"} {
		t.Run(kind, func(t *testing.T) {
			opts := base
			opts.OldSWU = input
			opts.Output = filepath.Join(t.TempDir(), "output.swu")
			opts.NewRootfs = filepath.Join(t.TempDir(), "replacement.rootfs.ext4")
			opts.AVBBody, opts.AVBTail = opts.NewRootfs+".avb.body", opts.NewRootfs+".avb.tail"
			for _, path := range []string{opts.NewRootfs, opts.AVBBody, opts.AVBTail} {
				writeTest(t, path, bytes.Repeat([]byte{0x59}, 512))
			}
			opts.NewFIP = filepath.Join(t.TempDir(), "fip-next.bin")
			writeTest(t, opts.NewFIP, []byte("new firmware bytes"))
			wantRoot, wantFIP := filepath.Base(base.NewRootfs), filepath.Base(base.NewFIP)
			if kind == "fip" {
				opts.NewRootfs, opts.AVBBody, opts.AVBTail = "", "", ""
			} else {
				wantRoot = filepath.Base(opts.NewRootfs)
				opts.SoftwareVersion = "2.3.4-release"
			}
			if kind == "rootfs" {
				opts.NewFIP = ""
			} else {
				wantFIP = filepath.Base(opts.NewFIP)
			}
			_, outputs, err := Preview(opts)
			if err != nil {
				t.Fatal(err)
			}
			planned := map[string]bool{}
			for _, output := range outputs {
				planned[output.Path] = true
			}
			for _, member := range []string{wantRoot + ".gz", wantRoot + ".avb.body", wantRoot + ".avb.tail", wantFIP} {
				if !planned[opts.Output+"!/"+member] {
					t.Fatal("preview missing member", member)
				}
			}
			if err := Run(opts); err != nil {
				t.Fatal(err)
			}
			stage := t.TempDir()
			actualNames, err := extractCPIO(opts.Output, stage)
			if err != nil {
				t.Fatal(err)
			}
			if len(outputs) != len(actualNames)+1 {
				t.Fatal("preview includes obsolete or missing members", outputs, actualNames)
			}
			for _, name := range actualNames {
				if !planned[opts.Output+"!/"+name] {
					t.Fatal("preview omitted actual member", name)
				}
			}
			updated := string(readTest(t, filepath.Join(stage, "sw-description")))
			if kind == "fip" && !strings.Contains(updated, `version = "1"`) {
				t.Fatal("FIP-only replacement changed software version")
			}
			if kind != "fip" && !strings.Contains(updated, `version = "2.3.4-release"`) {
				t.Fatal("rootfs replacement did not update software version")
			}
			if opts.NewRootfs != "" {
				if got := runTest(t, stage, nil, "gzip", "-dc", wantRoot+".gz"); !bytes.Equal(got, readTest(t, opts.NewRootfs)) {
					t.Fatal("replacement rootfs bytes differ")
				}
				for _, suffix := range []string{".avb.body", ".avb.tail"} {
					if !bytes.Equal(readTest(t, filepath.Join(stage, wantRoot+suffix)), readTest(t, opts.NewRootfs+suffix)) {
						t.Fatal("replacement AVB bytes differ", suffix)
					}
				}
				if _, err := os.Lstat(filepath.Join(stage, filepath.Base(base.NewRootfs)+".gz")); !os.IsNotExist(err) {
					t.Fatal("old rootfs member retained")
				}
			}
			if opts.NewFIP != "" {
				if !bytes.Equal(readTest(t, filepath.Join(stage, wantFIP)), readTest(t, opts.NewFIP)) {
					t.Fatal("replacement FIP bytes differ")
				}
				if _, err := os.Lstat(filepath.Join(stage, filepath.Base(base.NewFIP))); !os.IsNotExist(err) {
					t.Fatal("old FIP member retained")
				}
			}
			if !strings.Contains(updated, opaque) {
				t.Fatal("unrelated manifest text changed")
			}
			unchanged := map[string]bool{"custom-hook.sh": true, "proto-swupdate": true, "opaque.bin": true}
			if kind == "rootfs" {
				unchanged[filepath.Base(base.NewFIP)] = true
			}
			if kind == "fip" {
				for _, suffix := range []string{".gz", ".avb.body", ".avb.tail"} {
					unchanged[filepath.Base(base.NewRootfs)+suffix] = true
				}
			}
			for name := range unchanged {
				if !bytes.Equal(readTest(t, filepath.Join(stage, name)), readTest(t, filepath.Join(seed, name))) {
					t.Fatal("unselected member bytes changed", name)
				}
			}
			// Compare complete original image records, including whitespace and offsets.
			for _, record := range imageFieldsTest(t, source) {
				if !unchanged[record["filename"]] {
					continue
				}
				pattern := regexp.MustCompile(`filename\s*=\s*"` + regexp.QuoteMeta(record["filename"]) + `"`)
				match := pattern.FindStringIndex(source)
				start := -1
				if match != nil {
					start = match[0]
				}
				if start < 0 {
					t.Fatal("fixture record not found", record)
				}
				start = strings.LastIndex(source[:start], "{")
				end := start + strings.Index(source[start:], "}") + 1
				if !strings.Contains(updated, source[start:end]) {
					t.Fatal("unselected manifest record changed", record)
				}
			}
			for _, record := range imageFieldsTest(t, updated) {
				payload := readTest(t, filepath.Join(stage, record["filename"]))
				if record["sha256"] != fmt.Sprintf("%x", sha256.Sum256(payload)) {
					t.Fatal("image hash mismatch", record)
				}
			}
			runTest(t, stage, nil, "openssl", "dgst", "-sha256", "-verify", public, "-signature", "sw-description.sig", "sw-description")
		})
	}
}

func TestReplacementNameCollisions(t *testing.T) {
	base, _ := fipFixture(t)
	for _, kind := range []string{"rootfs", "fip", "between-replacements"} {
		t.Run(kind, func(t *testing.T) {
			opts := base
			stage := t.TempDir()
			names, err := extractCPIO(base.OldSWU, stage)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "rootfs":
				opts.NewRootfs = filepath.Join(t.TempDir(), "collision.rootfs.ext4")
				opts.AVBBody, opts.AVBTail = opts.NewRootfs+".avb.body", opts.NewRootfs+".avb.tail"
				for _, path := range []string{opts.NewRootfs, opts.AVBBody, opts.AVBTail} {
					writeTest(t, path, make([]byte, 512))
				}
				writeTest(t, filepath.Join(stage, "collision.rootfs.ext4.avb.body"), []byte("unrelated"))
				names = append(names, "collision.rootfs.ext4.avb.body")
			case "fip":
				opts.NewFIP = filepath.Join(t.TempDir(), "fip-opaque.bin")
				writeTest(t, opts.NewFIP, []byte("firmware"))
				writeTest(t, filepath.Join(stage, "fip-opaque.bin"), []byte("unrelated"))
				names = append(names, "fip-opaque.bin")
			case "between-replacements":
				opts.NewFIP = filepath.Join(t.TempDir(), filepath.Base(opts.NewRootfs)+".gz")
				writeTest(t, opts.NewFIP, []byte("firmware"))
			}
			opts.OldSWU = filepath.Join(t.TempDir(), "collision.swu")
			if _, err := createCPIO(stage, opts.OldSWU, names[2:]); err != nil {
				t.Fatal(err)
			}
			before := readTest(t, opts.OldSWU)
			opts.Output = filepath.Join(t.TempDir(), "output.swu")
			if err := Run(opts); err == nil {
				t.Fatal("accepted replacement member collision")
			}
			if !bytes.Equal(before, readTest(t, opts.OldSWU)) {
				t.Fatal("collision changed seed")
			}
			if _, err := os.Lstat(opts.Output); !os.IsNotExist(err) {
				t.Fatal("collision published output")
			}
		})
	}
}

func TestReplacementRejectsRootfsWithoutImageName(t *testing.T) {
	opts, _ := fipFixture(t)
	root := filepath.Join(t.TempDir(), "rootfs.ext4")
	for _, suffix := range []string{"", ".avb.body", ".avb.tail"} {
		writeTest(t, root+suffix, readTest(t, opts.NewRootfs+suffix))
	}
	opts.NewRootfs = root
	opts.AVBBody, opts.AVBTail = root+".avb.body", root+".avb.tail"
	opts.NewFIP = ""
	before := readTest(t, opts.OldSWU)
	if _, _, err := Preview(opts); err == nil {
		t.Fatal("preview accepted rootfs without .rootfs.ext4 suffix")
	}
	if err := Run(opts); err == nil {
		t.Fatal("accepted rootfs without .rootfs.ext4 suffix")
	}
	if _, err := os.Lstat(opts.Output); !os.IsNotExist(err) {
		t.Fatal("invalid rootfs name published output")
	}
	if !bytes.Equal(before, readTest(t, opts.OldSWU)) {
		t.Fatal("invalid rootfs name changed seed archive")
	}
}

func TestFIPOnlyRejectsSoftwareVersion(t *testing.T) {
	opts, _ := fipFixture(t)
	opts.NewRootfs, opts.AVBBody, opts.AVBTail = "", "", ""
	opts.SoftwareVersion = "2"
	if _, _, err := Preview(opts); err == nil {
		t.Fatal("preview accepted software version without rootfs replacement")
	}
	if err := Run(opts); err == nil {
		t.Fatal("accepted software version without rootfs replacement")
	}
	if _, err := os.Lstat(opts.Output); !os.IsNotExist(err) {
		t.Fatal("invalid software version created output")
	}
}

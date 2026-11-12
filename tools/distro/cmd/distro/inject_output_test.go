package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"proto-distro/tools/distro/internal/fileutil"
)

func TestInjectCommandOutput(t *testing.T) {
	for _, zipped := range []bool{false, true} {
		for _, flag := range []string{"--output-dir", "-o"} {
			for _, fail := range []bool{false, true} {
				t.Run(fmt.Sprintf("zip=%t/%s/failure=%t", zipped, flag, fail), func(t *testing.T) {
					cfg, configPath := commandFixture(t)
					if fail {
						t.Setenv("DISTRO_TEST_SIGNER_FAIL", "1")
					}
					data, err := yaml.Marshal(cfg)
					if err != nil {
						t.Fatal(err)
					}
					commandWrite(t, configPath, data)
					source := t.TempDir()
					folder := filepath.Join(source, "arbitrary-artifact")
					root := "miner-image-release-c3-p1-1.0.0.rootfs.ext4"
					plain := bytes.Repeat([]byte("r"), 512)
					artifactPut(t, filepath.Join(folder, root), plain)
					artifactPut(t, filepath.Join(folder, "sha.txt"), []byte("old checksums"))
					artifactPut(t, filepath.Join(folder, "sentinel"), []byte("untouched"))
					input := folder
					if zipped {
						input += ".zip"
						if err := fileutil.CreateZip(folder, input); err != nil {
							t.Fatal(err)
						}
					}
					before := artifactSnapshot(t, source)
					parent := filepath.Join(t.TempDir(), "missing", "output")
					argument := parent
					if flag == "-o" {
						cwd, err := os.Getwd()
						if err != nil {
							t.Fatal(err)
						}
						argument, err = filepath.Rel(cwd, parent)
						if err != nil {
							t.Fatal(err)
						}
					}
					_, err = artifactCommand(injectCommand(), configPath, flag, argument, "--version", "2.0.0", input)
					if !reflect.DeepEqual(before, artifactSnapshot(t, source)) {
						t.Fatal("explicit output changed source")
					}
					output := filepath.Join(parent, filepath.Base(input))
					if fail {
						if err == nil || !strings.Contains(err.Error(), "creating AVB") {
							t.Fatalf("signer failure = %v", err)
						}
						if zipped {
							if _, err := os.Lstat(output); !os.IsNotExist(err) {
								t.Fatalf("failed ZIP published: %v", err)
							}
						}
					} else {
						if err != nil {
							t.Fatal(err)
						}
						result := output
						if zipped {
							result = t.TempDir()
							if err := fileutil.ExtractZip(output, result); err != nil {
								t.Fatal(err)
							}
						}
						gotRoot := commandRead(t, filepath.Join(result, root))
						if !bytes.HasPrefix(gotRoot, append(plain, []byte("VERSION=2.0.0\n")...)) || len(gotRoot)%512 != 0 {
							t.Fatal("version injection failed or internal filename changed")
						}
						assertAVBPair(t, result, root)
						checksums := string(commandRead(t, filepath.Join(result, "sha.txt")))
						for _, name := range []string{root, root + ".avb.body", root + ".avb.tail", "sentinel"} {
							want := fmt.Sprintf("%x  %s\n", sha256.Sum256(commandRead(t, filepath.Join(result, name))), name)
							if !strings.Contains(checksums, want) {
								t.Fatalf("missing output checksum for %s", name)
							}
						}
					}
					entries, err := os.ReadDir(parent)
					if err != nil {
						t.Fatal(err)
					}
					wantEntries := 1
					if fail && zipped {
						wantEntries = 0
					}
					if len(entries) != wantEntries {
						t.Fatalf("unexpected output entries: %v", entries)
					}
					for _, entry := range entries {
						if entry.Name() != filepath.Base(input) || entry.Type()&os.ModeSymlink != 0 {
							t.Fatalf("unexpected output or latest link: %s", entry.Name())
						}
					}
				})
			}
		}
	}
}

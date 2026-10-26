package gofastboot

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"proto-distro/tools/flashimage"
	"proto-distro/tools/go_fastboot/internal/fastboot"
	"proto-distro/tools/go_fastboot/internal/ui"
)

type geometryClient struct {
	queries  []string
	capacity string
}

func (c *geometryClient) GetVar(ctx context.Context, name string) (string, error) {
	c.queries = append(c.queries, name)
	if name == "max-download-size" {
		return "1000", nil
	}
	if strings.HasPrefix(name, "partition-size:") {
		if c.capacity != "" {
			return c.capacity, nil
		}
		return "40000000", nil
	}
	return "", fmt.Errorf("unexpected query %s", name)
}

func avbFixture(t *testing.T, directory, name string, missing int) string {
	t.Helper()
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	for i, part := range []struct {
		suffix string
		size   int
		value  byte
	}{{"", 61952, 7}, {".avb.body", 5632, 13}, {".avb.tail", 512, 29}} {
		if i > 0 && missing&(1<<(i-1)) != 0 {
			continue
		}
		if err := os.WriteFile(path+part.suffix, bytes.Repeat([]byte{part.value}, part.size), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestSelectedAVBInputs(t *testing.T) {
	profile := flashimage.C3()
	for _, source := range []string{"directory", "zip", "single"} {
		for missing := range 4 {
			t.Run(fmt.Sprintf("%s/missing%d", source, missing), func(t *testing.T) {
				root := t.TempDir()
				imageDir := filepath.Join(root, "images")
				raw := avbFixture(t, imageDir, "miner-image-test.rootfs.ext4", missing)
				var images map[string]string
				var err error
				key := "rootfs"
				switch source {
				case "directory":
					images, err = scanDirImages(imageDir)
				case "zip":
					archive := filepath.Join(root, "images.zip")
					writeImageArchive(t, archive, imageDir)
					var extracted string
					extracted, images, err = extractZipImages(archive)
					if extracted != "" {
						defer os.RemoveAll(extracted)
					}
				case "single":
					key = "rootfs_a"
					images = map[string]string{key: raw}
				}
				if err != nil {
					t.Fatal(err)
				}
				original := images[key]
				client := &geometryClient{}
				warnings := captureStatus(t)
				prepared, cleanup, err := prepareFlashImages(context.Background(), profile, images, sortPartitions(profile, images), client.GetVar, ui.NewStatusLine(), false)
				defer cleanup()
				if err != nil {
					t.Fatal(err)
				}
				if images[key] != original {
					t.Fatal("discovery path changed")
				}
				if missing != 0 {
					if len(prepared) != 0 || len(client.queries) != 0 {
						t.Fatalf("incomplete pair prepared or queried: %v %v", prepared, client.queries)
					}
					text := warnings()
					if strings.Count(text, "AVB metadata missing") != 1 || !strings.Contains(text, "verified boot may fail") {
						t.Fatalf("warning: %s", text)
					}
					for bit, suffix := range []string{".avb.body", ".avb.tail"} {
						if missing&(1<<bit) != 0 && !strings.Contains(text, filepath.Base(original)+suffix) {
							t.Fatalf("missing path absent: %s", text)
						}
					}
				} else {
					if len(warnings()) != 0 {
						t.Fatalf("unexpected warning: %s", warnings())
					}
					sparse, err := fastboot.OpenSparseImage(prepared[key])
					if err != nil {
						t.Fatal(err)
					}
					defer sparse.Close()
					if sparse.Header.BlockSize != 512 || sparse.Header.TotalBlocks != 1<<30/512 {
						t.Fatalf("sparse geometry: %+v", sparse.Header)
					}
					last := sparse.Chunks[len(sparse.Chunks)-1]
					if last.Header.ChunkType != 0xCAC1 || last.Header.ChunkBlocks != 1 || last.DataSize != 512 {
						t.Fatalf("invalid tail descriptor: %+v", last)
					}
					file, err := os.Open(prepared[key])
					if err != nil {
						t.Fatal(err)
					}
					defer file.Close()
					tail := make([]byte, 512)
					if _, err := file.ReadAt(tail, last.DataOffset); err != nil || !bytes.Equal(tail, bytes.Repeat([]byte{29}, 512)) {
						t.Fatalf("tail: %v", err)
					}
					if !reflect.DeepEqual(client.queries, []string{"partition-size:rootfs_a", "max-download-size"}) {
						t.Fatalf("queries: %v", client.queries)
					}
				}
				data, err := os.ReadFile(raw)
				if err != nil || !bytes.Equal(data, bytes.Repeat([]byte{7}, 61952)) {
					t.Fatalf("source changed: %v", err)
				}
			})
		}
	}
}

func TestAVBPreflightUsesFinalDestination(t *testing.T) {
	profile := flashimage.C3()
	for _, key := range []string{"rootfs", "rootfs_a", "rootfs_b", "recovery"} {
		t.Run(key, func(t *testing.T) {
			path := avbFixture(t, t.TempDir(), "rootfs.ext4", 0)
			images := map[string]string{key: path}
			client := &geometryClient{capacity: "20000"}
			warnings := captureStatus(t)
			prepared, cleanup, err := prepareFlashImages(context.Background(), profile, images, sortPartitions(profile, images), client.GetVar, ui.NewStatusLine(), false)
			defer cleanup()
			if err == nil || len(prepared) != 0 || len(warnings()) != 0 {
				t.Fatalf("mismatched target accepted: %v %v", prepared, err)
			}
			destination := key
			if key == "rootfs" {
				destination = "rootfs_a"
			}
			if len(client.queries) == 0 || client.queries[0] != "partition-size:"+destination {
				t.Fatalf("destination: %v", client.queries)
			}
		})
	}
}

func TestAVBPreflightIgnoresUnselectedMetadataAndUnknownTargets(t *testing.T) {
	profile := flashimage.C3()
	directory := t.TempDir()
	raw := avbFixture(t, directory, "miner-image-test.rootfs.ext4", 0)
	if err := os.Remove(raw + ".avb.tail"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(raw+".avb.tail", 0700); err != nil {
		t.Fatal(err)
	}
	images := map[string]string{"bootfs": filepath.Join(directory, "not-opened"), "unknown": raw, "boot": raw}
	client := &geometryClient{}
	warnings := captureStatus(t)
	prepared, cleanup, err := prepareFlashImages(context.Background(), profile, images, sortPartitions(profile, images), client.GetVar, ui.NewStatusLine(), false)
	defer cleanup()
	if err != nil || len(prepared) != 0 || len(client.queries) != 0 || len(warnings()) != 0 {
		t.Fatalf("non-AVB preflight: %v %v %v %s", err, prepared, client.queries, warnings())
	}
}

func TestAVBPreflightRemovesEarlierPreparedImagesOnFailure(t *testing.T) {
	profile := flashimage.C3()
	root := t.TempDir()
	output := filepath.Join(root, "temporary")
	if err := os.Mkdir(output, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", output)
	t.Setenv("TMP", output)
	t.Setenv("TEMP", output)
	first := avbFixture(t, filepath.Join(root, "first"), "rootfs.ext4", 0)
	later := avbFixture(t, filepath.Join(root, "later"), "recovery.ext4", 0)
	if err := os.WriteFile(later+".avb.tail", []byte{1}, 0600); err != nil {
		t.Fatal(err)
	}
	images := map[string]string{"rootfs": first, "recovery": later, "datafs": "not-opened"}
	warnings := captureStatus(t)
	_, cleanup, err := prepareFlashImages(context.Background(), profile, images, sortPartitions(profile, images), (&geometryClient{}).GetVar, ui.NewStatusLine(), false)
	defer cleanup()
	if err == nil {
		t.Fatal("malformed later pair accepted")
	}
	entries, err := os.ReadDir(output)
	if err != nil || len(entries) != 0 {
		t.Fatalf("prepared image leaked: %v %v", entries, err)
	}
	if len(warnings()) != 0 {
		t.Fatalf("fatal error downgraded to warning: %s", warnings())
	}
}

func writeImageArchive(t *testing.T, archive, directory string) {
	t.Helper()
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		w, err := z.Create("nested/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// StatusLine writes to process stdout; these tests deliberately do not run in parallel.
func captureStatus(t *testing.T) func() string {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "status-")
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = file
	t.Cleanup(func() { os.Stdout = previous; file.Close() })
	return func() string {
		data, err := os.ReadFile(file.Name())
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
}

func TestNativePreflightRejectsLaterImageBeforeWrites(t *testing.T) {
	for _, verity := range []bool{false, true} {
		t.Run(fmt.Sprintf("verity=%t", verity), func(t *testing.T) {
			var writes, limits atomic.Int32
			startWorkflowPeer(t, func(command string) string {
				switch command {
				case "getvar:product":
					return "OKAYloopback"
				case "getvar:max-download-size":
					limits.Add(1)
					return "OKAY2000"
				default:
					writes.Add(1)
					return "FAILunexpected write"
				}
			})
			t.Chdir(t.TempDir())
			directory := t.TempDir()
			if err := os.WriteFile(filepath.Join(directory, "bootfs.ext4"), bytes.Repeat([]byte{7}, 512), 0600); err != nil {
				t.Fatal(err)
			}
			wantLimits := int32(1)
			if verity {
				avbFixture(t, directory, "rootfs.ext4", 2) // Missing tail must abort before any write.
				wantLimits = 0
			} else if err := os.WriteFile(filepath.Join(directory, "rootfs.ext4"), []byte{0x3a, 0xff, 0x26, 0xed}, 0600); err != nil {
				t.Fatal(err) // Correct sparse magic, truncated header.
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := Flash(ctx, FlashRequest{Target: "127.0.0.1", ImagePath: directory, NoReboot: true, Verity: verity})
			if err == nil || ctx.Err() != nil {
				t.Fatalf("invalid later image did not fail preflight: %v", err)
			}
			if verity && !strings.Contains(err.Error(), "rootfs.ext4.avb.tail") {
				t.Fatalf("missing tail not reported: %v", err)
			}
			if limits.Load() != wantLimits || writes.Load() != 0 {
				t.Fatalf("capacity queries=%d writes=%d", limits.Load(), writes.Load())
			}
		})
	}
}

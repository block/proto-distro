package distroinject

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOutputPreservesSourceAndArtifactType(t *testing.T) {
	for _, zipped := range []bool{false, true} {
		for _, version := range []string{"", "2.0.0"} {
			t.Run(fmt.Sprintf("zip=%t/version=%s", zipped, version), func(t *testing.T) {
				testDocker(t)
				cfg, configPath := testConfig(t)
				root := "nested/miner-image-release-c3-p1-1.0.0.rootfs.ext4"
				initial := map[string]string{root: alignedTestData("plain\n"), "sha.txt": "stale", "unrelated": "keep"}
				input := testInput(t, initial, zipped)
				var beforeZIP string
				if zipped {
					beforeZIP = readTestFile(t, input)
					// Explicit ZIP output must not use a sibling extraction folder.
					sibling := strings.TrimSuffix(input, ".zip")
					if err := os.Mkdir(sibling, 0755); err != nil {
						t.Fatal(err)
					}
					writeTestFile(t, filepath.Join(sibling, "sentinel"), "untouched")
				}
				parent := filepath.Join(t.TempDir(), "missing", "output")
				cwd, err := os.Getwd()
				if err != nil {
					t.Fatal(err)
				}
				relativeParent, err := filepath.Rel(cwd, parent)
				if err != nil {
					t.Fatal(err)
				}
				output, err := RunWithConfig(context.Background(), Params{
					Config: cfg, ConfigPath: configPath, InputPath: input, OutputDir: relativeParent, Version: version,
				})
				if err != nil {
					t.Fatal(err)
				}
				resolvedParent, err := filepath.EvalSymlinks(parent)
				if err != nil {
					t.Fatal(err)
				}
				if want := filepath.Join(resolvedParent, filepath.Base(input)); output != want {
					t.Fatalf("output = %q, want %q", output, want)
				}
				files := testFiles(t, output, zipped)
				wantRoot := initial[root]
				if version != "" {
					wantRoot = alignedTestData(wantRoot + "VERSION=" + version + "\n")
				}
				if files[root] != wantRoot || files["unrelated"] != initial["unrelated"] {
					t.Fatal("output content differs or internal filename changed")
				}
				if files[root+".avb.body"] != readTestFile(t, os.Getenv("DISTRO_TEST_BODY")) || files[root+".avb.tail"] != readTestFile(t, os.Getenv("DISTRO_TEST_TAIL")) {
					t.Fatal("output missing generated AVB pair")
				}
				assertChecksums(t, files)
				assertPipelineUnchanged(t, initial, testFiles(t, input, zipped))
				if zipped {
					if readTestFile(t, input) != beforeZIP || readTestFile(t, filepath.Join(strings.TrimSuffix(input, ".zip"), "sentinel")) != "untouched" {
						t.Fatal("source ZIP or sibling extraction directory changed")
					}
				}
				entries, err := os.ReadDir(parent)
				if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(input) || entries[0].Type()&os.ModeSymlink != 0 {
					t.Fatalf("unexpected output entries: %v, %v", entries, err)
				}
			})
		}
	}
}

func TestOutputNoWorkStillCreatesArtifact(t *testing.T) {
	for _, zipped := range []bool{false, true} {
		t.Run(fmt.Sprintf("zip=%t", zipped), func(t *testing.T) {
			testDocker(t)
			t.Setenv("DISTRO_TEST_NO_DOCKER", "1")
			cfg, configPath := testConfig(t)
			initial := map[string]string{"generic.rootfs.ext4": "plain", "sha.txt": "unchanged"}
			input := testInput(t, initial, zipped)
			output, err := RunWithConfig(context.Background(), Params{Config: cfg, ConfigPath: configPath, InputPath: input, OutputDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			assertPipelineUnchanged(t, initial, testFiles(t, output, zipped))
			assertPipelineUnchanged(t, initial, testFiles(t, input, zipped))
		})
	}
}

func TestOutputConflictsPreserveExistingArtifact(t *testing.T) {
	for _, zipped := range []bool{false, true} {
		for _, kind := range []string{"file", "directory", "dangling symlink", "source parent", "source alias", "parent file"} {
			t.Run(fmt.Sprintf("zip=%t/%s", zipped, kind), func(t *testing.T) {
				cfg, configPath := testConfig(t)
				initial := map[string]string{"generic.rootfs.ext4": "source", "sha.txt": "original"}
				input := testInput(t, initial, zipped)
				parent := t.TempDir()
				destination := filepath.Join(parent, filepath.Base(input))
				wantError := "output artifact already exists:"
				sentinel := destination
				switch kind {
				case "file":
					writeTestFile(t, destination, "destination")
				case "directory":
					if err := os.Mkdir(destination, 0755); err != nil {
						t.Fatal(err)
					}
					sentinel = filepath.Join(destination, "sentinel")
					writeTestFile(t, sentinel, "destination")
				case "dangling symlink":
					if err := os.Symlink(filepath.Join(parent, "absent"), destination); err != nil {
						t.Fatal(err)
					}
				case "source parent":
					parent = filepath.Dir(input)
				case "source alias":
					parent = filepath.Join(parent, "alias")
					if err := os.Symlink(filepath.Dir(input), parent); err != nil {
						t.Fatal(err)
					}
				case "parent file":
					parent = filepath.Join(parent, "file")
					writeTestFile(t, parent, "destination")
					sentinel = parent
					wantError = "output"
				}
				_, err := RunWithConfig(context.Background(), Params{Config: cfg, ConfigPath: configPath, InputPath: input, OutputDir: parent, Version: "2"})
				if err == nil || !strings.Contains(err.Error(), wantError) {
					t.Fatalf("conflict error = %v, want %s", err, wantError)
				}
				assertPipelineUnchanged(t, initial, testFiles(t, input, zipped))
				switch kind {
				case "file", "directory", "parent file":
					if readTestFile(t, sentinel) != "destination" {
						t.Fatal("destination sentinel changed")
					}
				case "dangling symlink":
					if target, err := os.Readlink(destination); err != nil || target != filepath.Join(parent, "absent") {
						t.Fatalf("destination symlink changed: %q, %v", target, err)
					}
				}
			})
		}
	}
}

func TestOutputOverlapRejectedBeforeCreatingParents(t *testing.T) {
	for _, alias := range []bool{false, true} {
		for _, suffix := range []string{"", "missing/nested"} {
			t.Run(fmt.Sprintf("alias=%t/suffix=%s", alias, suffix), func(t *testing.T) {
				cfg, configPath := testConfig(t)
				initial := map[string]string{"generic.rootfs.ext4": "plain"}
				input := testInput(t, initial, false)
				parent := input
				if alias {
					parent = filepath.Join(t.TempDir(), "alias")
					if err := os.Symlink(input, parent); err != nil {
						t.Fatal(err)
					}
				}
				parent = filepath.Join(parent, suffix)
				_, err := RunWithConfig(context.Background(), Params{Config: cfg, ConfigPath: configPath, InputPath: input, OutputDir: parent})
				if err == nil || !strings.Contains(err.Error(), "output directory must not be inside input artifact directory") {
					t.Fatalf("overlap error = %v", err)
				}
				assertPipelineUnchanged(t, initial, testFiles(t, input, false))
				entries, err := os.ReadDir(input)
				if err != nil || len(entries) != 1 {
					t.Fatalf("rejected overlap created source entries: %v, %v", entries, err)
				}
			})
		}
	}
}

func TestOutputFolderReservesBeforeCopy(t *testing.T) {
	initial := map[string]string{"generic.rootfs.ext4": "source"}
	input := testInput(t, initial, false)
	parent := t.TempDir()
	output, err := prepareOutputPath(input, true, parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(output, 0755); !os.IsExist(err) {
		t.Fatalf("competing folder creation = %v, want existing destination error before copying", err)
	}
	sentinel := filepath.Join(output, "generic.rootfs.ext4")
	writeTestFile(t, sentinel, "winner")
	if _, err := Run(context.Background(), Options{InputPath: input, OutputDir: parent}); err == nil || !strings.Contains(err.Error(), "output artifact already exists:") {
		t.Fatalf("competing output error = %v", err)
	}
	if readTestFile(t, sentinel) != "winner" {
		t.Fatal("competing output overwrote winning bytes")
	}
	assertPipelineUnchanged(t, initial, testFiles(t, input, false))
}

func TestOutputZipPublicationDoesNotReplaceWinner(t *testing.T) {
	first := testInput(t, map[string]string{"generic.rootfs.ext4": "winner"}, true)
	second := testInput(t, map[string]string{"generic.rootfs.ext4": "loser"}, true)
	firstBefore, secondBefore := readTestFile(t, first), readTestFile(t, second)
	parent := t.TempDir()
	output, err := prepareOutputPath(first, false, parent)
	if err != nil {
		t.Fatal(err)
	}
	// Both publishers finish preflight before either publishes.
	competingOutput, err := prepareOutputPath(second, false, parent)
	if err != nil {
		t.Fatal(err)
	}
	if competingOutput != output {
		t.Fatalf("competing destination = %q, want %q", competingOutput, output)
	}
	options := Options{OutputDir: parent}
	if _, err := runZip(context.Background(), first, output, options); err != nil {
		t.Fatal(err)
	}
	winner := readTestFile(t, output)
	if _, err := runZip(context.Background(), second, competingOutput, options); err == nil || !strings.Contains(err.Error(), "output artifact already exists:") {
		t.Errorf("competing publication error = %v, want existing destination error", err)
	}
	if readTestFile(t, output) != winner || testFiles(t, output, true)["generic.rootfs.ext4"] != "winner" {
		t.Error("competing ZIP publication overwrote winning bytes")
	}
	if readTestFile(t, first) != firstBefore || readTestFile(t, second) != secondBefore {
		t.Error("ZIP publication changed input bytes")
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(output) {
		t.Fatalf("unexpected output entries: %v, %v", entries, err)
	}
}

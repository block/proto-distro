// Package distroinject updates plain rootfs images, signs release/mfg AVB, and
// repackages SWUs through one shared folder/ZIP pipeline.
package distroinject

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"proto-distro/tools/distro/internal/config"
	"proto-distro/tools/distro/internal/docker"
	"proto-distro/tools/distro/internal/fileutil"
	"proto-distro/tools/distro/internal/opkginject"
	"proto-distro/tools/distro/internal/swurepackage"
)

// Options configures a distro inject operation.
type Options struct {
	Machine        string
	Variant        string
	ConfigPath     string
	AVB            map[string]config.AVBKeys
	ContainerImage string   // Docker image name for the injector container
	InputPath      string   // Path to the distro zip package or release folder
	OutputDir      string   // Optional output parent directory; preserves the input
	IPKPaths       []string // IPK files or directories
	Version        string   // Optional VERSION value to write to /usr/lib/os-release
	PrivateKey     string   // RSA private key for SWU signing (empty = config default)
	Passfile       string   // Key passphrase file (empty = config default)
	KeyDir         string   // Directory containing signing keys (from config)
	Verbose        bool
	SkipChecksums  bool // Caller finalizes checksums after exporting local artifacts
}

// Run optionally injects packages/VERSION and always regenerates release/mfg AVB.
// Without OutputDir, ZIP inputs are replaced only on success and folder inputs
// are modified in-place. OutputDir preserves the source and input artifact type.
func Run(ctx context.Context, opts Options) (string, error) {
	opts.Version = strings.TrimSpace(opts.Version)
	inputPath, err := filepath.Abs(opts.InputPath)
	if err != nil {
		return "", fmt.Errorf("resolving input path: %w", err)
	}
	inputPath, err = filepath.EvalSymlinks(inputPath)
	if err != nil {
		return "", fmt.Errorf("resolving symlinks: %w", err)
	}

	info, err := os.Stat(inputPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("input path not found: %s", inputPath)
		}
		return "", fmt.Errorf("stat input path: %w", err)
	}

	// Resolve IPK paths
	for i, p := range opts.IPKPaths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", fmt.Errorf("resolving path %s: %w", p, err)
		}
		if _, err := os.Stat(abs); os.IsNotExist(err) {
			return "", fmt.Errorf("ipk file or directory not found: %s", p)
		}
		opts.IPKPaths[i] = abs
	}

	if !info.IsDir() && !strings.HasSuffix(inputPath, ".zip") {
		return "", fmt.Errorf("input must be a .zip file or directory: %s", inputPath)
	}
	outputPath := inputPath
	if opts.OutputDir != "" {
		outputPath, err = prepareOutputPath(inputPath, info.IsDir(), opts.OutputDir)
		if err != nil {
			return "", err
		}
	}
	if info.IsDir() {
		if opts.OutputDir != "" {
			if err := fileutil.CopyDir(inputPath, outputPath); err != nil {
				return "", fmt.Errorf("copying artifact to %s: %w", outputPath, err)
			}
		}
		return runFolder(ctx, outputPath, opts)
	}
	return runZip(ctx, inputPath, outputPath, opts)
}

func prepareOutputPath(inputPath string, directory bool, outputDir string) (string, error) {
	outputDir, err := fileutil.ResolveOutputParent(outputDir)
	if err != nil {
		return "", err
	}
	if directory {
		within, err := fileutil.IsWithin(inputPath, outputDir)
		if err != nil {
			return "", fmt.Errorf("checking output directory: %w", err)
		}
		if within {
			return "", fmt.Errorf("output directory must not be inside input artifact directory")
		}
	}
	outputPath := filepath.Join(outputDir, filepath.Base(inputPath))
	if _, err := os.Lstat(outputPath); err == nil {
		return "", fmt.Errorf("output artifact already exists: %s", outputPath)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("checking output artifact %s: %w", outputPath, err)
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("creating output directory: %w", err)
	}
	if directory {
		if err := os.Mkdir(outputPath, 0755); err != nil {
			if os.IsExist(err) {
				return "", fmt.Errorf("output artifact already exists: %s", outputPath)
			}
			return "", fmt.Errorf("creating output artifact %s: %w", outputPath, err)
		}
	}
	return outputPath, nil
}

// injectIntoDir mutates the plain rootfs, signs release/mfg AVB, then rebuilds SWUs.
// It reports whether work occurred, so no-op ZIPs stay untouched.
func injectIntoDir(ctx context.Context, dir string, opts Options) (bool, error) {
	rootfs, swuFiles, err := discoverArtifacts(dir)
	if err != nil {
		return false, err
	}
	keys, err := avbKeys(rootfs, opts)
	if err != nil {
		return false, err
	}
	sort.Strings(swuFiles)
	components := make([]swurepackage.Components, len(swuFiles))
	var stage, fip string
	if len(swuFiles) > 0 {
		if keys == nil {
			return false, fmt.Errorf("SWU rebuilding requires release or mfg AVB identity")
		}
		needsFIP := false
		for i, path := range swuFiles {
			components[i], err = swurepackage.Inspect(path)
			if err != nil {
				return false, fmt.Errorf("inspecting SWU %s before mutation: %w", path, err)
			}
			needsFIP = needsFIP || components[i].FIP != ""
		}
		if needsFIP {
			fip, err = findFIP(filepath.Dir(rootfs))
			if err != nil {
				return false, fmt.Errorf("selecting artifact FIP before mutation: %w", err)
			}
		}
		stage, err = os.MkdirTemp(dir, ".distro-swu-*")
		if err != nil {
			return false, err
		}
		defer os.RemoveAll(stage)
	}
	mutate := len(opts.IPKPaths) > 0 || opts.Version != ""
	if !mutate && keys == nil {
		return false, nil
	}
	if err := docker.PullImage(ctx, opts.ContainerImage); err != nil {
		return false, fmt.Errorf("ensuring injector image: %w\nhint: build locally with just docker-build-injector", err)
	}
	artifactDir := filepath.Dir(rootfs)
	if mutate {
		outputRootfs := rootfs + ".injected"
		installedIPKs, err := opkginject.Run(opkginject.Options{
			ContainerImage: opts.ContainerImage, Image: rootfs, Output: outputRootfs,
			Packages: opts.IPKPaths, Version: opts.Version, Verbose: opts.Verbose,
		})
		if err != nil {
			return false, fmt.Errorf("opkg injection: %w", err)
		}
		if err := os.Rename(outputRootfs, rootfs); err != nil {
			return false, fmt.Errorf("replacing rootfs: %w", err)
		}
		if _, _, err := fileutil.UpdateApplicationManifest(artifactDir, installedIPKs); err != nil {
			return false, fmt.Errorf("updating application manifest: %w", err)
		}
	}
	body, tail := "", ""
	if keys != nil {
		body, tail = rootfs+".avb.body", rootfs+".avb.tail"
		if err := createAVB(ctx, rootfs, opts.ContainerImage, *keys); err != nil {
			return false, err
		}
	}
	if len(swuFiles) > 0 {
		batch := make([]swurepackage.Options, len(swuFiles))
		for i, seed := range swuFiles {
			relative, err := filepath.Rel(dir, seed)
			if err != nil {
				return false, err
			}
			output := filepath.Join(stage, relative)
			if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
				return false, err
			}
			batch[i] = swurepackage.Options{
				OldSWU: seed, Output: output,
				PrivateKey: opts.PrivateKey, Passfile: opts.Passfile, KeyDir: opts.KeyDir,
			}
			if components[i].Rootfs != "" {
				batch[i].NewRootfs, batch[i].AVBBody, batch[i].AVBTail = rootfs, body, tail
				batch[i].SoftwareVersion = opts.Version
			}
			if components[i].FIP != "" {
				batch[i].NewFIP = fip
			}
		}
		if err := swurepackage.RebuildAll(batch); err != nil {
			return false, fmt.Errorf("rebuilding artifact SWUs: %w", err)
		}
		for i, destination := range swuFiles {
			if err := os.Rename(batch[i].Output, destination); err != nil {
				return false, fmt.Errorf("replacing SWU: %w", err)
			}
		}
	}
	return true, nil
}

// runZip extracts the source once and publishes a rebuilt archive on success.
func runZip(ctx context.Context, zipFile, outputPath string, opts Options) (string, error) {
	tmpDir, err := os.MkdirTemp("", "distro-inject-*")
	if err != nil {
		return "", fmt.Errorf("creating temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	fmt.Println("Extracting zip...")
	if err := fileutil.ExtractZip(zipFile, tmpDir); err != nil {
		return "", fmt.Errorf("extracting zip: %w", err)
	}

	changed, err := injectIntoDir(ctx, tmpDir, opts)
	if err != nil {
		return "", err
	}
	if !changed && outputPath == zipFile {
		return zipFile, nil
	}
	if changed && !opts.SkipChecksums {
		if err := writeSHA256(tmpDir); err != nil {
			return "", fmt.Errorf("writing sha.txt: %w", err)
		}
	}

	fmt.Println("Rebuilding zip...")
	if err := replaceZipFromDir(tmpDir, outputPath, opts.OutputDir != ""); err != nil {
		return "", fmt.Errorf("rebuilding zip: %w", err)
	}

	fmt.Printf("Done. Output: %s\n", outputPath)
	return outputPath, nil
}

// runFolder injects IPK packages into a release folder in-place.
func runFolder(ctx context.Context, dir string, opts Options) (string, error) {
	changed, err := injectIntoDir(ctx, dir, opts)
	if err != nil {
		return "", err
	}
	if changed && !opts.SkipChecksums {
		if err := writeSHA256(dir); err != nil {
			return "", fmt.Errorf("writing sha.txt: %w", err)
		}
	}

	fmt.Printf("Done. Modified folder: %s\n", dir)
	return dir, nil
}

func replaceZipFromDir(srcDir, zipFile string, exclusive bool) error {
	tmpFile, err := os.CreateTemp(filepath.Dir(zipFile), filepath.Base(zipFile)+".tmp-*.zip")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	defer os.Remove(tmpPath)

	if err := fileutil.CreateZip(srcDir, tmpPath); err != nil {
		return err
	}
	if exclusive {
		if err := os.Link(tmpPath, zipFile); err != nil {
			if os.IsExist(err) {
				return fmt.Errorf("output artifact already exists: %s", zipFile)
			}
			return err
		}
		return nil
	}
	return os.Rename(tmpPath, zipFile)
}

func writeSHA256(dir string) error {
	if err := fileutil.WriteChecksums(dir, "sha.txt"); err != nil {
		return err
	}
	fmt.Printf("Updated sha.txt: %s\n", filepath.Join(dir, "sha.txt"))
	return nil
}

func discoverArtifacts(dir string) (string, []string, error) {
	var rootfs, swuFiles []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			switch name := info.Name(); {
			case strings.HasSuffix(name, ".rootfs.ext4"):
				rootfs = append(rootfs, path)
			case strings.HasSuffix(name, ".swu"):
				swuFiles = append(swuFiles, path)
			}
		}
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	if len(rootfs) == 0 {
		return "", nil, fmt.Errorf("no rootfs file matching *.rootfs.ext4 found")
	}
	if len(rootfs) > 1 {
		return "", nil, fmt.Errorf("more than 1 rootfs file found:\n%s", strings.Join(rootfs, "\n"))
	}
	return rootfs[0], swuFiles, nil
}

// findFIP requires one unambiguous exported firmware image, never a seed payload.
func findFIP(artifactDir string) (string, error) {
	directory := filepath.Join(artifactDir, "fip")
	info, err := os.Lstat(directory)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("FIP directory must not be a symlink or non-directory: %s", directory)
	}
	matches, err := filepath.Glob(filepath.Join(directory, "fip-*.bin"))
	if err != nil {
		return "", err
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("require exactly one artifact fip/fip-*.bin (found %d)", len(matches))
	}
	info, err = os.Lstat(matches[0])
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return "", fmt.Errorf("artifact FIP must be a nonempty regular file: %s", matches[0])
	}
	return matches[0], nil
}

package postbuild

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"proto-distro/tools/distro/internal/config"
	"proto-distro/tools/distro/internal/fileutil"
)

// ProcessPostBuild exports Yocto sources, optionally injects packages/VERSION,
// signs release/mfg AVB and discovered SWUs, then publishes latest links and a ZIP.
func ProcessPostBuild(cfg *config.Config, configPath, distroDir, machine, distro, variant, distroVersion, injectVersion string) error {
	folderPath, err := CreateArtifactOutput(cfg, configPath, distroDir, machine, distro, variant, distroVersion)
	if err != nil {
		return err
	}

	if err := InjectPackages(cfg, configPath, machine, variant, folderPath, injectVersion); err != nil {
		return fmt.Errorf("post-build injection: %w", err)
	}
	if variant == "release" || variant == "mfg" {
		base := filepath.Join(folderPath, fmt.Sprintf("%s-%s-%s", config.TargetName(variant), machine, distroVersion))
		for _, suffix := range []string{".rootfs.ext4", ".rootfs.ext4.avb.body", ".rootfs.ext4.avb.tail"} {
			info, err := os.Lstat(base + suffix)
			if err != nil {
				return fmt.Errorf("checking final artifact %s: %w", base+suffix, err)
			}
			if !info.Mode().IsRegular() || info.Size() == 0 {
				return fmt.Errorf("final artifact must be a nonempty regular file: %s", base+suffix)
			}
		}
	}

	return FinalizeArtifactOutput(cfg, configPath, machine, variant, folderPath)
}

// CreateArtifactOutput creates the versioned artifact directory and copies the
// configured build artifacts into it. It returns the created artifact folder.
func CreateArtifactOutput(cfg *config.Config, configPath, distroDir, machine, distro, variant, distroVersion string) (string, error) {
	target := config.TargetName(variant)

	artifactsDir := config.ArtifactRootDir(cfg, configPath)

	imagesBasePath := filepath.Join(distroDir, config.ExpandVars(cfg.Artifacts.ImagesBasePath, machine, distro, target, distroVersion))
	if info, err := os.Stat(imagesBasePath); err != nil || !info.IsDir() {
		return "", fmt.Errorf("images base path %s does not exist or is not a directory", imagesBasePath)
	}

	// Collect artifact paths
	artifactPaths, err := collectArtifacts(cfg, machine, distro, variant, distroVersion, imagesBasePath)
	if err != nil {
		return "", fmt.Errorf("collecting artifacts: %w", err)
	}

	// Create versioned output directory
	folderName := fmt.Sprintf("%s-%s-%s", target, machine, distroVersion)
	folderPath := filepath.Join(artifactsDir, folderName)

	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		return "", fmt.Errorf("creating artifacts dir: %w", err)
	}
	if err := os.RemoveAll(folderPath); err != nil {
		return "", fmt.Errorf("removing existing artifact folder: %w", err)
	}
	if err := os.Mkdir(folderPath, 0o755); err != nil {
		return "", fmt.Errorf("creating artifact folder: %w", err)
	}

	// Copy artifacts
	if err := copyArtifacts(artifactPaths, imagesBasePath, folderPath); err != nil {
		return "", fmt.Errorf("copying artifacts: %w", err)
	}

	return folderPath, nil
}

// FinalizeArtifactOutput copies local artifacts, refreshes checksums, and publishes
// latest symlinks and the zip archive for a post-injection artifact folder.
func FinalizeArtifactOutput(cfg *config.Config, configPath, machine, variant, folderPath string) error {
	for _, path := range cfg.Artifacts.LocalFiles {
		source := config.ResolvePath(configPath, path)
		if err := fileutil.CopyFile(source, filepath.Join(folderPath, filepath.Base(source))); err != nil {
			return fmt.Errorf("copying local artifact %s: %w", source, err)
		}
	}
	if err := fileutil.WriteChecksums(folderPath, "sha.txt"); err != nil {
		return fmt.Errorf("writing artifact checksums: %w", err)
	}
	target := config.TargetName(variant)
	artifactsDir := config.ArtifactRootDir(cfg, configPath)
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		return fmt.Errorf("creating artifacts dir: %w", err)
	}

	// Create symlinks
	for _, linkName := range []string{
		fmt.Sprintf("%s-%s-latest", target, machine),
		"miner-image-latest",
	} {
		linkPath := filepath.Join(artifactsDir, linkName)
		linkTarget := symlinkTarget(artifactsDir, folderPath)
		if err := replaceSymlink(linkPath, linkTarget); err != nil {
			return err
		}
	}

	// Create zip archive
	zipPath := folderPath + ".zip"
	if err := fileutil.CreateZip(folderPath, zipPath); err != nil {
		return fmt.Errorf("creating zip: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Created zip archive: %s\n", zipPath)

	// Symlink to the latest zip
	zipLinkPath := filepath.Join(artifactsDir, "miner-image-latest.zip")
	zipTarget := symlinkTarget(artifactsDir, zipPath)
	if err := replaceSymlink(zipLinkPath, zipTarget); err != nil {
		return err
	}

	return nil
}

func symlinkTarget(baseDir, targetPath string) string {
	// Relative links resolve from the physical parent, not its symlink spelling.
	if resolved, err := filepath.EvalSymlinks(baseDir); err == nil {
		baseDir = resolved
	}
	if resolved, err := filepath.EvalSymlinks(targetPath); err == nil {
		targetPath = resolved
	}
	rel, err := filepath.Rel(baseDir, targetPath)
	if err != nil {
		return targetPath
	}
	return rel
}

func replaceSymlink(linkPath, target string) error {
	if err := os.Remove(linkPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing existing symlink %s: %w", filepath.Base(linkPath), err)
	}
	if err := os.Symlink(target, linkPath); err != nil {
		return fmt.Errorf("creating symlink %s: %w", filepath.Base(linkPath), err)
	}
	fmt.Fprintf(os.Stderr, "Linked %s -> %s\n", filepath.Base(linkPath), target)
	return nil
}

// BuildStats prints directory sizes for configured paths.
func BuildStats(cfg *config.Config, distroDir, machine, distro, variant, version string, duration time.Duration) {
	target := config.TargetName(variant)

	fmt.Fprintln(os.Stderr, "Build Statistics:")
	fmt.Fprintf(os.Stderr, "  Build time: %s\n", duration.Truncate(time.Second))
	fmt.Fprintln(os.Stderr, "  Sizes:")
	for _, p := range cfg.BuildStats.Paths {
		expanded := config.ExpandVars(p, machine, distro, target, version)
		fullPath := filepath.Join(distroDir, expanded)
		size := directorySize(fullPath)
		name := filepath.Base(fullPath)
		fmt.Fprintf(os.Stderr, "    %-70s: %s\n", name, formatSize(size))
	}
}

// collectArtifacts resolves artifact file/dir paths from config, following symlinks.
func collectArtifacts(cfg *config.Config, machine, distro, variant, distroVersion, imagesBasePath string) ([]string, error) {
	target := config.TargetName(variant)
	var paths []string

	// Image artifacts (relative to images_base_path)
	for _, patterns := range [][]string{cfg.Artifacts.Files, cfg.Artifacts.Variants[variant].Files} {
		for _, pattern := range patterns {
			expanded := config.ExpandVars(pattern, machine, distro, target, distroVersion)
			fullPath := filepath.Join(imagesBasePath, expanded)

			// Resolve symlinks
			resolved, err := filepath.EvalSymlinks(fullPath)
			if err != nil {
				return nil, fmt.Errorf("artifact file not found: %s (from pattern %q): %w", fullPath, pattern, err)
			}
			fullPath = resolved

			if _, err := os.Stat(fullPath); err != nil {
				return nil, fmt.Errorf("artifact file not found after symlink resolution: %s (from pattern %q): %w", fullPath, pattern, err)
			}
			paths = append(paths, fullPath)
		}
	}

	return paths, nil
}

// copyArtifacts copies files and directories into the destination folder.
func copyArtifacts(artifactPaths []string, imagesBasePath, destDir string) error {
	verbose := os.Getenv("DISTRO_VERBOSE") != ""

	for _, src := range artifactPaths {
		info, err := os.Stat(src)
		if err != nil {
			continue
		}

		// Determine destination name
		var destName string
		if rel, err := filepath.Rel(imagesBasePath, src); err == nil && !strings.HasPrefix(rel, "..") {
			destName = rel
		} else {
			destName = filepath.Base(src)
		}
		dest := filepath.Join(destDir, destName)

		if verbose {
			kind := "file"
			if info.IsDir() {
				kind = "dir"
			}
			fmt.Fprintf(os.Stderr, "  [copy] %s (%s) -> %s\n", src, kind, dest)
		}

		if info.IsDir() {
			if err := copyDir(src, dest); err != nil {
				return err
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return err
			}
			if err := fileutil.CopyFile(src, dest); err != nil {
				return err
			}
		}
	}

	size := directorySize(destDir)
	fmt.Fprintf(os.Stderr, "Copied artifacts (%s) to %s/\n", formatSize(size), destDir)
	return nil
}

// copyDir recursively copies a directory, following symlinks.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)

		// Resolve symlinks to their real targets.
		realPath := path
		if d.Type()&fs.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return fmt.Errorf("resolving symlink %s: %w", path, err)
			}
			realPath = resolved
			info, err := os.Stat(realPath)
			if err != nil {
				return fmt.Errorf("stat resolved symlink %s: %w", realPath, err)
			}
			if info.IsDir() {
				// Symlink points to a directory; recurse into it.
				return copyDir(realPath, target)
			}
		}

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return fileutil.CopyFile(realPath, target)
	})
}

// directorySize returns the total size of all files in a path.
func directorySize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	if !info.IsDir() {
		return info.Size()
	}

	var total int64
	filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}

// formatSize formats bytes into human-readable format.
func formatSize(size int64) string {
	s := float64(size)
	for _, unit := range []string{"B", "KB", "MB", "GB", "TB"} {
		if s < 1000 {
			return fmt.Sprintf("%.2f %s", s, unit)
		}
		s /= 1000
	}
	return "0"
}

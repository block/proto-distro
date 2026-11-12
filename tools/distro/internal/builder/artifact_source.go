package builder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"proto-distro/tools/distro/internal/config"
	"proto-distro/tools/distro/internal/docker"
)

// ensureBuildOutputsAvailable copies the Yocto deploy/images directory from the
// Docker build volume to the host so that post-build steps can access the files.
// On Linux the build dir is a bind mount and this is a no-op; on macOS the build
// dir lives in a Docker named volume and must be synced explicitly.
func ensureBuildOutputsAvailable(ctx context.Context, cfg *config.Config, distroDir, machine, distro, target string) error {
	imagesRel := filepath.Clean(config.ExpandVars(cfg.Artifacts.ImagesBasePath, machine, distro, target, ""))
	hostImagesPath := filepath.Join(distroDir, imagesRel)

	volume := cfg.Docker.Builder.BuildVolume
	if volume == "" {
		return fmt.Errorf("build volume is not configured (docker.builder.build_volume)")
	}
	if err := docker.VolumeExists(ctx, volume); err != nil {
		return err
	}

	volumeRel, err := toVolumeRelativePath(imagesRel, cfg.Docker.Builder.BuildDir)
	if err != nil {
		return fmt.Errorf("invalid build path %q: %w", imagesRel, err)
	}

	fmt.Fprintf(os.Stderr, "Syncing build outputs from Docker volume %s to %s...\n", volume, hostImagesPath)

	if err := syncFromBuildVolume(ctx, volume, cfg.Docker.Builder.Image, distroDir, imagesRel, volumeRel); err != nil {
		return err
	}

	if !dirExists(hostImagesPath) {
		return fmt.Errorf("images base path %s does not exist after volume sync", hostImagesPath)
	}

	return nil
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func syncFromBuildVolume(ctx context.Context, volume, image, distroDir, hostRel, volumeRel string) error {
	args := []string{
		"run", "--rm",
		"--entrypoint", "/bin/bash",
		"-v", fmt.Sprintf("%s:/from", volume),
		"-v", fmt.Sprintf("%s:/to", distroDir),
		image,
		"-lc",
		fmt.Sprintf(
			`set -e; src="/from/%s"; dst="/to/%s"; if [ ! -e "$src" ]; then echo "required path missing in build volume: %s" >&2; exit 1; fi; mkdir -p "$(dirname "$dst")"; rm -rf "$dst"; cp -a "$src" "$dst"`,
			volumeRel, hostRel, volumeRel,
		),
	}

	if err := docker.Run(ctx, args, docker.RunOptions{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}); err != nil {
		return fmt.Errorf("syncing build path %q from volume %q failed: %w", hostRel, volume, err)
	}

	return nil
}

func toVolumeRelativePath(hostRel, buildDir string) (string, error) {
	if filepath.IsAbs(hostRel) {
		return "", fmt.Errorf("path must be relative: %s", hostRel)
	}

	cleaned := filepath.Clean(filepath.ToSlash(hostRel))
	if cleaned == "." || cleaned == "" {
		return "", fmt.Errorf("empty path")
	}

	if cleaned == buildDir {
		return ".", nil
	}

	prefix := buildDir + "/"
	if strings.HasPrefix(cleaned, prefix) {
		trimmed := strings.TrimPrefix(cleaned, prefix)
		if trimmed == "" {
			return ".", nil
		}
		return trimmed, nil
	}

	return cleaned, nil
}

package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// RunOptions controls stdio and cancellation behavior for a docker invocation.
type RunOptions struct {
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	WaitDelay time.Duration
}

// CheckAvailable verifies that Docker is installed and running.
func CheckAvailable(ctx context.Context) error {
	if err := Run(ctx, []string{"version"}, RunOptions{}); err != nil {
		return fmt.Errorf("Docker is not available or not running. Please ensure Docker is installed and running: %w", err)
	}
	return nil
}

// CheckContainerImage verifies the given container image exists locally.
func CheckContainerImage(ctx context.Context, image string) error {
	if err := Run(ctx, []string{"image", "inspect", image}, RunOptions{}); err != nil {
		return fmt.Errorf("Docker image '%s' not found. Build it first or run the ensure step", image)
	}
	return nil
}

// Run executes docker with the provided arguments.
func Run(ctx context.Context, args []string, opts RunOptions) error {
	LogCommand(args)
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = opts.Stdin
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	if opts.WaitDelay > 0 {
		cmd.Cancel = func() error {
			if cmd.Process == nil {
				return nil
			}
			return cmd.Process.Signal(syscall.SIGTERM)
		}
		cmd.WaitDelay = opts.WaitDelay
	}
	return cmd.Run()
}

// VolumeExists verifies the given Docker volume exists.
func VolumeExists(ctx context.Context, name string) error {
	args := []string{"volume", "inspect", name}
	if err := Run(ctx, args, RunOptions{}); err != nil {
		return fmt.Errorf("docker volume %q does not exist", name)
	}
	return nil
}

// EnsureVolume creates a Docker volume if it does not already exist.
func EnsureVolume(ctx context.Context, name string) error {
	if err := VolumeExists(ctx, name); err == nil {
		return nil
	}

	args := []string{"volume", "create", name}
	if err := Run(ctx, args, RunOptions{}); err != nil {
		return fmt.Errorf("failed to create Docker volume %s: %w", name, err)
	}
	fmt.Fprintf(os.Stderr, "Created Docker volume %s\n", name)
	return nil
}

// PullImage ensures a Docker image is present by pulling from the registry.
// If the image already exists locally, it is used as-is.
// It does not fall back to a local build.
func PullImage(ctx context.Context, image string) error {
	if err := CheckContainerImage(ctx, image); err == nil {
		return nil
	}

	fmt.Fprintf(os.Stderr, "Pulling %s...\n", image)
	if err := Run(ctx, []string{"pull", image}, RunOptions{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}); err != nil {
		return fmt.Errorf("failed to pull Docker image %s: %w", image, err)
	}

	return nil
}

// RunContainer runs the opkg-injector container with the given image,
// packages, and optional os-release VERSION override.
func RunContainer(ctx context.Context, image, imagePath, packagesDir string, estimatedInstallSize int64, version string, verbose bool) error {
	args := []string{
		"run", "--rm", "--privileged",
		"-e", fmt.Sprintf("ESTIMATED_INSTALL_SIZE=%d", estimatedInstallSize),
		"-v", fmt.Sprintf("%s:/work/image.ext4", imagePath),
		"-v", fmt.Sprintf("%s:/work/packages", packagesDir),
	}
	if version != "" {
		args = append(args, "-e", fmt.Sprintf("OS_RELEASE_VERSION=%s", version))
	}
	args = append(args,
		image,
	)
	if verbose {
		args = append(args, "-v")
	}

	return Run(ctx, args, RunOptions{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
}

// LogCommand prints a docker command to stderr when DISTRO_VERBOSE is set.
func LogCommand(args []string) {
	if os.Getenv("DISTRO_VERBOSE") == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "+ docker %s\n", strings.Join(args, " "))
}

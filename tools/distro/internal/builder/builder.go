package builder

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"proto-distro/tools/distro/internal/config"
	"proto-distro/tools/distro/internal/docker"
	"proto-distro/tools/distro/internal/dotenv"
	"proto-distro/tools/distro/internal/postbuild"
)

// Options configures a builder invocation.
type Options struct {
	ConfigPath    string
	Machine       string
	Distro        string
	Variant       string
	Command       string // "build", "shell", "clean", or "layers"
	InjectVersion string // when non-empty, run injection and set this version in os-release
	OutputDir     string // optional output parent, relative to the working directory
}

// buildContext holds the resolved state needed to assemble and run a docker build.
type buildContext struct {
	cfg                *config.Config
	opts               Options
	target             string // full Yocto target name (miner-image-{variant})
	distroDir          string
	absDistroDir       string
	containerDistroDir string
	gitHash            string
	distroVersion      string
	buildDatetime      string
}

// Run executes the build orchestration.
func Run(ctx context.Context, opts Options) error {
	bctx, err := newBuildContext(ctx, opts)
	if err != nil {
		return err
	}

	if err := prepareBuildEnvironment(ctx, bctx); err != nil {
		return err
	}

	args := bctx.dockerRunArgs()

	buildStart := time.Now()

	if err := docker.Run(ctx, args, docker.RunOptions{
		Stdin:     os.Stdin,
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		WaitDelay: 3 * time.Second,
	}); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("interrupted")
		}
		return fmt.Errorf("docker run failed: %w", err)
	}

	if bctx.opts.Command == "build" && strings.Contains(bctx.target, "image") {
		duration := time.Since(buildStart)
		if runtime.GOOS == "darwin" {
			if err := ensureBuildOutputsAvailable(ctx, bctx.cfg, bctx.distroDir, bctx.opts.Machine, bctx.opts.Distro, bctx.target); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to sync build outputs from volume: %v\n", err)
			}
		}
		if err := postbuild.ProcessPostBuild(bctx.cfg, bctx.opts.ConfigPath, bctx.distroDir, bctx.opts.Machine, bctx.opts.Distro, bctx.opts.Variant, bctx.distroVersion, bctx.opts.InjectVersion, bctx.opts.OutputDir); err != nil {
			return fmt.Errorf("post-build processing failed: %w", err)
		}
		postbuild.BuildStats(bctx.cfg, bctx.distroDir, bctx.opts.Machine, bctx.opts.Distro, bctx.opts.Variant, bctx.distroVersion, duration)
	}

	return nil
}

// newBuildContext loads config, resolves paths, loads .env, detects versions,
// and returns a fully populated buildContext ready for docker arg assembly.
func newBuildContext(ctx context.Context, opts Options) (*buildContext, error) {
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	if opts.Machine == "" {
		opts.Machine = cfg.Defaults.Machine
	}
	if opts.Distro == "" {
		opts.Distro = cfg.Defaults.Distro
	}
	if opts.Variant == "" {
		opts.Variant = cfg.Defaults.Variant
	}

	distroDir := filepath.Dir(opts.ConfigPath)

	// Load .env before version detection so env overrides like DISTRO_VERSION take effect.
	if cfg.Defaults.EnvFile != "" {
		envPath := config.ResolvePath(opts.ConfigPath, cfg.Defaults.EnvFile)
		if err := dotenv.Load(envPath); err != nil {
			return nil, fmt.Errorf("loading .env: %w", err)
		}
	}

	absDistroDir, err := filepath.Abs(distroDir)
	if err != nil {
		return nil, fmt.Errorf("resolving distro dir: %w", err)
	}

	gitHash, err := gitVersion(ctx, false)
	if err != nil {
		return nil, fmt.Errorf("detecting git version: %w", err)
	}

	distroVersion, err := DistroVersionString(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("detecting distro version: %w", err)
	}
	if opts.OutputDir != "" {
		folderPath, err := postbuild.ArtifactOutputPath(opts.OutputDir, opts.Machine, opts.Variant, distroVersion)
		if err != nil {
			return nil, err
		}
		opts.OutputDir = filepath.Dir(folderPath)
	}

	return &buildContext{
		cfg:                cfg,
		opts:               opts,
		target:             config.TargetName(opts.Variant),
		distroDir:          distroDir,
		absDistroDir:       absDistroDir,
		containerDistroDir: cfg.Docker.Builder.ContainerDistroDir,
		gitHash:            gitHash,
		distroVersion:      distroVersion,
		buildDatetime:      time.Now().UTC().Format("20060102T150405Z"),
	}, nil
}

// prepareBuildEnvironment handles side effects needed before docker run:
// checking docker, ensuring volumes/images, and creating output dirs.
func prepareBuildEnvironment(ctx context.Context, bctx *buildContext) error {
	if err := ensureSubmodulesInitialized(ctx, bctx.absDistroDir); err != nil {
		return err
	}

	if err := docker.CheckAvailable(ctx); err != nil {
		return err
	}

	if runtime.GOOS == "darwin" {
		if err := docker.EnsureVolume(ctx, bctx.cfg.Docker.Builder.BuildVolume); err != nil {
			return err
		}
	}

	if err := docker.PullImage(ctx, bctx.cfg.Docker.Builder.Image); err != nil {
		return fmt.Errorf("ensuring builder image: %w\nhint: you can build it locally with: just docker-build-builder", err)
	}

	artifactsDir := config.ArtifactRootDir(bctx.cfg, bctx.opts.ConfigPath)
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		return fmt.Errorf("creating artifacts directory: %w", err)
	}
	buildDir := config.ResolvePath(bctx.opts.ConfigPath, bctx.cfg.Docker.Builder.BuildDir)
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return fmt.Errorf("creating build directory: %w", err)
	}

	return nil
}

// dockerRunArgs assembles the full argument list for the `docker run` invocation.
func (bctx *buildContext) dockerRunArgs() []string {
	args := []string{
		"run",
		"--hostname", bctx.cfg.Docker.Builder.Hostname,
		"--rm",
		"-v", fmt.Sprintf("%s:%s/", bctx.absDistroDir, bctx.containerDistroDir),
	}

	if bctx.opts.Command == "shell" {
		args = append(args, "-it")
	} else if isTerminal(os.Stdout.Fd()) {
		args = append(args, "-t")
	}

	args = append(args,
		"-e", fmt.Sprintf("GIT_HASH=%s", bctx.gitHash),
		"-e", fmt.Sprintf("DISTRO_VERSION=%s", bctx.distroVersion),
		"-e", fmt.Sprintf("BUILD_DATETIME=%s", bctx.buildDatetime),
		"-e", fmt.Sprintf("MACHINE=%s", bctx.opts.Machine),
		"-e", fmt.Sprintf("DISTRO=%s", bctx.opts.Distro),
		"-e", fmt.Sprintf("TARGET=%s", bctx.target),
		"-e", fmt.Sprintf("TEMPLATECONF=%s/sources/meta-miner/conf/templates/proto", bctx.containerDistroDir),
	)

	if runtime.GOOS == "darwin" {
		buildDir := bctx.cfg.Docker.Builder.BuildDir
		builderBuildDir := bctx.containerDistroDir + "/" + buildDir
		args = append(args, "-v", fmt.Sprintf("%s:%s", bctx.cfg.Docker.Builder.BuildVolume, builderBuildDir))
		hostBuildDir := config.ResolvePath(bctx.opts.ConfigPath, buildDir)
		if abs, err := filepath.Abs(hostBuildDir); err == nil {
			hostBuildDir = abs
		}
		args = append(args, "-v", fmt.Sprintf("%s/workspace:%s/workspace", hostBuildDir, builderBuildDir))
	}

	for _, envVar := range bctx.cfg.Docker.Builder.EnvPassthrough {
		if val := os.Getenv(envVar); val != "" {
			args = append(args, "-e", fmt.Sprintf("%s=%s", envVar, val))
		}
	}

	args = append(args, bctx.cfg.Docker.Builder.Image)

	switch bctx.opts.Command {
	case "build":
		args = append(args, "build")
	case "shell":
		imageIdx := len(args) - 1
		image := args[imageIdx]
		args = args[:imageIdx]
		args = append(args, "--entrypoint", "/bin/bash", image)
		args = append(args, "-c", bctx.cfg.Docker.Builder.ShellInit)
	case "clean":
		args = append(args, "clean")
	case "layers":
		args = append(args, "layers")
	}

	return args
}

// ensureSubmodulesInitialized runs `git submodule update --init --recursive`
// when any submodule path is uninitialized, so a fresh checkout can `distro
// build`/`distro shell` without a manual bootstrap step. Status is checked
// recursively so nested uninitialized submodules under already-initialized
// parents are also surfaced.
func ensureSubmodulesInitialized(ctx context.Context, repoDir string) error {
	out, err := execOutput(ctx, "git", "-C", repoDir, "submodule", "status", "--recursive")
	if err != nil {
		return fmt.Errorf("checking submodule status: %w", err)
	}

	uninitialized := parseUninitializedSubmodules(out)
	if len(uninitialized) == 0 {
		return nil
	}

	fmt.Fprintf(os.Stderr, "Initializing %d submodule(s): %s\n", len(uninitialized), strings.Join(uninitialized, ", "))
	args := append([]string{"-C", repoDir, "submodule", "update", "--init", "--recursive", "--"}, uninitialized...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("initializing submodules: %w", err)
	}
	return nil
}

// parseUninitializedSubmodules returns the submodule paths flagged as
// uninitialized (lines prefixed with `-`) in `git submodule status` output.
func parseUninitializedSubmodules(statusOutput string) []string {
	var paths []string
	for _, line := range strings.Split(statusOutput, "\n") {
		if !strings.HasPrefix(line, "-") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			paths = append(paths, fields[1])
		}
	}
	return paths
}

// gitVersion returns a full or abbreviated git hash with an optional -dirty suffix.
func gitVersion(ctx context.Context, short bool) (string, error) {
	args := []string{"rev-parse", "HEAD"}
	if short {
		args = []string{"rev-parse", "--short", "HEAD"}
	}
	hash, err := execOutput(ctx, "git", args...)
	if err != nil {
		return "", err
	}

	dirty := ""
	cmd := exec.CommandContext(ctx, "git", "diff", "--quiet", "--ignore-submodules", "HEAD")
	if err := cmd.Run(); err != nil {
		dirty = "-dirty"
	}

	return hash + dirty, nil
}

// DistroVersionString returns the version for the distro build.
// If HEAD is tagged with cfg.Version.TagPrefix*, uses that tag without the prefix.
// Otherwise uses short git SHA, with "-dirty" appended when the working tree is dirty.
func DistroVersionString(ctx context.Context, cfg *config.Config) (string, error) {
	if version, ok := os.LookupEnv("DISTRO_VERSION"); ok {
		fmt.Fprintf(os.Stderr, "Building with DISTRO_VERSION override: %s\n", version)
		return version, nil
	}
	if cfg == nil {
		return "", fmt.Errorf("config is required when DISTRO_VERSION is not set")
	}

	// Try git describe for a tag-based version
	desc, err := execOutput(ctx, "git", "describe", "--tags", "--exact-match", "--match="+cfg.Version.TagPrefix+"*")
	if err == nil {
		version := strings.TrimPrefix(desc, cfg.Version.TagPrefix)
		fmt.Fprintf(os.Stderr, "Building with git version: %s\n", version)
		return version, nil
	}

	// Fall back to short rev, adding a dirty suffix when applicable.
	version, err := gitVersion(ctx, true)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stderr, "Building with git version: %s\n", version)
	return version, nil
}

// isTerminal reports whether the given file descriptor is a terminal.
func isTerminal(fd uintptr) bool {
	var termios syscall.Termios
	_, _, err := syscall.Syscall6(syscall.SYS_IOCTL, fd, ioctlGetTermios, uintptr(unsafe.Pointer(&termios)), 0, 0, 0)
	return err == 0
}

// execOutput runs a command and returns its trimmed stdout.
func execOutput(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

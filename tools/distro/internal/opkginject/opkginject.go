// Package opkginject implements the host-side logic for injecting IPK packages
// into an ext4 rootfs image using a Docker container.
package opkginject

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"proto-distro/tools/distro/internal/docker"
	"proto-distro/tools/distro/internal/fileutil"
)

const (
	// ExpansionFactor is the IPK package size to installed size multiplier.
	// Accounts for package extraction, metadata, and filesystem overhead.
	ExpansionFactor = 2.0
)

// Options configures an opkg inject operation.
type Options struct {
	ContainerImage string   // Docker image name for the injector container
	Image          string   // Input ext4 image path
	Output         string   // Output ext4 image path
	Packages       []string // IPK files or directories containing IPK files
	Version        string   // Optional VERSION value to write to /usr/lib/os-release
	Verbose        bool
}

type workspace struct {
	imagePath   string
	packagesDir string
	imageSize   int64
}

type sizeStats struct {
	totalPackageSize   int64
	estimatedInstall   int64
	expectedOutputSize int64
}

// Run performs the rootfs update: copies the image, collects packages,
// optionally updates os-release VERSION inside the mounted rootfs, and copies
// the result to output. It returns the collected IPK files that were passed to
// the injector container.
func Run(opts Options) ([]string, error) {
	opts, err := normalizeOptions(opts)
	if err != nil {
		return nil, err
	}
	if err := validatePaths(opts); err != nil {
		return nil, err
	}

	ctx := context.Background()
	if err := checkDocker(ctx, opts.ContainerImage); err != nil {
		return nil, err
	}

	ws, cleanup, err := prepareWorkspace(opts)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	ipkFiles, err := collectPackages(opts, ws.packagesDir)
	if err != nil {
		return nil, err
	}

	stats, err := reportPackageAndSizeInfo(opts, ws.imageSize, ipkFiles)
	if err != nil {
		return nil, err
	}

	if err := runInjector(ctx, opts, ws, stats.estimatedInstall, ipkFiles); err != nil {
		return nil, err
	}

	if err := writeOutput(opts, ws.imagePath, ws.imageSize, stats.estimatedInstall); err != nil {
		return nil, err
	}

	return ipkFiles, nil
}

func normalizeOptions(opts Options) (Options, error) {
	var err error
	opts.Version = strings.TrimSpace(opts.Version)
	opts.Image, err = filepath.Abs(opts.Image)
	if err != nil {
		return opts, fmt.Errorf("resolving image path: %w", err)
	}
	opts.Output, err = filepath.Abs(opts.Output)
	if err != nil {
		return opts, fmt.Errorf("resolving output path: %w", err)
	}
	return opts, nil
}

func validatePaths(opts Options) error {
	if opts.Image == opts.Output {
		return fmt.Errorf("output path cannot be the same as input image")
	}

	if _, err := os.Stat(opts.Image); os.IsNotExist(err) {
		return fmt.Errorf("image file not found: %s", opts.Image)
	}

	outputDir := filepath.Dir(opts.Output)
	if info, err := os.Stat(outputDir); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("output directory does not exist: %s", outputDir)
		}
		return fmt.Errorf("checking output directory: %w", err)
	} else if !info.IsDir() {
		return fmt.Errorf("output parent path is not a directory: %s", outputDir)
	}

	return nil
}

func checkDocker(ctx context.Context, image string) error {
	if err := docker.CheckAvailable(ctx); err != nil {
		return err
	}
	return docker.CheckContainerImage(ctx, image)
}

func prepareWorkspace(opts Options) (workspace, func(), error) {
	tmpDir, err := os.MkdirTemp("", "opkg-injector-*")
	if err != nil {
		return workspace{}, nil, fmt.Errorf("creating temp directory: %w", err)
	}
	cleanup := func() { os.RemoveAll(tmpDir) }

	imageInfo, err := os.Stat(opts.Image)
	if err != nil {
		cleanup()
		return workspace{}, nil, fmt.Errorf("getting image size: %w", err)
	}

	fmt.Println("Copying image to temp directory...")
	tmpImage := filepath.Join(tmpDir, "image.ext4")
	if err := fileutil.CopyFile(opts.Image, tmpImage); err != nil {
		cleanup()
		return workspace{}, nil, fmt.Errorf("copying image: %w", err)
	}

	packagesDir := filepath.Join(tmpDir, "packages")
	if err := os.MkdirAll(packagesDir, 0755); err != nil {
		cleanup()
		return workspace{}, nil, fmt.Errorf("creating packages directory: %w", err)
	}

	return workspace{
		imagePath:   tmpImage,
		packagesDir: packagesDir,
		imageSize:   imageInfo.Size(),
	}, cleanup, nil
}

func collectPackages(opts Options, packagesDir string) ([]string, error) {
	ipkFiles, err := fileutil.CollectIPKFiles(opts.Packages, packagesDir)
	if err != nil {
		return nil, fmt.Errorf("collecting IPK files: %w", err)
	}

	if len(ipkFiles) == 0 && (len(opts.Packages) > 0 || opts.Version == "") {
		return nil, fmt.Errorf("no IPK files found in provided paths")
	}

	return ipkFiles, nil
}

func reportPackageAndSizeInfo(opts Options, imageSize int64, ipkFiles []string) (sizeStats, error) {
	totalSize, err := reportPackageInfo(opts, ipkFiles)
	if err != nil {
		return sizeStats{}, err
	}

	estimatedInstall := int64(float64(totalSize) * ExpansionFactor)
	stats := sizeStats{
		totalPackageSize:   totalSize,
		estimatedInstall:   estimatedInstall,
		expectedOutputSize: imageSize + estimatedInstall,
	}
	reportSizeInfo(opts, imageSize, stats)
	return stats, nil
}

func reportPackageInfo(opts Options, ipkFiles []string) (int64, error) {
	if len(ipkFiles) == 0 {
		if opts.Version != "" {
			fmt.Println("\n=== Package Information ===")
			fmt.Println("  No IPK packages provided; only updating /usr/lib/os-release VERSION.")
		}
		return 0, nil
	}

	var totalSize int64
	fmt.Println("\n=== Package Information ===")
	for _, ipk := range ipkFiles {
		info, err := os.Stat(ipk)
		if err != nil {
			return 0, fmt.Errorf("getting size of %s: %w", ipk, err)
		}
		size := info.Size()
		totalSize += size
		fmt.Printf("  %-50s %8.2f MB\n", filepath.Base(ipk), float64(size)/1_000_000)
	}
	return totalSize, nil
}

func reportSizeInfo(opts Options, imageSize int64, stats sizeStats) {
	fmt.Println("\n=== Size Statistics ===")
	fmt.Printf("Input image size:                 %10.2f MB\n", float64(imageSize)/1_000_000)
	fmt.Printf("Total package files:              %10.2f MB\n", float64(stats.totalPackageSize)/1_000_000)
	fmt.Printf("Estimated install size (%.1fx):       %10.2f MB\n", ExpansionFactor, float64(stats.estimatedInstall)/1_000_000)
	fmt.Printf("Expected output image size:       %10.2f MB\n", float64(stats.expectedOutputSize)/1_000_000)
	if opts.Version != "" {
		fmt.Printf("Requested os-release VERSION:     %s\n", opts.Version)
	}
}

func runInjector(ctx context.Context, opts Options, ws workspace, estimatedInstallSize int64, ipkFiles []string) error {
	fmt.Println("\n=== Running Injection ===")
	if err := docker.RunContainer(ctx, opts.ContainerImage, ws.imagePath, ws.packagesDir, estimatedInstallSize, opts.Version, opts.Verbose); err != nil {
		return injectionError(err, len(ipkFiles) == 0)
	}
	return nil
}

func injectionError(err error, versionOnly bool) error {
	if versionOnly {
		return fmt.Errorf("rootfs update failed: %w", err)
	}
	suggestedFactor := ExpansionFactor + 0.5
	if suggestedFactor > 5.0 {
		suggestedFactor = 5.0
	}
	return fmt.Errorf("package injection failed: %w\n\n"+
		"Common causes:\n"+
		"  - Insufficient space in image (current expansion factor: %.1fx)\n"+
		"    Try increasing ExpansionFactor to %.1f\n"+
		"  - Package installation errors (check output above)\n",
		err, ExpansionFactor, suggestedFactor)
}

func writeOutput(opts Options, tmpImage string, imageSize, estimatedInstallSize int64) error {
	fmt.Println("\nCopying modified image to output...")
	if err := fileutil.CopyFile(tmpImage, opts.Output); err != nil {
		return fmt.Errorf("copying output: %w", err)
	}

	outputInfo, err := os.Stat(opts.Output)
	if err != nil {
		return fmt.Errorf("getting output size: %w", err)
	}
	actualOutputSize := outputInfo.Size()
	sizeDiff := actualOutputSize - imageSize

	fmt.Println("\n=== Final Results ===")
	fmt.Printf("Output image:         %s\n", opts.Output)
	fmt.Printf("Actual output size:   %10.2f MB\n", float64(actualOutputSize)/1_000_000)
	fmt.Printf("Size increase:        %10.2f MB\n", float64(sizeDiff)/1_000_000)
	fmt.Printf("Expected increase:    %10.2f MB\n", float64(estimatedInstallSize)/1_000_000)
	if opts.Version != "" {
		fmt.Printf("Updated VERSION:      %s\n", opts.Version)
	}

	return nil
}

// Package main implements the IPK package injector that runs inside the Docker container.
// It mounts an ext4 image and installs opkg packages into it.
// Changing this file requires rebuilding the injector docker image
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"proto-distro/tools/distro/internal/color"
	"proto-distro/tools/distro/internal/fileutil"
)

const (
	imageFile    = "/work/image.ext4"
	packagesDir  = "/work/packages"
	rootfsMount  = "/work/rootfs"
	opkgConf     = "/tmp/opkg.conf"
	osReleaseRel = "usr/lib/os-release"

	// opkgInfoRel is opkg's per-package info dir, relative to rootfsMount.
	opkgInfoRel = "var/lib/opkg/info"
)

// opkgConfig must pin the status/info/lists paths to the same locations the
// device's opkg.conf uses (/var/lib/opkg). With --offline-root these are written
// under the mounted rootfs, so the on-device opkg reads the injected packages.
// Without these options opkg falls back to its compiled-in default lib dir, which
// the device opkg does not read, leaving `opkg list-installed` empty.
const opkgConfig = `dest root /
arch all 1
arch any 6
arch arm 10
arch armhf 10
arch armv7vet2hf-neon 10
arch armv7vehf-neon 9
option lists_dir   /var/lib/opkg/lists
option info_dir    /var/lib/opkg/info
option status_file /var/lib/opkg/status
`

var (
	mounted            bool
	verbose            bool
	osReleaseVersionRe = regexp.MustCompile(`(?m)^VERSION=.*$`)
)

func main() {
	flag.BoolVar(&verbose, "v", false, "verbose output")
	flag.BoolVar(&verbose, "verbose", false, "verbose output")
	flag.Parse()

	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, color.Errorf("Error: %v", err))
		cleanup()
		os.Exit(1)
	}
}

func run() error {
	if err := os.MkdirAll(rootfsMount, 0755); err != nil {
		return fmt.Errorf("failed to create mount point: %w", err)
	}

	if err := validateImage(); err != nil {
		return err
	}

	if err := runE2fsck("initial check"); err != nil {
		return err
	}

	estimatedSize := getEstimatedInstallSize()
	if estimatedSize > 0 {
		if err := resizeImage(estimatedSize); err != nil {
			return err
		}
	}

	if err := mountImage(); err != nil {
		return err
	}
	defer cleanup()

	blockSize, freeBlocks, err := getImageStats()
	if err != nil {
		return err
	}
	const mb int64 = 1_000_000

	freeBytes := blockSize * freeBlocks
	fmt.Printf("Available space in mounted image: %dMB\n", freeBytes/mb)

	if estimatedSize > 0 && freeBytes < estimatedSize {
		fmt.Println(color.Warnf("Warning: Available space (%dMB) is less than estimated requirement (%dMB)",
			freeBytes/mb, estimatedSize/mb))
		fmt.Println(color.Warnf("Installation may fail due to insufficient space"))
	}

	if err := writeOpkgConfig(); err != nil {
		return err
	}

	if verbose {
		fmt.Printf("\n=== opkg config (%s) ===\n%s\n", opkgConf, opkgConfig)
	}

	listsDir := filepath.Join(rootfsMount, "var/lib/opkg/lists")
	if err := os.MkdirAll(listsDir, 0755); err != nil {
		return fmt.Errorf("failed to create opkg lists directory: %w", err)
	}

	infoDir := filepath.Join(rootfsMount, opkgInfoRel)
	if err := os.MkdirAll(infoDir, 0755); err != nil {
		return fmt.Errorf("failed to create opkg info directory: %w", err)
	}

	installedPkgs, err := installPackages()
	if err != nil {
		return err
	}

	if err := runDeferredPostinstalls(installedPkgs); err != nil {
		return err
	}

	if err := markPackagesInstalled(installedPkgs); err != nil {
		return err
	}

	version := getOSReleaseVersion()
	if version != "" {
		if err := updateOSReleaseVersion(version); err != nil {
			return err
		}
	}

	if err := unmountImage(); err != nil {
		return err
	}

	if err := runE2fsck("final check"); err != nil {
		return err
	}

	if version != "" {
		fmt.Printf("Package injection complete! Installed %d package(s). Updated VERSION to %s.\n", len(installedPkgs), version)
	} else {
		fmt.Printf("Package injection complete! Installed %d package(s).\n", len(installedPkgs))
	}
	return nil
}

func validateImage() error {
	fmt.Println("Validating image...")

	if _, err := os.Stat(imageFile); os.IsNotExist(err) {
		return fmt.Errorf("image file not found: %s", imageFile)
	}

	output, err := runCommandOutput("file", imageFile)
	if err != nil {
		return fmt.Errorf("failed to check file type: %w", err)
	}

	if !strings.Contains(output, "ext4 filesystem") {
		return fmt.Errorf("%s is not an ext4 filesystem: %s", imageFile, strings.TrimSpace(output))
	}

	return nil
}

func runE2fsck(context string) error {
	fmt.Printf("Running filesystem check (%s)...\n", context)

	cmd := exec.Command("e2fsck", "-f", "-y", imageFile)
	if verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}

	err := cmd.Run()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return fmt.Errorf("e2fsck failed: %w", err)
		}
	}

	// Exit codes 0-3 are acceptable
	// 0 = no errors, 1 = errors corrected, 2 = reboot recommended, 3 = combination
	// >= 4 = critical errors
	if exitCode >= 4 {
		return fmt.Errorf("filesystem check failed with exit code %d - critical errors that cannot be automatically fixed", exitCode)
	}

	switch exitCode {
	case 1:
		fmt.Println("Filesystem errors were corrected.")
	case 2, 3:
		fmt.Println("Filesystem was corrected (reboot recommended for real system).")
	}

	return nil
}

func getEstimatedInstallSize() int64 {
	sizeStr := os.Getenv("ESTIMATED_INSTALL_SIZE")
	if sizeStr == "" {
		return 0
	}

	size, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		return 0
	}
	return size
}

func getOSReleaseVersion() string {
	return strings.TrimSpace(os.Getenv("OS_RELEASE_VERSION"))
}

func getImageStats() (blockSize, freeBlocks int64, err error) {
	output, err := runCommandOutput("dumpe2fs", "-h", imageFile)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get image stats: %w", err)
	}

	blockSizeRe := regexp.MustCompile(`Block size:\s+(\d+)`)
	freeBlocksRe := regexp.MustCompile(`Free blocks:\s+(\d+)`)

	if match := blockSizeRe.FindStringSubmatch(output); len(match) > 1 {
		blockSize, _ = strconv.ParseInt(match[1], 10, 64)
	}
	if match := freeBlocksRe.FindStringSubmatch(output); len(match) > 1 {
		freeBlocks, _ = strconv.ParseInt(match[1], 10, 64)
	}

	if blockSize == 0 || freeBlocks == 0 {
		return 0, 0, fmt.Errorf("failed to parse dumpe2fs output")
	}

	return blockSize, freeBlocks, nil
}

func resizeImage(estimatedSize int64) error {
	blockSize, freeBlocks, err := getImageStats()
	if err != nil {
		return err
	}

	const mb = 1_000_000

	freeBytes := blockSize * freeBlocks
	fmt.Printf("Current free space: %dMB\n", freeBytes/mb)
	fmt.Printf("Adding space for packages: %dMB\n", estimatedSize/mb)

	resizeMB := (estimatedSize / mb) + 20

	fmt.Printf("Resizing image by %dMB...\n", resizeMB)

	if err := runCommand("truncate", "-s", fmt.Sprintf("+%dM", resizeMB), imageFile); err != nil {
		return fmt.Errorf("failed to extend image: %w", err)
	}

	if err := runE2fsck("after truncate"); err != nil {
		return err
	}

	if err := runCommand("resize2fs", imageFile); err != nil {
		return fmt.Errorf("failed to resize filesystem: %w", err)
	}

	fmt.Println("Image resized successfully")

	blockSize, freeBlocks, err = getImageStats()
	if err != nil {
		return err
	}
	freeBytes = blockSize * freeBlocks
	fmt.Printf("Free space after resize: %dMB\n", freeBytes/mb)

	return nil
}

func mountImage() error {
	fmt.Println("Mounting ext4 image...")
	if err := runCommand("mount", "-o", "loop", imageFile, rootfsMount); err != nil {
		return fmt.Errorf("failed to mount image: %w", err)
	}
	mounted = true
	return nil
}

func unmountImage() error {
	if !mounted {
		return nil
	}

	fmt.Println("Unmounting...")

	if err := runCommand("sync"); err != nil {
		fmt.Fprintln(os.Stderr, color.Warnf("Warning: sync failed: %v", err))
	}

	if err := runCommand("umount", rootfsMount); err != nil {
		return fmt.Errorf("failed to unmount image: %w", err)
	}

	mounted = false
	return nil
}

func cleanup() {
	if mounted {
		fmt.Println("Cleaning up...")
		_ = exec.Command("umount", rootfsMount).Run()
		mounted = false
	}
}

func writeOpkgConfig() error {
	return os.WriteFile(opkgConf, []byte(opkgConfig), 0644)
}

// installPackages installs every .ipk in packagesDir and returns the control
// Package names that installed successfully.
func installPackages() ([]string, error) {
	fmt.Println("Installing packages...")

	packages, err := filepath.Glob(filepath.Join(packagesDir, "*.ipk"))
	if err != nil {
		return nil, fmt.Errorf("failed to list packages: %w", err)
	}
	if len(packages) == 0 {
		fmt.Println("No IPK packages provided; skipping package installation.")
		return nil, nil
	}

	var installedPkgs []string
	failed := 0

	spaceErrorPatterns := []string{"Write failed", "errno=28", "No space left"}

	for _, pkg := range packages {
		pkgName := filepath.Base(pkg)
		fmt.Printf("Installing: %s\n", pkgName)

		opkgArgs := []string{"--conf", opkgConf, "--offline-root", rootfsMount, "--force-reinstall"}
		if verbose {
			opkgArgs = append(opkgArgs, "-V2")
		}
		opkgArgs = append(opkgArgs, "install", pkg)

		if verbose {
			fmt.Println(color.Cmdf("opkg %s", strings.Join(opkgArgs, " ")))
		}

		cmd := exec.Command("opkg", opkgArgs...)
		output, err := cmd.CombinedOutput()
		outputStr := string(output)

		if err != nil {
			// On error always print full output for diagnosis
			fmt.Print(outputStr)
			fmt.Printf("Error: opkg failed for %s (exit code: %v)\n", pkgName, err)
			failed++
			continue
		}

		if verbose {
			fmt.Print(outputStr)
		}

		hasSpaceError := false
		for _, pattern := range spaceErrorPatterns {
			if strings.Contains(outputStr, pattern) {
				hasSpaceError = true
				break
			}
		}

		if hasSpaceError {
			// Print output on space error too
			if !verbose {
				fmt.Print(outputStr)
			}
			fmt.Printf("Error: Insufficient space detected during installation of %s\n", pkgName)
			failed++
			continue
		}

		// Record the control Package name so postinst lookup and opkg status
		// updates match the info files opkg writes, even for variant-suffixed
		// filenames such as mcdd_mfg_armv7_1.7.5.ipk.
		name, _, readErr := fileutil.ReadIPKControl(pkg)
		if readErr != nil || name == "" {
			name, _ = fileutil.ParseIPKFilename(pkgName)
			if name == "" {
				name = strings.TrimSuffix(pkgName, ".ipk")
			}
			if readErr != nil {
				fmt.Fprintln(os.Stderr, color.Warnf(
					"Warning: reading control Package for %s: %v; falling back to filename", pkgName, readErr))
			}
		}
		installedPkgs = append(installedPkgs, name)
	}

	if len(installedPkgs) == 0 {
		return nil, fmt.Errorf("no IPK packages were successfully installed")
	}

	if failed > 0 {
		return installedPkgs, fmt.Errorf("failed to install %d package(s)", failed)
	}

	fmt.Printf("Successfully installed %d package(s)\n", len(installedPkgs))
	return installedPkgs, nil
}

// runDeferredPostinstalls runs the postinstall script for each just-installed
// package only. The opkg info dir holds a .postinst for every package on the
// image, so it must not be globbed wholesale — that would re-run base-image
// postinstalls.
func runDeferredPostinstalls(installedPkgs []string) error {
	infoDir := filepath.Join(rootfsMount, opkgInfoRel)

	var scripts []string
	for _, name := range installedPkgs {
		script := filepath.Join(infoDir, name+".postinst")
		if _, err := os.Stat(script); err == nil {
			scripts = append(scripts, script)
		}
	}
	if len(scripts) == 0 {
		return nil
	}

	fmt.Printf("Found %d deferred postinstall script(s), executing...\n", len(scripts))
	for _, script := range scripts {
		scriptName := filepath.Base(script)
		fmt.Printf("Running deferred postinstall: %s\n", scriptName)

		cmd := exec.Command("sh", script)
		cmd.Env = append(os.Environ(), "OFFLINE_ROOT="+rootfsMount)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintln(os.Stderr, color.Warnf("Warning: postinstall script %s failed: %v", scriptName, err))
		}
	}
	return nil
}

// markPackagesInstalled flips each just-installed package from opkg's offline
// "unpacked" state to "installed". opkg leaves offline-root installs as
// "unpacked" (it defers configuration), and the device's opkg only reports
// "installed" packages in `opkg list-installed`. We run the postinst ourselves
// above, then record the package as installed via opkg's own status writer.
func markPackagesInstalled(installedPkgs []string) error {
	if len(installedPkgs) == 0 {
		return nil
	}

	args := []string{"--conf", opkgConf, "--offline-root", rootfsMount, "flag", "installed"}
	args = append(args, installedPkgs...)

	if verbose {
		fmt.Println(color.Cmdf("opkg %s", strings.Join(args, " ")))
	}

	output, err := runCommandOutput("opkg", args...)
	if err != nil {
		fmt.Print(output)
		return fmt.Errorf("marking %d package(s) installed: %w", len(installedPkgs), err)
	}
	if verbose {
		fmt.Print(output)
	}
	return nil
}

func updateOSReleaseVersion(version string) error {
	osReleasePath := filepath.Join(rootfsMount, osReleaseRel)
	data, err := os.ReadFile(osReleasePath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", osReleasePath, err)
	}

	info, err := os.Stat(osReleasePath)
	if err != nil {
		return fmt.Errorf("stat %s: %w", osReleasePath, err)
	}

	updated := rewriteOSReleaseVersion(data, version)
	if err := os.WriteFile(osReleasePath, updated, info.Mode()); err != nil {
		return fmt.Errorf("writing %s: %w", osReleasePath, err)
	}

	fmt.Printf("Updated %s VERSION to %s\n", osReleasePath, version)
	return nil
}

func rewriteOSReleaseVersion(data []byte, version string) []byte {
	line := []byte(fmt.Sprintf("VERSION=%q", version))
	if osReleaseVersionRe.Match(data) {
		return osReleaseVersionRe.ReplaceAll(data, line)
	}

	updated := bytes.Clone(data)
	if len(updated) > 0 && updated[len(updated)-1] != '\n' {
		updated = append(updated, '\n')
	}
	updated = append(updated, line...)
	updated = append(updated, '\n')
	return updated
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd.Run()
}

func runCommandOutput(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

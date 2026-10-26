// This file implements the "flash" command for firmware image flashing.
// It handles single images, zip archives, and directories, with automatic partition detection
// and safe multi-partition flashing with progress tracking.
package gofastboot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/urfave/cli/v2"
	"proto-distro/tools/flashimage"
	"proto-distro/tools/go_fastboot/internal/device"
	"proto-distro/tools/go_fastboot/internal/fastboot"
	"proto-distro/tools/go_fastboot/internal/ssh"
	"proto-distro/tools/go_fastboot/internal/ui"
)

const (
	defaultFlashTimeout       = 3 * time.Minute
	rebootWaitDelay           = 5 * time.Second
	deviceOnlineTimeout       = 2 * time.Minute
	deviceOnlineCheckInterval = 3 * time.Second
)

func makeFlashCommand() *cli.Command {
	return &cli.Command{
		Name:      "flash",
		Usage:     "Flash firmware to device",
		ArgsUsage: "<image>",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "partition",
				Aliases:  []string{"p"},
				Usage:    "Partition to flash (e.g., rootfs, bootfs, boot). If not specified, flashes all detected partitions from zip",
				Required: false,
			},
			&cli.DurationFlag{
				Name:    "timeout",
				Aliases: []string{"t"},
				Usage:   "Flash timeout (e.g., 5m, 10m, 30m)",
				Value:   defaultFlashTimeout,
			},
			&cli.BoolFlag{
				Name:  "no-reboot",
				Usage: "Don't reboot after flashing (default: auto-reboot when flashing all partitions, no reboot when partition specified)",
			},
			&cli.BoolFlag{
				Name:  "verity",
				Usage: "Require AVB body and tail images for every selected AVB partition",
			},
		},
		Action: handleFlash,
	}
}

func handleFlash(c *cli.Context) error {
	return Flash(c.Context, FlashRequest{
		Target:    c.String("target"),
		Username:  c.String("username"),
		Password:  c.String("password"),
		ImagePath: c.Args().First(),
		Partition: c.String("partition"),
		Timeout:   c.Duration("timeout"),
		NoReboot:  c.Bool("no-reboot"),
		Verity:    c.Bool("verity"),
	})
}

// Flash flashes a firmware image or image archive to a fastboot-mode device.
func Flash(ctx context.Context, req FlashRequest) error {
	profile := flashimage.C3()
	if err := profile.Validate(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if req.Timeout == 0 {
		req.Timeout = defaultFlashTimeout
	}

	dev, err := resolveRequestTarget(ctx, req.Target, req.Username, req.Password, true)
	if err != nil {
		return err
	}

	imagePath := req.ImagePath
	if imagePath == "" {
		return fmt.Errorf("image path required. Usage: go_fastboot flash <image>")
	}

	partition := req.Partition
	timeout := req.Timeout
	noReboot := req.NoReboot

	// Track if user specified a partition - used to determine auto-reboot behavior
	partitionSpecified := partition != ""

	spinner := ui.NewSpinner()
	status := ui.NewStatusLine()

	// Validate image file exists
	absPath, err := filepath.Abs(imagePath)
	if err != nil {
		return fmt.Errorf("invalid image path: %w", err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("cannot access image: %w", err)
	}

	var tempDir string
	var extractedImages map[string]string // map of partition -> image path

	if info.IsDir() {
		// Directory input - scan for image files recursively (pre-extracted package)
		status.Info(fmt.Sprintf("Image directory: %s", filepath.Base(absPath)))

		extractedImages, err = scanDirImages(absPath)
		if err != nil {
			return fmt.Errorf("failed to scan directory: %w", err)
		}

		if len(extractedImages) == 0 {
			return fmt.Errorf("no flashable images found in directory")
		}

		extractedImages = filterPartitions(profile, extractedImages, partition, status)
		if partition != "" {
			if _, ok := extractedImages[partition]; !ok {
				return fmt.Errorf("partition %s not found in directory. Available: %v", partition, getKeys(extractedImages))
			}
		}
		if len(extractedImages) == 0 {
			return fmt.Errorf("no default flashable images found in directory (use --partition to flash explicitly)")
		}

		for part, path := range extractedImages {
			status.Success(fmt.Sprintf("Found: %s -> %s", filepath.Base(path), part))
		}
	} else if filepath.Ext(absPath) == ".zip" {
		status.Info(fmt.Sprintf("Image: %s (%.2f MB)", filepath.Base(absPath), float64(info.Size())/fastboot.MB))
		status.Info("Extracting zip archive...")

		tempDir, extractedImages, err = extractZipImages(absPath)
		if err != nil {
			return fmt.Errorf("failed to extract zip: %w", err)
		}

		// Clean up temp directory when done
		defer func() {
			if tempDir != "" {
				os.RemoveAll(tempDir)
			}
		}()

		if len(extractedImages) == 0 {
			return fmt.Errorf("no flashable images found in zip")
		}

		extractedImages = filterPartitions(profile, extractedImages, partition, status)
		if partition != "" {
			if _, ok := extractedImages[partition]; !ok {
				return fmt.Errorf("partition %s not found in zip. Available: %v", partition, getKeys(extractedImages))
			}
		}
		if len(extractedImages) == 0 {
			return fmt.Errorf("no default flashable images found in zip (use --partition to flash explicitly)")
		}

		// Show what we will flash
		for part, path := range extractedImages {
			status.Success(fmt.Sprintf("Extracted: %s -> %s", filepath.Base(path), part))
		}
	} else {
		if strings.HasSuffix(strings.ToLower(absPath), ".avb.body") || strings.HasSuffix(strings.ToLower(absPath), ".avb.tail") {
			return fmt.Errorf("AVB companions cannot be flashed directly; select the raw image")
		}
		status.Info(fmt.Sprintf("Image: %s (%.2f MB)", filepath.Base(absPath), float64(info.Size())/fastboot.MB))

		// Single image file - partition must be specified
		if partition == "" {
			partition = detectPartition(filepath.Base(absPath))
			if partition == "" {
				return fmt.Errorf("cannot auto-detect partition from filename. Use --partition flag")
			}
			status.Info(fmt.Sprintf("Partition: %s (detected from filename)", partition))
		} else {
			status.Info(fmt.Sprintf("Partition: %s", partition))
		}
		extractedImages = map[string]string{partition: absPath}
	}

	// Check device mode
	spinner.Start("Checking device mode...")
	mode := probeDeviceMode(ctx, dev.IP)
	if err := ctx.Err(); err != nil {
		spinner.Error("Operation canceled")
		return err
	}

	if mode == device.ModeLinux {
		spinner.Warning("Device is in Linux mode")
		status.Info("Run 'go_fastboot enter' first to enter fastboot mode")
		return fmt.Errorf("device must be in fastboot mode for flashing")
	}

	if mode != device.ModeFastboot {
		spinner.Error("Device not reachable")
		return fmt.Errorf("device not reachable")
	}

	spinner.Success("Device is in fastboot mode")

	// Create fastboot client
	fbClient := fastboot.NewNativeClient(dev.IP)

	// Verify fastboot is responsive
	spinner.Start("Verifying fastboot server...")
	if err := fbClient.CheckResponsive(ctx); err != nil {
		spinner.Error("Fastboot server not responding")
		return fmt.Errorf("fastboot server not responding: %w", err)
	}
	spinner.Success("Fastboot server ready")

	// Prepare every selected AVB image before any erase or flash, including earlier-ranked data.
	sortedPartitions := sortPartitions(profile, extractedImages)
	preparedImages, cleanupPrepared, err := prepareFlashImages(ctx, profile, extractedImages, sortedPartitions, fbClient.GetVar, status, req.Verity)
	defer cleanupPrepared()
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(sortedPartitions))
	for _, partition := range sortedPartitions {
		path := extractedImages[partition]
		if prepared := preparedImages[partition]; prepared != "" {
			path = prepared
		}
		paths = append(paths, path)
	}
	if err := fbClient.ValidateImages(ctx, paths); err != nil {
		return fmt.Errorf("validating flash images: %w", err)
	}

	// Erase u-boot-env partition when flashing all partitions (not when a specific partition was requested)
	// This ensures clean boot state and prevents stale U-Boot variables from interfering
	erasedUbootEnv := false
	environment := ""
	for i := range profile.PartitionCount() {
		row, _ := profile.Partition(i)
		if row.Environment {
			environment = row.Name
			break
		}
	}
	if !partitionSpecified && len(extractedImages) > 1 {
		fmt.Println()
		// Total steps = 1 (erase) + len(images) (flash) + (1 if reboot, 0 if --no-reboot)
		totalSteps := len(extractedImages) + 1
		if !noReboot {
			totalSteps++
		}
		status.Step(1, totalSteps, "Erasing u-boot-env partition...")
		spinner.Start("Erasing u-boot-env...")

		// Mark step as attempted regardless of success for consistent progress tracking
		erasedUbootEnv = true
		if err := fbClient.Erase(ctx, environment); err != nil {
			spinner.Error("Failed to erase u-boot-env")
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			status.Warning("Continuing without erasing u-boot-env - device may have stale boot configuration")
		} else {
			spinner.Success("u-boot-env erased")
		}
		fmt.Println()
	}

	// Flash the image(s) in deterministic safe order
	fmt.Println()
	totalPartitions := len(extractedImages)
	currentStep := 1
	overallStartTime := time.Now()

	// Adjust step counter if we erased u-boot-env
	if erasedUbootEnv {
		currentStep = 2
	}

	// Calculate total steps for progress display
	totalSteps := totalPartitions
	if erasedUbootEnv {
		totalSteps++ // Account for u-boot-env erase step
	}
	if !noReboot && !partitionSpecified {
		totalSteps++ // Account for reboot step
	}

	for _, detectedPartition := range sortedPartitions {
		if err := ctx.Err(); err != nil {
			spinner.Error("Operation canceled")
			return err
		}
		imagePath := extractedImages[detectedPartition]
		if prepared := preparedImages[detectedPartition]; prepared != "" {
			imagePath = prepared
		}
		// Map detected partition name to actual fastboot partition name
		fastbootPartition := detectedPartition
		if rule, ok := profile.FastbootRule(detectedPartition); ok {
			fastbootPartition = rule.Destination()
		}

		status.Step(currentStep, totalSteps, fmt.Sprintf("Flashing %s to %s...", filepath.Base(imagePath), fastbootPartition))

		// Track progress with a spinner initially, then switch to progress bar when we get percentage updates
		var progressBar *ui.ProgressBar
		spinnerMsg := fmt.Sprintf("Flashing image (timeout: %s)...", timeout)
		spinner.Start(spinnerMsg)

		progressCallback := func(msg string) {
			// Messages starting with \r are inline progress updates
			if strings.HasPrefix(msg, "\r") {
				msg = msg[1:] // Remove \r prefix

				// Extract percentage if present in format "Progress (X%)"
				if strings.Contains(msg, "Progress (") && strings.Contains(msg, "%)") {
					var percent int
					if _, err := fmt.Sscanf(msg, "Progress (%d%%)", &percent); err == nil {
						// Create progress bar on first percentage update
						if progressBar == nil {
							spinner.Stop()
							progressBar = ui.NewProgressBar(100)
						}
						progressBar.Set(percent)
						// Extract the step description (everything after " - ")
						if idx := strings.Index(msg, " - "); idx > 0 {
							progressBar.SetMessage(msg[idx+3:])
						}
					}
				} else if progressBar != nil {
					// Non-percentage inline update (e.g., "Sending... X%")
					progressBar.SetMessage(msg)
				} else {
					// Still in spinner mode, update spinner
					spinner.Update(msg)
				}
			} else {
				// Non-inline messages are setup/informational - just update the spinner/status
				if progressBar == nil {
					spinner.Update(msg)
				}
			}
		}

		result, err := fbClient.Flash(ctx, fastbootPartition, imagePath, timeout, progressCallback)

		if err != nil {
			if progressBar != nil {
				progressBar.Error(fmt.Sprintf("Flash failed: %v", err))
			} else {
				spinner.Error(fmt.Sprintf("Flash failed: %v", err))
			}
			// Check if this was a timeout error using proper error inspection
			if errors.Is(err, context.DeadlineExceeded) {
				fmt.Println()
				status.Warning(fmt.Sprintf("Tip: Use --timeout flag to increase timeout (e.g., --timeout 10m)"))
			}
			return err
		}

		if progressBar != nil {
			progressBar.Complete(fmt.Sprintf("Flash complete (%.1fs)", result.Duration.Seconds()))
		} else {
			spinner.Success(fmt.Sprintf("Flash complete (%.1fs)", result.Duration.Seconds()))
		}
		fmt.Println()
		currentStep++
	}

	// Reboot by default only when flashing ALL partitions from a zip (no specific partition requested)
	// If user specified which partition(s) to flash, they're doing targeted updates and shouldn't auto-reboot
	shouldReboot := !noReboot && !partitionSpecified

	if shouldReboot {
		status.Step(totalSteps, totalSteps, "Rebooting device...")

		spinner.Start("Rebooting...")
		if err := fbClient.Reboot(ctx); err != nil {
			spinner.Error("Reboot failed")
			return fmt.Errorf("reboot failed: %w", err)
		}

		// Wait for device to come back
		spinner.Update("Waiting for device...")

		// Device might get new IP after reboot
		if err := sleepContext(ctx, rebootWaitDelay); err != nil {
			spinner.Error("Operation canceled")
			return err
		}

		var cache *device.Cache
		if strings.TrimSpace(req.Target) == "" {
			cache, err = device.NewCache()
			if err != nil {
				spinner.Warning("Device tracking unavailable")
				status.Success(fmt.Sprintf("Flash completed successfully in %.1fs", time.Since(overallStartTime).Seconds()))
				status.Warning("Could not track device after reboot. It may have a new IP address.")
				return nil
			}
		}
		tracker := device.NewTracker(dev, cache)

		// Poll for device to come back online and verify SSH connectivity
		deadline := time.Now().Add(deviceOnlineTimeout)
		authFailureCount := 0
		const maxAuthFailures = 3 // Stop after this many consecutive auth failures

		for time.Now().Before(deadline) {
			if err := ctx.Err(); err != nil {
				spinner.Error("Operation canceled")
				return err
			}
			result := tracker.Relocate(ctx)
			if result.Found {
				// Device is reachable on SSH port, now verify we can actually SSH in
				// Try multiple credential sets since flashing may change the account
				// (e.g., testrack has admin, mfg has root only)
				spinner.Update("Verifying SSH connectivity...")

				// Build credential list with fallbacks
				credentialList := device.BuildCredentialList(device.Credential{
					Username: dev.Username,
					Password: dev.Password,
				})

				sshVerified := false
				allAuthFailed := true // Track if ALL failures were auth failures
				var triedUsers []string
				var usedFallback bool
				var successCred device.Credential

				for i, cred := range credentialList {
					if err := ctx.Err(); err != nil {
						spinner.Error("Operation canceled")
						return err
					}
					triedUsers = append(triedUsers, cred.Username)
					sshClient, err := ssh.NewClient(ctx, ssh.Config{
						Host:     result.NewIP,
						Username: cred.Username,
						Password: cred.Password,
						Timeout:  sshConnectionTimeout,
					})
					if err != nil {
						if ctx.Err() != nil {
							spinner.Error("Operation canceled")
							return ctx.Err()
						}
						// Check if this is an auth failure vs connection failure
						if !errors.Is(err, ssh.ErrAuthFailed) {
							allAuthFailed = false
						}
						continue
					}

					// Try to get device info to confirm device is fully responsive
					_, err = sshClient.GetHostname(ctx)
					sshClient.Close()
					if ctx.Err() != nil {
						spinner.Error("Operation canceled")
						return ctx.Err()
					}

					if err == nil {
						sshVerified = true
						usedFallback = i > 0 // Index 0 is the primary credential
						successCred = cred
						break
					}
				}

				if sshVerified {
					// Device is fully online and responsive
					spinner.Success(fmt.Sprintf("Device online at %s", result.NewIP))

					fmt.Println()
					status.Success(fmt.Sprintf("Flash completed successfully in %.1fs", time.Since(overallStartTime).Seconds()))
					if usedFallback {
						status.Warning(fmt.Sprintf("Primary credentials failed, used fallback user: %s", successCred.Username))
					}
					return nil
				}

				// If all credential sets failed with auth errors, the device is online
				// but we just don't have the right credentials
				if allAuthFailed {
					authFailureCount++
					if authFailureCount >= maxAuthFailures {
						spinner.Success(fmt.Sprintf("Device online at %s", result.NewIP))

						fmt.Println()
						status.Success(fmt.Sprintf("Flash completed successfully in %.1fs", time.Since(overallStartTime).Seconds()))
						status.Warning(fmt.Sprintf("SSH authentication failed (tried users: %s).", strings.Join(triedUsers, ", ")))
						status.Info("The flashed image may use different credentials. Try:")
						status.Info(fmt.Sprintf("  go_fastboot -u <username> -p <password> -t %s status", result.NewIP))
						return nil
					}
				} else {
					// Reset counter if we got non-auth failures (device still booting)
					authFailureCount = 0
				}

				if err := sleepContext(ctx, deviceOnlineCheckInterval); err != nil {
					spinner.Error("Operation canceled")
					return err
				}
				continue
			}
			if err := sleepContext(ctx, deviceOnlineCheckInterval); err != nil {
				spinner.Error("Operation canceled")
				return err
			}
		}

		spinner.Warning("Device not found after reboot")
		status.Warning("Flash completed but device not reachable. It may have a new IP address.")
	} else {
		fmt.Println()
		status.Success(fmt.Sprintf("Flash completed successfully in %.1fs", time.Since(overallStartTime).Seconds()))

		// For targeted partition flashes, remind user they may need to reboot
		if partitionSpecified {
			status.Info("Device was not rebooted. Run 'go_fastboot reboot' or 'go_fastboot exit' when ready.")
		}
	}

	return nil
}

// prepareFlashImages leaves discovery paths intact and owns only prepared sparse files.
func prepareFlashImages(ctx context.Context, profile flashimage.Profile, images map[string]string, order []string, getVar func(context.Context, string) (string, error), status *ui.StatusLine, verity bool) (prepared map[string]string, cleanup func(), err error) {
	prepared = make(map[string]string)
	cleanup = func() {
		for _, path := range prepared {
			os.Remove(path)
		}
	}
	defer func() {
		if err != nil {
			cleanup()
		}
	}()
	for _, key := range order {
		if err := ctx.Err(); err != nil {
			return prepared, cleanup, err
		}
		destination := key
		if rule, ok := profile.FastbootRule(key); ok {
			destination = rule.Destination()
		}
		row, known := profile.Lookup(destination)
		if !known || !row.AVB {
			continue
		}
		capacity, fixed := profile.FixedCapacity(destination)
		if !fixed {
			return prepared, cleanup, fmt.Errorf("AVB %s requires a known fixed capacity", destination)
		}
		path := images[key]
		output, missing, err := fastboot.PrepareAVBImage(ctx, path, destination, capacity, getVar)
		if err != nil {
			return prepared, cleanup, fmt.Errorf("prepare AVB %s: %w", destination, err)
		}
		if output != "" {
			prepared[key] = output
		}
		if len(missing) != 0 {
			names := make([]string, len(missing))
			for i, path := range missing {
				names[i] = filepath.Base(path)
			}
			if verity {
				return prepared, cleanup, fmt.Errorf("AVB metadata required for %s: missing %s", destination, strings.Join(names, ", "))
			}
			status.Warning(fmt.Sprintf("AVB metadata missing for %s (%s); .avb.body and .avb.tail are required together. Flashing without AVB metadata; verified boot may fail.", filepath.Base(path), strings.Join(names, ", ")))
		}
	}
	return prepared, cleanup, nil
}

// detectPartition tries to detect the partition from the image filename
// Looks for explicit partition markers like "rootfs.ext4" or "-bootfs-"
func detectPartition(filename string) string {
	lowerName := strings.ToLower(filename)

	// Remove extension for cleaner matching
	nameWithoutExt := lowerName
	if idx := strings.LastIndex(lowerName, "."); idx > 0 {
		nameWithoutExt = lowerName[:idx]
	}

	// Check for TFA (Trusted Firmware-A) eMMC patterns
	// Only matches eMMC variant: "tf-a-xxx-emmc.stm32", "tf-a-xxx-emmc_Signed.stm32"
	// Rejects USB variant to prevent bricking (USB TFA on eMMC device = brick)
	if matched, _ := regexp.MatchString(`(^|[/._-])tf-?a[^/]*-emmc`, lowerName); matched {
		return "tfa"
	}

	// Check for FIP (Firmware Image Package) patterns
	// Matches: "fip-xxx.bin", "fip.bin", etc.
	if matched, _ := regexp.MatchString(`(^|[/._-])fip([/._-]|$)`, nameWithoutExt); matched {
		return "fip"
	}

	// Check for explicit partition names using regex with word boundaries
	// Matches patterns like: .partition., -partition-, .partition, -partition, or partition at end
	// Priority order matters: more specific partitions before generic ones
	explicitPartitions := []string{"rootfs", "bootfs", "datafs", "userfs", "boot", "recovery"}

	for _, partition := range explicitPartitions {
		// Regex pattern: (start|separator)partition(separator|end)
		pattern := fmt.Sprintf(`(^|[/._-])%s([/._-]|$)`, regexp.QuoteMeta(partition))
		if matched, _ := regexp.MatchString(pattern, nameWithoutExt); matched {
			return partition
		}
	}

	// Ambiguous fallback keywords require an explicit partition.
	boot := strings.Contains(lowerName, "kernel") || strings.Contains(lowerName, "zimage")
	dtb := strings.Contains(lowerName, "dtb") || strings.Contains(lowerName, "devicetree")
	if boot && !dtb {
		return "boot"
	}
	if dtb && !boot {
		return "dtb"
	}

	return ""
}

// validImageExtensions defines file extensions that are valid flashable images
var validImageExtensions = []string{
	".ext4",   // ext4 filesystem images
	".img",    // generic image files
	".bin",    // binary files (bootloaders, firmware)
	".raw",    // raw disk images
	".sparse", // sparse images
	".stm32",  // STM32 firmware images (TFA)
}

// isValidImageFile checks if a filename has a valid flashable image extension
func isValidImageFile(filename string) bool {
	lowerName := strings.ToLower(filename)
	for _, ext := range validImageExtensions {
		if strings.HasSuffix(lowerName, ext) {
			return true
		}
	}
	return false
}

// filterPartitions filters the image map based on the user's partition selection.
// If a partition is specified, only that partition is kept. Otherwise, only default partitions are kept.
func filterPartitions(profile flashimage.Profile, images map[string]string, partition string, status *ui.StatusLine) map[string]string {
	if partition != "" {
		if imgPath, ok := images[partition]; ok {
			return map[string]string{partition: imgPath}
		}
		// Return unchanged - caller checks for the missing partition and reports the error
		return images
	}

	// Filter to only default partitions (skip uboot, etc.)
	filtered := make(map[string]string)
	var skipped []string
	for part, path := range images {
		if rule, ok := profile.FastbootRule(part); ok && rule.Default {
			filtered[part] = path
		} else {
			skipped = append(skipped, part)
		}
	}
	if len(skipped) > 0 {
		status.Warning(fmt.Sprintf("Skipping partitions: %v (use --partition to flash explicitly)", skipped))
	}
	return filtered
}

// scanDirImages recursively scans a directory for flashable image files and returns them mapped to partitions
func scanDirImages(dirPath string) (map[string]string, error) {
	images := make(map[string]string)

	err := filepath.WalkDir(dirPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		filename := filepath.Base(path)
		if !isValidImageFile(filename) {
			return nil
		}

		partition := detectPartition(filename)
		if partition != "" {
			if shouldUseImageForPartition(partition, path, images[partition]) {
				images[partition] = path
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking directory: %w", err)
	}

	return images, nil
}

// extractZipImages extracts a zip file and returns all detected image files mapped to partitions
func extractZipImages(zipPath string) (tempDir string, images map[string]string, err error) {
	// Create temp directory
	tempDir, err = os.MkdirTemp("", "go_fastboot_*")
	if err != nil {
		return "", nil, fmt.Errorf("creating temp directory: %w", err)
	}

	images = make(map[string]string)
	err = flashimage.ExtractZIP(zipPath, tempDir, func(path string) {
		filename := filepath.Base(path)
		if !isValidImageFile(filename) {
			return
		}
		partition := detectPartition(filename)
		if partition != "" && shouldUseImageForPartition(partition, path, images[partition]) {
			images[partition] = path
		}
	})
	if err != nil {
		os.RemoveAll(tempDir)
		return "", nil, fmt.Errorf("extracting ZIP: %w", err)
	}

	if len(images) == 0 {
		os.RemoveAll(tempDir)
		return "", nil, fmt.Errorf("no flashable images found in zip archive")
	}

	return tempDir, images, nil
}

func shouldUseImageForPartition(partition string, candidatePath string, existingPath string) bool {
	if existingPath == "" {
		return true
	}
	if partition != "tfa" {
		return false
	}
	return isSignedSTM32(candidatePath) && !isSignedSTM32(existingPath)
}

func isSignedSTM32(path string) bool {
	name := strings.ToLower(filepath.Base(path))
	return strings.HasSuffix(name, "_signed.stm32")
}

// getKeys returns the keys of a map as a slice
func getKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// sortPartitions returns partition names sorted by safe flashing order
func sortPartitions(profile flashimage.Profile, images map[string]string) []string {
	partitions := getKeys(images)
	sort.Slice(partitions, func(i, j int) bool {
		ruleI, okI := profile.FastbootRule(partitions[i])
		ruleJ, okJ := profile.FastbootRule(partitions[j])

		// Known partitions come before unknown ones
		if okI && !okJ {
			return true
		}
		if !okI && okJ {
			return false
		}
		// Both known: sort by defined order
		if okI && okJ {
			return ruleI.Rank < ruleJ.Rank
		}
		// Both unknown: sort alphabetically for consistency
		return partitions[i] < partitions[j]
	})
	return partitions
}

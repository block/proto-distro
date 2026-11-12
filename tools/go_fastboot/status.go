// This file implements the "status" command for device information display.
// It shows device mode, IP, version, and service status for both Linux and fastboot modes.
package gofastboot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/urfave/cli/v2"
	"proto-distro/tools/go_fastboot/internal/device"
	"proto-distro/tools/go_fastboot/internal/fastboot"
	"proto-distro/tools/go_fastboot/internal/ssh"
	"proto-distro/tools/go_fastboot/internal/ui"
)

const (
	statusSSHTimeout = 5 * time.Second
)

func makeStatusCommand() *cli.Command {
	return &cli.Command{
		Name:   "status",
		Usage:  "Show device status",
		Action: handleStatus,
	}
}

func handleStatus(c *cli.Context) error {
	dev, err := resolveRequestTarget(c.Context, c.String("target"), c.String("username"), c.String("password"), false)
	if err != nil {
		return err
	}

	spinner := ui.NewSpinner()
	status := ui.NewStatusLine()

	spinner.Start("Checking device status...")

	// First check what mode the device is in
	mode := probeDeviceMode(c.Context, dev.IP)
	if err := c.Context.Err(); err != nil {
		spinner.Error("Operation canceled")
		return err
	}
	dev.Mode = mode

	switch mode {
	case device.ModeLinux:
		spinner.Success("Device is in Linux mode")
		return showLinuxStatus(c.Context, dev, status)

	case device.ModeFastboot:
		spinner.Success("Device is in Fastboot mode")
		return showFastbootStatus(c.Context, dev, status)

	default:
		spinner.Error("Device mode unknown or unreachable")
		status.Info("Device may be rebooting or offline. Try again in a few seconds.")
		return fmt.Errorf("cannot determine device mode")
	}
}

func showLinuxStatus(ctx context.Context, dev *device.Device, status *ui.StatusLine) error {
	sshClient, err := ssh.NewClientForDevice(ctx, dev, statusSSHTimeout)
	if err != nil {
		// Check if this looks like SSH server not ready yet (common during boot)
		errStr := err.Error()
		if strings.Contains(errStr, "connection refused") ||
			strings.Contains(errStr, "no route to host") ||
			strings.Contains(errStr, "network is unreachable") {
			status.Warning("SSH not ready yet")
			fmt.Println()
			fmt.Printf("  %s Device may still be booting. Port 22 is not accepting connections.\n", ui.StyleMuted.Render("ℹ"))
			fmt.Println()
			fmt.Println("  Try again in a few seconds, or wait for the device to fully boot.")
			fmt.Println()
			return fmt.Errorf("SSH server not ready")
		}

		// Provide helpful error message for authentication failures
		if errors.Is(err, ssh.ErrAuthFailed) {
			ui.PrintAuthHint(dev.IP, "status")
			return err
		}
		return fmt.Errorf("SSH connection failed: %w", err)
	}
	defer sshClient.Close()

	fmt.Println()
	fmt.Printf("  %-14s %s\n", "Mode:", ui.StyleMode("LINUX"))
	fmt.Printf("  %-14s %s\n", "IP:", dev.IP)

	// Get hostname
	if hostname, err := sshClient.GetHostname(ctx); err == nil {
		fmt.Printf("  %-14s %s\n", "Hostname:", hostname)
	}

	// Get OS release (version, variant, machine)
	if release, err := sshClient.GetOSRelease(ctx); err == nil {
		fmt.Printf("  %-14s %s\n", "Version:", release.Version)
		if release.Variant != "" {
			fmt.Printf("  %-14s %s\n", "Variant:", release.Variant)
		}
		if release.Machine != "" {
			fmt.Printf("  %-14s %s\n", "Machine:", release.Machine)
		}
	}

	// Get MAC
	if mac, err := sshClient.GetMAC(ctx); err == nil {
		fmt.Printf("  %-14s %s\n", "MAC:", mac)
	}

	return ctx.Err()
}

func showFastbootStatus(ctx context.Context, dev *device.Device, status *ui.StatusLine) error {
	client := fastboot.NewNativeClient(dev.IP)

	fmt.Println()
	fmt.Printf("  %-14s %s\n", "Mode:", ui.StyleMode("FASTBOOT"))
	fmt.Printf("  %-14s %s\n", "IP:", dev.IP)

	// Get product
	if product, err := client.GetProduct(ctx); err == nil {
		fmt.Printf("  %-14s %s\n", "Product:", product)
	}

	// Get version
	if version, err := client.GetVersion(ctx); err == nil {
		fmt.Printf("  %-14s %s\n", "Bootloader:", version)
	}

	// Get serial
	if serial, err := client.GetSerialNo(ctx); err == nil {
		fmt.Printf("  %-14s %s\n", "Serial:", serial)
	}

	return nil
}

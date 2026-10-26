// This file implements the "target" command for managing saved device targets.
// It provides set/show operations with device information gathering and IP probing.
package gofastboot

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/urfave/cli/v2"
	"proto-distro/tools/go_fastboot/internal/device"
	"proto-distro/tools/go_fastboot/internal/ssh"
	"proto-distro/tools/go_fastboot/internal/ui"
)

const (
	targetSSHTimeout  = 5 * time.Second
	probeTimeout      = 2 * time.Second
	defaultSubnetMask = "/24"
)

func makeTargetCommand() *cli.Command {
	return &cli.Command{
		Name:      "target",
		Usage:     "Set or show the current target device",
		ArgsUsage: "[ip|hostname]",
		Subcommands: []*cli.Command{
			{
				Name:      "username",
				Usage:     "Set the SSH username for the target (prompts if not provided)",
				ArgsUsage: "[username]",
				Action:    handleTargetUsername,
			},
		},
		Action: handleTarget,
	}
}

func handleTarget(c *cli.Context) error {
	// If argument provided, set target; otherwise show current target
	if c.NArg() >= 1 {
		return handleTargetSet(c)
	}
	return handleTargetShow(c)
}

func handleTargetSet(c *cli.Context) error {
	if c.NArg() < 1 {
		return fmt.Errorf("target required: IP, hostname, or MAC address")
	}

	targetStr := c.Args().First()
	username := resolveCredential(c.String("username"), "", defaultUsername)
	password := resolveCredential(c.String("password"), "", defaultPassword)

	status := ui.NewStatusLine()
	spinner := ui.NewSpinner()

	// Parse target
	target, err := device.ParseTarget(targetStr)
	if err != nil {
		return fmt.Errorf("invalid target: %w", err)
	}

	// Create device from target
	dev := &device.Device{
		Username: username,
		Password: password,
	}

	// Resolve target to IP
	spinner.Start("Resolving target...")

	if target.IP != "" {
		dev.IP = target.IP
	} else if target.Hostname != "" {
		dev.Hostname = target.Hostname
		ip, err := device.ResolveHostname(c.Context, target.Hostname)
		if err != nil {
			spinner.Error(fmt.Sprintf("Cannot resolve hostname: %s", target.Hostname))
			return err
		}
		dev.IP = ip
	} else if target.MAC != "" {
		// Need subnet for MAC-based discovery
		spinner.Error("MAC-based target requires existing target with subnet. Use IP or hostname first.")
		return fmt.Errorf("MAC-based targeting requires subnet information")
	}

	// Check reachability (accept both Linux and Fastboot mode)
	spinner.Update("Checking reachability...")
	mode := probeDeviceMode(c.Context, dev.IP)
	if err := c.Context.Err(); err != nil {
		spinner.Error("Operation canceled")
		return err
	}
	if mode == device.ModeUnknown {
		spinner.Error(fmt.Sprintf("Device not reachable at %s", dev.IP))
		return fmt.Errorf("device not reachable")
	}
	modeStr := "(" + string(mode) + " mode)"
	spinner.Success(fmt.Sprintf("Device reachable at %s %s", dev.IP, modeStr))
	dev.Mode = mode

	// Try to get additional info via SSH
	spinner.Start("Gathering device information...")

	sshClient, err := ssh.NewClient(c.Context, ssh.Config{
		Host:     dev.IP,
		Username: username,
		Password: password,
		Timeout:  targetSSHTimeout,
	})
	if err != nil {
		if errors.Is(err, ssh.ErrAuthFailed) {
			spinner.Warning(fmt.Sprintf("SSH authentication failed for user '%s', saving with limited info", username))
			status.Info(fmt.Sprintf("Tip: Use -u <username> -p <password> if this device uses different credentials"))
		} else {
			spinner.Warning("SSH connection failed, saving with limited info")
		}
	} else {
		defer sshClient.Close()

		// Get hostname
		if hostname, err := sshClient.GetHostname(c.Context); err == nil && hostname != "" {
			dev.Hostname = hostname
		}

		// Get MAC address
		if mac, err := sshClient.GetMAC(c.Context); err == nil && mac != "" {
			dev.MAC = mac
		}

		spinner.Success("Device information gathered")
	}
	if err := c.Context.Err(); err != nil {
		spinner.Error("Operation canceled")
		return err
	}

	// Try to get MAC from ARP if we don't have it
	if dev.MAC == "" {
		if mac, err := device.GetMACFromARP(c.Context, dev.IP); err == nil {
			dev.MAC = mac
		}
	}

	// Infer subnet from IP
	dev.Subnet = inferSubnet(dev.IP)
	dev.LastSeen = time.Now()
	// Mode was already set during reachability check

	// Save to cache
	cache, err := device.NewCache()
	if err != nil {
		return fmt.Errorf("creating cache: %w", err)
	}

	if err := cache.SaveTarget(dev); err != nil {
		return fmt.Errorf("saving target: %w", err)
	}

	// Print summary
	fmt.Println()
	status.Success("Target set successfully")
	printDeviceInfo(dev, true)

	return nil
}

func handleTargetShow(c *cli.Context) error {
	username := c.String("username")
	password := c.String("password")

	status := ui.NewStatusLine()
	spinner := ui.NewSpinner()

	cache, err := device.NewCache()
	if err != nil {
		return fmt.Errorf("creating cache: %w", err)
	}

	dev, err := cache.LoadTarget()
	if err != nil {
		return fmt.Errorf("loading target: %w", err)
	}
	if dev == nil {
		status.Warning("No target set. Use 'go_fastboot target <ip>' to set one.")
		return nil
	}

	dev.Username = resolveCredential(username, dev.Username, defaultUsername)
	dev.Password = resolveCredential(password, dev.Password, defaultPassword)

	// Check if device is reachable and relocate if needed
	spinner.Start(fmt.Sprintf("Checking %s...", dev.IP))

	tracker := device.NewTracker(dev, cache)
	result := tracker.Relocate(c.Context)
	if err := c.Context.Err(); err != nil {
		spinner.Error("Operation canceled")
		return err
	}

	if result.Found {
		if result.Method == "direct" {
			spinner.Success(fmt.Sprintf("Device online at %s", result.NewIP))
		} else {
			spinner.Success(fmt.Sprintf("Device found at %s (was %s, via %s)", result.NewIP, result.OldIP, result.Method))
		}

		// Update mode by probing
		dev.Mode = probeDeviceMode(c.Context, dev.IP)

		// Update last seen
		dev.LastSeen = time.Now()
		if err := cache.SaveTarget(dev); err != nil {
			status.Warning(fmt.Sprintf("Failed to save updated target state: %v", err))
		}
	} else {
		spinner.Error(fmt.Sprintf("Device unreachable (last seen: %s)", ui.FormatTime(dev.LastSeen)))
		fmt.Println()
		status.Info("Saved target information:")
	}

	fmt.Println()
	printDeviceInfo(dev, result.Found)

	return nil
}

func handleTargetUsername(c *cli.Context) error {
	status := ui.NewStatusLine()

	cache, err := device.NewCache()
	if err != nil {
		return fmt.Errorf("creating cache: %w", err)
	}

	dev, err := cache.LoadTarget()
	if err != nil {
		return fmt.Errorf("loading target: %w", err)
	}
	if dev == nil {
		return fmt.Errorf("no target set. Use 'go_fastboot target <ip>' first")
	}

	var username string
	if c.NArg() >= 1 {
		username = c.Args().First()
	} else {
		// Prompt for username
		fmt.Print("Enter username: ")
		if _, err := fmt.Scanln(&username); err != nil {
			return fmt.Errorf("reading username: %w", err)
		}
	}

	if username == "" {
		return fmt.Errorf("username cannot be empty")
	}

	dev.Username = username
	if err := cache.SaveTarget(dev); err != nil {
		return fmt.Errorf("saving target: %w", err)
	}

	status.Success(fmt.Sprintf("Username set to '%s'", username))
	return nil
}

func printDeviceInfo(dev *device.Device, reachable bool) {
	fmt.Printf("  %-12s %s\n", "IP:", ui.FormatIP(dev.IP, reachable))
	if dev.Hostname != "" {
		fmt.Printf("  %-12s %s\n", "Hostname:", dev.Hostname)
	}
	if dev.MAC != "" {
		fmt.Printf("  %-12s %s\n", "MAC:", dev.MAC)
	}
	if dev.Subnet != "" {
		fmt.Printf("  %-12s %s\n", "Subnet:", dev.Subnet)
	}
	if dev.Username != "" {
		fmt.Printf("  %-12s %s\n", "Username:", dev.Username)
	}
	if dev.Password != "" {
		fmt.Printf("  %-12s %s\n", "Password:", "********")
	}
	fmt.Printf("  %-12s %s\n", "Mode:", ui.StyleMode(string(dev.Mode)))
	if !dev.LastSeen.IsZero() {
		fmt.Printf("  %-12s %s\n", "Last seen:", ui.FormatTime(dev.LastSeen))
	}
}

func inferSubnet(ipStr string) string {
	// Parse and validate IP address
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ""
	}
	// Convert to IPv4 if possible
	ip = ip.To4()
	if ip == nil {
		return "" // IPv6 not supported for subnet inference
	}
	// Apply /24 mask (set last octet to 0)
	ip[3] = 0
	return ip.String() + defaultSubnetMask
}

func probeDeviceMode(ctx context.Context, ip string) device.Mode {
	// Check fastboot first - it's a more definitive protocol check
	// than just checking if port 22 is open
	if device.CheckFastbootReachable(ctx, ip, probeTimeout) {
		return device.ModeFastboot
	}

	// Check SSH (Linux mode)
	if device.CheckReachable(ctx, ip, probeTimeout) {
		return device.ModeLinux
	}

	return device.ModeUnknown
}

// This file implements the "discover" command for network device scanning.
// It provides subnet scanning with MAC address filtering and mode detection.
package gofastboot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/urfave/cli/v2"
	"proto-distro/tools/go_fastboot/internal/device"
	"proto-distro/tools/go_fastboot/internal/ui"
)

const (
	defaultMACFilter       = "c8:98"
	defaultSubnet          = "192.168.1.0/24"
	defaultDiscoverTimeout = 30 * time.Second
)

func makeDiscoverCommand() *cli.Command {
	return &cli.Command{
		Name:  "discover",
		Usage: "Discover devices on the network",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "subnet",
				Aliases: []string{"s"},
				Usage:   "Subnet to scan (e.g., 192.168.1.0/24)",
				Value:   defaultSubnet,
			},
			&cli.DurationFlag{
				Name:    "timeout",
				Aliases: []string{"T"},
				Usage:   "Discovery timeout",
				Value:   defaultDiscoverTimeout,
			},
			&cli.StringFlag{
				Name:    "mac-filter",
				Aliases: []string{"m"},
				Value:   defaultMACFilter,
				Usage:   "Filter by MAC address prefix (use '*' for all devices)",
			},
		},
		Action: handleDiscover,
	}
}

func handleDiscover(c *cli.Context) error {
	subnet := c.String("subnet")
	timeout := c.Duration("timeout")
	macFilter := c.String("mac-filter")

	// Use "*" to disable filtering and show all devices
	showAll := macFilter == "*" || macFilter == "all"
	if showAll {
		macFilter = ""
	}

	spinner := ui.NewSpinner()
	status := ui.NewStatusLine()

	scanMsg := fmt.Sprintf("Scanning %s...", subnet)
	if macFilter != "" {
		scanMsg = fmt.Sprintf("Scanning %s (MAC filter: %s)...", subnet, macFilter)
	}
	spinner.Start(scanMsg)

	discovery := device.NewDiscovery(subnet)
	ctx, cancel := context.WithTimeout(c.Context, timeout)
	defer cancel()

	results := discovery.DiscoverDevices(ctx)

	var devices []device.DiscoveryResult
	for result := range results {
		if result.Error != nil {
			spinner.Stop()
			return fmt.Errorf("discovery failed: %w", result.Error)
		}
		devices = append(devices, result)
	}

	spinner.Stop()
	if err := c.Context.Err(); err != nil {
		return err
	}

	// Apply MAC filter if specified
	if macFilter != "" {
		devices = filterByMAC(devices, macFilter)
	}

	if len(devices) == 0 {
		if macFilter != "" {
			status.Warning(fmt.Sprintf("No devices found matching MAC prefix '%s'", macFilter))
		} else {
			status.Warning("No devices found")
		}
		return nil
	}

	status.Success(fmt.Sprintf("Found %d device(s)", len(devices)))
	fmt.Println()

	// Print table header
	fmt.Printf("  %-16s %-20s %-18s %s\n",
		ui.StyleBold.Render("IP"),
		ui.StyleBold.Render("Hostname"),
		ui.StyleBold.Render("MAC"),
		ui.StyleBold.Render("Mode"))
	fmt.Println("  " + strings.Repeat("-", 70))

	for _, dev := range devices {
		hostname := dev.Hostname
		if hostname == "" {
			hostname = ui.StyleMuted.Render("-")
		}
		mac := dev.MAC
		if mac == "" {
			mac = ui.StyleMuted.Render("-")
		}

		fmt.Printf("  %-16s %-20s %-18s %s\n",
			dev.IP,
			hostname,
			mac,
			ui.StyleMode(string(dev.Mode)))
	}

	fmt.Println()
	status.Info("Use 'go_fastboot target <ip>' to select a device")

	return nil
}

// filterByMAC filters devices by MAC address prefix (case-insensitive)
func filterByMAC(devices []device.DiscoveryResult, prefix string) []device.DiscoveryResult {
	prefix = strings.ToLower(prefix)
	var filtered []device.DiscoveryResult
	for _, dev := range devices {
		if dev.MAC != "" && strings.HasPrefix(strings.ToLower(dev.MAC), prefix) {
			filtered = append(filtered, dev)
		}
	}
	return filtered
}

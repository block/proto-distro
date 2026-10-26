// Package gofastboot implements device flashing and management workflows for miners.
//
// It exposes the same workflows used by the go_fastboot CLI so other tools can
// call them in-process without shelling out.
package gofastboot

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/urfave/cli/v2"
	"proto-distro/tools/go_fastboot/internal/device"
	"proto-distro/tools/go_fastboot/internal/fastboot"
)

const (
	defaultUsername = "admin"
	defaultPassword = "proto"
)

var (
	// Version and GitCommit are set via ldflags at build time.
	Version   = "dev"
	GitCommit = "unknown"
)

// EnterRequest configures a request to reboot a Linux-mode device into fastboot.
type EnterRequest struct {
	Target          string
	Username        string
	Password        string
	Timeout         time.Duration
	FastbootIP      string
	FastbootNetmask string
	FastbootGateway string
}

// FlashRequest configures a request to flash one or more images to a fastboot device.
type FlashRequest struct {
	Target    string
	Username  string
	Password  string
	ImagePath string
	Partition string
	Timeout   time.Duration
	NoReboot  bool
	Verity    bool
}

// ExitRequest configures a request to reboot a fastboot device back to Linux.
type ExitRequest struct {
	Target   string
	Username string
	Password string
	Timeout  time.Duration
	NoWait   bool
}

// RunCLI runs the go_fastboot command-line interface with the provided argv.
func RunCLI(args []string) error {
	app := &cli.App{
		Name:    "go_fastboot",
		Usage:   "Device flashing and management tool for miners",
		Version: fmt.Sprintf("%s (commit: %s)", Version, GitCommit),
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "verbose",
				Usage:   "Enable verbose output",
				EnvVars: []string{"GO_FASTBOOT_VERBOSE"},
			},
			&cli.StringFlag{
				Name:    "target",
				Aliases: []string{"t"},
				Usage:   "Target device (IP or hostname)",
				EnvVars: []string{"GO_FASTBOOT_TARGET"},
			},
			&cli.StringFlag{
				Name:    "username",
				Aliases: []string{"u"},
				Usage:   "SSH username (default: from saved target, or 'admin')",
				EnvVars: []string{"GO_FASTBOOT_USERNAME"},
			},
			&cli.StringFlag{
				Name:    "password",
				Aliases: []string{"p"},
				Usage:   "SSH password (not saved; default: proto; fallback credentials only during post-flash verification)",
				EnvVars: []string{"GO_FASTBOOT_PASSWORD"},
			},
		},
		Commands: []*cli.Command{
			makeTargetCommand(),
			makeDiscoverCommand(),
			makeStatusCommand(),
			makeEnterCommand(),
			makeExitCommand(),
			makeRebootCommand(),
			makeFlashCommand(),
		},
	}

	return app.Run(args)
}

// IsFastbootReachable checks whether a native fastboot UDP server responds.
func IsFastbootReachable(ip string, timeout time.Duration) bool {
	host := strings.TrimSpace(ip)
	if host == "" {
		return false
	}
	port := 0
	if parsedHost, portString, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
		port, err = strconv.Atoi(portString)
		if err != nil {
			return false
		}
	}
	client := fastboot.NewNativeClient(host).WithTimeout(timeout)
	if port != 0 {
		client = client.WithPort(port)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return client.CheckResponsive(ctx) == nil
}

func resolveRequestTarget(ctx context.Context, targetStr, username, password string, verifyReachable bool) (*device.Device, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	targetStr = strings.TrimSpace(targetStr)
	if targetStr != "" {
		target, err := device.ParseTarget(targetStr)
		if err != nil {
			return nil, fmt.Errorf("invalid target: %w", err)
		}

		dev := &device.Device{
			Username: resolveCredential(username, "", defaultUsername),
			Password: resolveCredential(password, "", defaultPassword),
		}

		if target.IP != "" {
			dev.IP = target.IP
		} else if target.Hostname != "" {
			dev.Hostname = target.Hostname
			ip, err := device.ResolveHostname(ctx, target.Hostname)
			if err != nil {
				return nil, fmt.Errorf("cannot resolve hostname: %w", err)
			}
			dev.IP = ip
		} else {
			return nil, fmt.Errorf("MAC-based targeting requires saved target. Use 'go_fastboot target <ip>' first.")
		}

		return dev, nil
	}

	cache, err := device.NewCache()
	if err != nil {
		return nil, fmt.Errorf("creating cache: %w", err)
	}

	dev, err := cache.LoadTarget()
	if err != nil {
		return nil, fmt.Errorf("loading target: %w", err)
	}
	if dev == nil {
		return nil, fmt.Errorf("no target specified. Use --target flag or 'go_fastboot target <ip>'")
	}

	dev.Username = resolveCredential(username, dev.Username, defaultUsername)
	dev.Password = resolveCredential(password, dev.Password, defaultPassword)

	if verifyReachable {
		tracker := device.NewTracker(dev, cache)
		if err := tracker.EnsureReachable(ctx); err != nil {
			return nil, fmt.Errorf("target not reachable: %w", err)
		}
	}

	return dev, nil
}

// saveCachedTarget updates state only when the request used the saved target.
// An explicit target is a one-off override, even when its IP matches the cache.
func saveCachedTarget(target string, dev *device.Device) error {
	if strings.TrimSpace(target) != "" {
		return nil
	}
	cache, err := device.NewCache()
	if err != nil {
		return err
	}
	return cache.SaveTarget(dev)
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// resolveCredential returns the first non-empty value from flag, cache, and default.
func resolveCredential(flag, cached, defaultVal string) string {
	if flag != "" {
		return flag
	}
	if cached != "" {
		return cached
	}
	return defaultVal
}

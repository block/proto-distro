// This file implements device state transition commands (enter, exit, reboot).
// It handles switching between Linux and fastboot modes.
package gofastboot

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/urfave/cli/v2"
	"proto-distro/tools/go_fastboot/internal/device"
	"proto-distro/tools/go_fastboot/internal/fastboot"
	"proto-distro/tools/go_fastboot/internal/ssh"
	"proto-distro/tools/go_fastboot/internal/ui"
)

const (
	defaultEnterTimeout       = 3 * time.Minute
	defaultExitTimeout        = 2 * time.Minute
	sshConnectionTimeout      = 10 * time.Second
	reachabilityCheckTimeout  = 2 * time.Second
	reachabilityCheckInterval = 3 * time.Second
	initialModeCheckTimeout   = 30 * time.Second // Timeout for initial mode check with retries
)

func makeEnterCommand() *cli.Command {
	return &cli.Command{
		Name:  "enter",
		Usage: "Enter fastboot mode",
		Flags: []cli.Flag{
			&cli.DurationFlag{
				Name:  "timeout",
				Usage: "Timeout waiting for fastboot mode",
				Value: defaultEnterTimeout,
			},
			&cli.StringFlag{
				Name:  "fastboot-ip",
				Usage: "IP address to request for the fastboot session",
			},
			&cli.StringFlag{
				Name:  "fastboot-netmask",
				Usage: "Netmask to request for the fastboot session",
			},
			&cli.StringFlag{
				Name:  "fastboot-gateway",
				Usage: "Gateway to request for the fastboot session",
			},
		},
		Action: handleEnter,
	}
}

func makeExitCommand() *cli.Command {
	return &cli.Command{
		Name:  "exit",
		Usage: "Exit fastboot mode (reboot to Linux)",
		Flags: []cli.Flag{
			&cli.DurationFlag{
				Name:  "timeout",
				Usage: "Timeout waiting for Linux boot",
				Value: defaultExitTimeout,
			},
			&cli.BoolFlag{
				Name:  "no-wait",
				Usage: "Send reboot command and return without waiting for Linux boot",
			},
		},
		Action: handleExit,
	}
}

func makeRebootCommand() *cli.Command {
	return &cli.Command{
		Name:   "reboot",
		Usage:  "Reboot the device",
		Action: handleReboot,
	}
}

func handleEnter(c *cli.Context) error {
	return Enter(c.Context, EnterRequest{
		Target:          c.String("target"),
		Username:        c.String("username"),
		Password:        c.String("password"),
		Timeout:         c.Duration("timeout"),
		FastbootIP:      c.String("fastboot-ip"),
		FastbootNetmask: c.String("fastboot-netmask"),
		FastbootGateway: c.String("fastboot-gateway"),
	})
}

// Enter reboots a Linux-mode device into fastboot and waits for the fastboot server.
func Enter(ctx context.Context, req EnterRequest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ssh.ValidateFastbootNetwork(req.FastbootIP, req.FastbootNetmask, req.FastbootGateway); err != nil {
		return err
	}
	if req.Timeout == 0 {
		req.Timeout = defaultEnterTimeout
	}

	dev, err := resolveRequestTarget(ctx, req.Target, req.Username, req.Password, true)
	if err != nil {
		return err
	}

	spinner := ui.NewSpinner()
	status := ui.NewStatusLine()

	// Check current mode with retries (device might be temporarily unreachable after reboot)
	spinner.Start("Checking device mode...")
	var mode device.Mode
	deadline := time.Now().Add(initialModeCheckTimeout)

	for {
		if err := ctx.Err(); err != nil {
			spinner.Error("Operation canceled")
			return err
		}
		mode = probeDeviceMode(ctx, dev.IP)
		if err := ctx.Err(); err != nil {
			spinner.Error("Operation canceled")
			return err
		}

		if mode == device.ModeFastboot {
			spinner.Success("Device is already in fastboot mode")
			return nil
		}

		if mode == device.ModeLinux {
			break // Success - device is in Linux mode
		}

		// Mode is Unknown - retry if we have time left
		if time.Now().After(deadline) {
			spinner.Error("Device not reachable in Linux mode")
			return fmt.Errorf("cannot reach device via SSH after %v", initialModeCheckTimeout)
		}

		if err := sleepContext(ctx, reachabilityCheckInterval); err != nil {
			spinner.Error("Operation canceled")
			return err
		}
	}

	spinner.Success("Device is in Linux mode")

	// Connect via SSH
	spinner.Start("Connecting via SSH...")
	sshClient, err := ssh.NewClientForDevice(ctx, dev, sshConnectionTimeout)
	if err != nil {
		spinner.Error("SSH connection failed")
		if errors.Is(err, ssh.ErrAuthFailed) {
			ui.PrintAuthHint(dev.IP, "enter")
		}
		return fmt.Errorf("SSH connection failed: %w", err)
	}
	defer sshClient.Close()

	spinner.Success("SSH connected")

	spinner.Start("Rebooting into fastboot mode...")
	if err := sshClient.EnterFastbootWithNetwork(ctx, req.FastbootIP, req.FastbootNetmask, req.FastbootGateway); err != nil {
		spinner.Error("Reboot command failed")
		return fmt.Errorf("entering fastboot: %w", err)
	}
	sshClient.Close()

	// Wait for fastboot to become available
	spinner.Update("Waiting for fastboot server...")
	fastbootTargetIP := dev.IP
	if req.FastbootIP != "" {
		fastbootTargetIP = req.FastbootIP
	}
	fbClient := fastboot.NewNativeClient(fastbootTargetIP)
	err = fbClient.WaitForDevice(ctx, req.Timeout)

	if err != nil {
		spinner.Error("Timeout waiting for fastboot mode")
		status.Info("The device may have rebooted to Linux instead. Try again.")
		return err
	}

	spinner.Success("Device is now in fastboot mode")

	dev.IP = fastbootTargetIP
	dev.Mode = device.ModeFastboot
	if err := saveCachedTarget(req.Target, dev); err != nil {
		status.Warning(fmt.Sprintf("Failed to save target state: %v", err))
	}

	return nil
}

func handleExit(c *cli.Context) error {
	return Exit(c.Context, ExitRequest{
		Target:   c.String("target"),
		Username: c.String("username"),
		Password: c.String("password"),
		Timeout:  c.Duration("timeout"),
		NoWait:   c.Bool("no-wait"),
	})
}

// Exit reboots a fastboot-mode device back to Linux.
func Exit(ctx context.Context, req ExitRequest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if req.Timeout == 0 {
		req.Timeout = defaultExitTimeout
	}

	// Use NoVerify since device is in fastboot mode (SSH won't be reachable)
	dev, err := resolveRequestTarget(ctx, req.Target, req.Username, req.Password, false)
	if err != nil {
		return err
	}

	spinner := ui.NewSpinner()

	// Check current mode
	spinner.Start("Checking device mode...")
	mode := probeDeviceMode(ctx, dev.IP)
	if err := ctx.Err(); err != nil {
		spinner.Error("Operation canceled")
		return err
	}

	if mode == device.ModeLinux {
		spinner.Success("Device is already in Linux mode")
		return nil
	}

	if mode != device.ModeFastboot {
		spinner.Error("Device not in fastboot mode")
		return fmt.Errorf("device not in fastboot mode")
	}

	spinner.Success("Device is in fastboot mode")

	// Reboot via fastboot
	spinner.Start("Rebooting to Linux...")
	fbClient := fastboot.NewNativeClient(dev.IP)
	if err := fbClient.Reboot(ctx); err != nil {
		spinner.Error("Reboot command failed")
		return fmt.Errorf("reboot failed: %w", err)
	}
	if req.NoWait {
		spinner.Success("Reboot command sent")
		return nil
	}

	// Wait for Linux to come up
	spinner.Update("Waiting for Linux boot...")
	deadline := time.Now().Add(req.Timeout)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			spinner.Error("Operation canceled")
			return err
		}
		if device.CheckReachable(ctx, dev.IP, reachabilityCheckTimeout) {
			spinner.Success("Device is now in Linux mode")

			dev.Mode = device.ModeLinux
			if err := saveCachedTarget(req.Target, dev); err != nil {
				ui.NewStatusLine().Warning(fmt.Sprintf("Failed to save target state: %v", err))
			}

			return nil
		}
		if err := sleepContext(ctx, reachabilityCheckInterval); err != nil {
			spinner.Error("Operation canceled")
			return err
		}
	}

	// Device might have gotten a new IP
	spinner.Warning("Device not reachable at original IP, attempting to relocate...")

	// Persist relocation only after Linux is confirmed, and only for a cached target.
	tracker := device.NewTracker(dev, nil)
	result := tracker.Relocate(ctx)

	if result.Found {
		if !device.CheckReachable(ctx, result.NewIP, reachabilityCheckTimeout) {
			spinner.Error("Relocated device is not in Linux mode")
			if err := ctx.Err(); err != nil {
				return err
			}
			return fmt.Errorf("device at %s is not reachable via SSH after reboot", result.NewIP)
		}
		dev.Mode = device.ModeLinux
		if err := saveCachedTarget(req.Target, dev); err != nil {
			ui.NewStatusLine().Warning(fmt.Sprintf("Failed to save target state: %v", err))
		}
		spinner.Success(fmt.Sprintf("Device found at %s (was %s)", result.NewIP, result.OldIP))
		return nil
	}
	if err := ctx.Err(); err != nil {
		spinner.Error("Operation canceled")
		return err
	}

	spinner.Error("Device not found after reboot")
	return fmt.Errorf("device not found at %s after reboot", dev.IP)
}

func handleReboot(c *cli.Context) error {
	// Use NoVerify since device might be in transition or we just want to reboot it
	dev, err := resolveRequestTarget(c.Context, c.String("target"), c.String("username"), c.String("password"), false)
	if err != nil {
		return err
	}

	spinner := ui.NewSpinner()

	// Check current mode
	spinner.Start("Checking device mode...")
	mode := probeDeviceMode(c.Context, dev.IP)
	if err := c.Context.Err(); err != nil {
		spinner.Error("Operation canceled")
		return err
	}

	switch mode {
	case device.ModeLinux:
		spinner.Success("Device is in Linux mode")

		// Connect via SSH and reboot
		spinner.Start("Rebooting...")
		sshClient, err := ssh.NewClientForDevice(c.Context, dev, sshConnectionTimeout)
		if err != nil {
			spinner.Error("SSH connection failed")
			if errors.Is(err, ssh.ErrAuthFailed) {
				ui.PrintAuthHint(dev.IP, "reboot")
			}
			return fmt.Errorf("SSH connection failed: %w", err)
		}
		defer sshClient.Close()
		if err := sshClient.Reboot(c.Context); err != nil {
			spinner.Error("Reboot failed")
			return fmt.Errorf("reboot failed: %w", err)
		}
		sshClient.Close()

		spinner.Success("Reboot command sent")

	case device.ModeFastboot:
		spinner.Success("Device is in fastboot mode")

		spinner.Start("Rebooting...")
		fbClient := fastboot.NewNativeClient(dev.IP)
		if err := fbClient.Reboot(c.Context); err != nil {
			spinner.Error("Reboot failed")
			return err
		}

		spinner.Success("Reboot command sent")

	default:
		spinner.Error("Device not reachable")
		return fmt.Errorf("device not reachable")
	}

	return nil
}

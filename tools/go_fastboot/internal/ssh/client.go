// Package ssh provides SSH client utilities for device management.
// It wraps golang.org/x/crypto/ssh with convenience methods for device operations,
// OS information retrieval, and reboot commands.
package ssh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	defaultPort    = "22"
	defaultTimeout = 10 * time.Second
)

// ErrAuthFailed indicates authentication failure
var ErrAuthFailed = errors.New("authentication failed")

// Client wraps an SSH connection with convenience methods
type Client struct {
	client   *ssh.Client
	host     string
	username string
}

// Config holds SSH connection configuration
type Config struct {
	Host     string
	Username string
	Password string
	Timeout  time.Duration
}

// DeviceInfo interface for device connection info (avoids circular import)
type DeviceInfo interface {
	GetIP() string
	GetUsername() string
	GetPassword() string
}

// NewClientForDevice creates an SSH client from a device with the specified timeout.
func NewClientForDevice(ctx context.Context, dev DeviceInfo, timeout time.Duration) (*Client, error) {
	return NewClient(ctx, Config{
		Host: dev.GetIP(), Username: dev.GetUsername(), Password: dev.GetPassword(), Timeout: timeout,
	})
}

// NewClient creates a new SSH client connection.
func NewClient(ctx context.Context, cfg Config) (*Client, error) {
	return newClientAt(ctx, cfg, net.JoinHostPort(cfg.Host, defaultPort))
}

func newClientAt(ctx context.Context, cfg Config, address string) (*Client, error) {
	sshConfig := newSSHClientConfig(cfg)
	ctx, cancel := context.WithTimeout(ctx, sshConfig.Timeout)
	defer cancel()
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("SSH connection failed: %w", errors.Join(err, ctx.Err()))
	}
	deadline, _ := ctx.Deadline()
	if err := raw.SetDeadline(deadline); err != nil {
		raw.Close()
		return nil, fmt.Errorf("setting SSH handshake deadline: %w", err)
	}
	stop := closeOnCancel(ctx, raw)
	conn, channels, requests, err := ssh.NewClientConn(raw, address, sshConfig)
	stop() // Join cancellation before clearing the handshake deadline.
	if ctx.Err() != nil {
		err = errors.Join(err, ctx.Err())
	} else if err != nil && !time.Now().Before(deadline) {
		err = errors.Join(err, context.DeadlineExceeded)
	}
	if err != nil {
		raw.Close()
		// x/crypto/ssh has no typed client authentication error. Match only
		// authentication exhaustion, not general handshake failures.
		if strings.HasPrefix(err.Error(), "ssh: handshake failed: ssh: unable to authenticate,") {
			return nil, fmt.Errorf("%w: check username/password (use --username/-u and --password/-p flags): %w", ErrAuthFailed, err)
		}
		return nil, fmt.Errorf("SSH connection failed: %w", err)
	}
	if err := raw.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("clearing SSH handshake deadline: %w", err)
	}
	return &Client{client: ssh.NewClient(conn, channels, requests), host: cfg.Host, username: cfg.Username}, nil
}

// closeOnCancel interrupts handshake, session creation, exec, and wait.
// The returned function stops or joins the callback; canceled clients cannot be reused.
func closeOnCancel(ctx context.Context, conn io.Closer) func() {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(done)
		conn.Close()
	})
	return func() {
		if !stop() {
			<-done
		} else if ctx.Err() != nil {
			conn.Close()
		}
	}
}

func newSSHClientConfig(cfg Config) *ssh.ClientConfig {
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}

	return &ssh.ClientConfig{
		User: cfg.Username,
		Auth: []ssh.AuthMethod{
			ssh.Password(cfg.Password),
		},
		// Rig onboarding devices are frequently reflashed and reused at the same IP.
		// Do not read host known-host files, verify host keys, or persist fingerprints.
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         cfg.Timeout,
		BannerCallback:  ssh.BannerDisplayStderr(), // Don't prompt for banner confirmation
	}
}

// Close closes the SSH connection
func (c *Client) Close() error {
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

// needsSudo returns true if the user is not root and commands need sudo
func (c *Client) needsSudo() bool {
	return c.username != "root"
}

// maybeSudo prepends "sudo " to a command if the user is not root
// Uses -n flag to prevent password prompts (fail if password needed)
func (c *Client) maybeSudo(command string) string {
	if c.needsSudo() {
		return "sudo -n " + command
	}
	return command
}

// Run executes a command with a ten-second maximum and returns its combined output.
func (c *Client) Run(ctx context.Context, command string) (string, error) {
	return c.run(ctx, command, defaultTimeout)
}

func (c *Client) run(ctx context.Context, command string, timeout time.Duration) (output string, err error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stop := closeOnCancel(ctx, c.client)
	defer func() {
		stop()
		err = errors.Join(err, ctx.Err())
	}()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	session, err := c.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("creating session: %w", err)
	}
	defer session.Close()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	data, err := session.CombinedOutput(command)
	if err != nil {
		return string(data), fmt.Errorf("running command: %w: %s", err, strings.TrimSpace(string(data)))
	}
	return strings.TrimSpace(string(data)), nil
}

func (c *Client) runReboot(ctx context.Context, command string) (err error) {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	stop := closeOnCancel(ctx, c.client)
	defer func() {
		stop()
		err = errors.Join(err, ctx.Err())
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	session, err := c.client.NewSession()
	if err != nil {
		return fmt.Errorf("creating reboot session: %w", err)
	}
	defer session.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	var stderr bytes.Buffer
	session.Stderr = &stderr
	if err := session.Start(command); err != nil {
		return fmt.Errorf("starting reboot command: %w", err)
	}
	err = session.Wait()
	var missing *ssh.ExitMissingError
	if errors.As(err, &missing) && ctx.Err() == nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("waiting for reboot command: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// OSRelease contains parsed information from /etc/os-release
type OSRelease struct {
	Version string
	Variant string
	Machine string
	GitHash string
}

// GetOSRelease retrieves detailed OS release information.
func (c *Client) GetOSRelease(ctx context.Context) (*OSRelease, error) {
	output, err := c.Run(ctx, "cat /etc/os-release 2>/dev/null || cat /etc/version 2>/dev/null")
	if err != nil {
		return nil, fmt.Errorf("getting OS release: %w", err)
	}

	release := &OSRelease{}

	// Parse key=value pairs from os-release
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasPrefix(line, "VERSION=") {
			release.Version = strings.Trim(strings.TrimPrefix(line, "VERSION="), "\"")
		} else if strings.HasPrefix(line, "VERSION_ID=") && release.Version == "" {
			release.Version = strings.Trim(strings.TrimPrefix(line, "VERSION_ID="), "\"")
		} else if strings.HasPrefix(line, "VARIANT=") {
			release.Variant = strings.Trim(strings.TrimPrefix(line, "VARIANT="), "\"")
		} else if strings.HasPrefix(line, "MACHINE=") {
			release.Machine = strings.Trim(strings.TrimPrefix(line, "MACHINE="), "\"")
		} else if strings.HasPrefix(line, "GIT_HASH=") {
			release.GitHash = strings.Trim(strings.TrimPrefix(line, "GIT_HASH="), "\"")
		}
	}

	// Return unknown if no version found
	if release.Version == "" {
		release.Version = "unknown"
	}

	return release, nil
}

// GetHostname retrieves the hostname from the device
func (c *Client) GetHostname(ctx context.Context) (string, error) {
	output, err := c.Run(ctx, "hostname")
	if err != nil {
		return "", fmt.Errorf("getting hostname: %w", err)
	}
	return output, nil
}

// GetMAC retrieves the MAC address of the primary interface
func (c *Client) GetMAC(ctx context.Context) (string, error) {
	// Try eth0 first, then any other interface
	output, err := c.Run(ctx, "cat /sys/class/net/eth0/address 2>/dev/null || ip link show | grep -oP 'link/ether \\K[^ ]+'")
	if err != nil {
		return "", fmt.Errorf("getting MAC address: %w", err)
	}
	return strings.ToLower(strings.TrimSpace(output)), nil
}

// Reboot reboots the device after the server accepts the command.
func (c *Client) Reboot(ctx context.Context) error {
	return c.runReboot(ctx, c.maybeSudo("reboot"))
}

// ValidateFastbootNetwork accepts optional canonical IPv4 values and a contiguous netmask.
func ValidateFastbootNetwork(ip, netmask, gateway string) error {
	for _, field := range []struct{ name, value string }{
		{"IP", ip}, {"netmask", netmask}, {"gateway", gateway},
	} {
		if field.value == "" {
			continue
		}
		addr, err := netip.ParseAddr(field.value)
		if err != nil || !addr.Is4() || addr.String() != field.value {
			return fmt.Errorf("invalid fastboot %s %q: expected canonical IPv4 address", field.name, field.value)
		}
		if field.name == "netmask" {
			if _, bits := net.IPMask(addr.AsSlice()).Size(); bits != 32 {
				return fmt.Errorf("invalid fastboot netmask %q: expected contiguous mask", netmask)
			}
		}
	}
	return nil
}

// EnterFastbootWithNetwork reboots into fastboot with optional positional network settings.
func (c *Client) EnterFastbootWithNetwork(ctx context.Context, ip, netmask, gateway string) error {
	if err := ValidateFastbootNetwork(ip, netmask, gateway); err != nil {
		return err
	}
	command := "reboot --fastboot"
	if ip != "" || netmask != "" || gateway != "" {
		// Validation restricts every value to a shell-safe IPv4 alphabet.
		command += fmt.Sprintf(" '%s' '%s' '%s'", ip, netmask, gateway)
	}
	return c.runReboot(ctx, c.maybeSudo(command))
}

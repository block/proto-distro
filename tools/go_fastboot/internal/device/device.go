// Package device provides core types and utilities for target device management.
// It includes device representation, target parsing (IP/hostname/MAC), and connectivity checks.
package device

import (
	"fmt"
	"net"
	"regexp"
	"time"
)

const sshPort = "22"

// Mode represents the current operating mode of a device
type Mode string

const (
	ModeUnknown  Mode = "UNKNOWN"
	ModeLinux    Mode = "LINUX"
	ModeFastboot Mode = "FASTBOOT"
	ModeUBoot    Mode = "UBOOT"
)

// Device represents a target device with its connection information
type Device struct {
	IP       string    `json:"ip"`
	Hostname string    `json:"hostname,omitempty"`
	MAC      string    `json:"mac,omitempty"`
	Subnet   string    `json:"subnet,omitempty"`
	Username string    `json:"username,omitempty"`
	Password string    `json:"-"` // Not persisted; use -p flag or GO_FASTBOOT_PASSWORD env var
	LastSeen time.Time `json:"last_seen,omitempty"`
	Mode     Mode      `json:"mode,omitempty"`
}

// Target represents a user-provided target identifier
type Target struct {
	Raw      string // Original input
	IP       string
	Hostname string
	MAC      string
}

var (
	ipRegex  = regexp.MustCompile(`^(\d{1,3}\.){3}\d{1,3}$`)
	macRegex = regexp.MustCompile(`^([0-9A-Fa-f]{2}[:-]){5}([0-9A-Fa-f]{2})$`)
)

// ParseTarget parses a target string into its components
// Supports: IP address, hostname, MAC address
func ParseTarget(target string) (*Target, error) {
	if target == "" {
		return nil, fmt.Errorf("empty target")
	}

	t := &Target{Raw: target}

	// Check if it's an IP address
	if ipRegex.MatchString(target) {
		ip := net.ParseIP(target)
		if ip == nil {
			return nil, fmt.Errorf("invalid IP address: %s", target)
		}
		t.IP = target
		return t, nil
	}

	// Check if it's a MAC address
	if macRegex.MatchString(target) {
		t.MAC = normalizeMAC(target)
		return t, nil
	}

	// Treat as hostname
	t.Hostname = target
	return t, nil
}

// normalizeMAC converts MAC address to lowercase with colons
func normalizeMAC(mac string) string {
	// Replace hyphens with colons and lowercase
	normalized := ""
	for _, c := range mac {
		if c == '-' {
			normalized += ":"
		} else if c >= 'A' && c <= 'F' {
			normalized += string(c + 32) // lowercase
		} else {
			normalized += string(c)
		}
	}
	return normalized
}

// GetIP returns the device IP address (implements ssh.DeviceInfo interface)
func (d *Device) GetIP() string {
	return d.IP
}

// GetUsername returns the device username (implements ssh.DeviceInfo interface)
func (d *Device) GetUsername() string {
	return d.Username
}

// GetPassword returns the device password (implements ssh.DeviceInfo interface)
func (d *Device) GetPassword() string {
	return d.Password
}

// HasMAC returns true if the device has a known MAC address
func (d *Device) HasMAC() bool {
	return d.MAC != ""
}

// HasHostname returns true if the device has a known hostname
func (d *Device) HasHostname() bool {
	return d.Hostname != ""
}

// UpdateIP updates the device's IP and last seen timestamp
func (d *Device) UpdateIP(newIP string) {
	d.IP = newIP
	d.LastSeen = time.Now()
}

// Credential represents a username/password pair for authentication
type Credential struct {
	Username string
	Password string
}

// DefaultCredentials returns the built-in fallback credential sets
// These are tried in order when the primary credentials fail
func DefaultCredentials() []Credential {
	return []Credential{
		{Username: "root", Password: ""},       // passwordless root (mfg image default)
		{Username: "admin", Password: "proto"}, // testrack default
	}
}

// BuildCredentialList returns a deduplicated list of credentials to try,
// starting with the primary credential followed by default fallbacks
func BuildCredentialList(primary Credential) []Credential {
	seen := make(map[Credential]bool)
	var result []Credential

	addCred := func(c Credential) {
		if !seen[c] {
			seen[c] = true
			result = append(result, c)
		}
	}

	// Primary credential first
	addCred(primary)

	// Then default fallbacks
	for _, c := range DefaultCredentials() {
		addCred(c)
	}

	return result
}

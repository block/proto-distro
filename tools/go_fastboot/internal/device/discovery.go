// This file implements network device discovery and scanning.
// It provides concurrent subnet scanning, MAC-based lookups, and ARP table parsing.
package device

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os/exec"
	"strings"
	"sync"
	"time"

	"proto-distro/tools/go_fastboot/internal/fastboot"
)

const (
	sshReadDeadline         = 1 * time.Second
	arpScanTimeout          = 30 * time.Second
	tcpDialTimeout          = 500 * time.Millisecond
	discoverProbeTimeout    = 2 * time.Second
	maxConcurrentScans      = 50
	sudoPermissionErrorCode = 1
)

// DiscoveryResult represents a device found during discovery
type DiscoveryResult struct {
	IP       string
	Hostname string
	MAC      string
	Mode     Mode
	Error    error
}

// Discovery handles device discovery operations
type Discovery struct {
	subnet string
}

// NewDiscovery creates a new discovery instance
func NewDiscovery(subnet string) *Discovery {
	return &Discovery{subnet: subnet}
}

// ResolveHostname resolves a hostname to an IP address
func ResolveHostname(ctx context.Context, hostname string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	addrs, err := net.DefaultResolver.LookupHost(ctx, hostname)
	if err != nil {
		return "", fmt.Errorf("DNS lookup failed: %w", err)
	}
	if len(addrs) == 0 {
		return "", fmt.Errorf("no addresses found for %s", hostname)
	}
	// Return first IPv4 address
	for _, addr := range addrs {
		if ip := net.ParseIP(addr); ip != nil && ip.To4() != nil {
			return addr, nil
		}
	}
	return addrs[0], nil
}

// CheckReachable checks for an SSH banner, without authenticating.
func CheckReachable(ctx context.Context, ip string, timeout time.Duration) bool {
	return checkSSHBanner(ctx, net.JoinHostPort(ip, sshPort), timeout)
}

func checkSSHBanner(ctx context.Context, address string, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return false
	}
	defer conn.Close()
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		conn.Close()
		close(closed)
	})
	defer func() {
		if !stop() {
			<-closed
		}
	}()

	// Bound dial plus banner by timeout, and the banner itself by one second.
	deadline, _ := ctx.Deadline()
	if bannerDeadline := time.Now().Add(sshReadDeadline); bannerDeadline.Before(deadline) {
		deadline = bannerDeadline
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		return false
	}
	var banner [4]byte
	_, err = io.ReadFull(conn, banner[:])
	return err == nil && ctx.Err() == nil && string(banner[:]) == "SSH-"
}

// CheckFastbootReachable checks if an IP has a fastboot server running.
// Uses native Go UDP implementation - no external fastboot binary required.
func CheckFastbootReachable(ctx context.Context, ip string, timeout time.Duration) bool {
	if ctx.Err() != nil {
		return false
	}
	return fastboot.CheckFastbootReachable(ctx, ip, timeout)
}

// GetMACFromARP tries to get MAC address from ARP table for a given IP
func GetMACFromARP(ctx context.Context, ip string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, discoverProbeTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// Try arp command first.
	cmd := exec.CommandContext(ctx, "arp", "-n", ip)
	cmd.WaitDelay = time.Second
	output, err := cmd.Output()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err == nil {
		if mac := parseARPOutput(string(output), ip); mac != "" {
			return mac, nil
		}
	}

	// Fallback to ip neigh on Linux, but never after cancellation.
	cmd = exec.CommandContext(ctx, "ip", "neigh", "show", ip)
	cmd.WaitDelay = time.Second
	output, err = cmd.Output()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err == nil {
		if mac := parseIPNeighOutput(string(output), ip); mac != "" {
			return mac, nil
		}
	}
	return "", fmt.Errorf("MAC address not found for %s", ip)
}

func parseARPOutput(output, ip string) string {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		matches := fields[0] == ip
		if strings.HasPrefix(fields[1], "(") {
			// BSD/macOS puts the IP after a hostname, which may itself look like an IP.
			matches = fields[1] == "("+ip+")"
		}
		if matches {
			for _, field := range fields[2:] {
				if mac := parseMACField(field); mac != "" {
					return mac
				}
			}
		}
	}
	return ""
}

func parseIPNeighOutput(output, ip string) string {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != ip {
			continue
		}
		for i := 1; i+1 < len(fields); i++ {
			if fields[i] == "lladdr" {
				if mac := parseMACField(fields[i+1]); mac != "" {
					return mac
				}
			}
		}
	}
	return ""
}

func parseMACField(field string) string {
	// macOS arp omits leading zeroes from individual octets.
	parts := strings.Split(field, ":")
	if len(parts) == 6 {
		for i, part := range parts {
			if len(part) == 1 {
				parts[i] = "0" + part
			}
		}
		field = strings.Join(parts, ":")
	}
	mac, err := net.ParseMAC(field)
	if err != nil || len(mac) != 6 {
		return ""
	}
	return mac.String()
}

// FindByMAC scans the subnet to find a device by MAC address.
func (d *Discovery) FindByMAC(ctx context.Context, mac string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, arpScanTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	mac = parseMACField(mac)
	if mac == "" {
		return "", fmt.Errorf("invalid MAC address")
	}

	// The external scan and fallback share one total budget.
	ip, err := d.arpScanForMAC(ctx, mac)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err == nil && ip != "" {
		return ip, nil
	}
	return d.scanSubnetForMAC(ctx, mac)
}

func (d *Discovery) arpScanForMAC(ctx context.Context, mac string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	first, last, err := ipv4HostRange(d.subnet)
	if err != nil {
		return "", err
	}
	prefix, _ := netip.ParsePrefix(d.subnet) // Validated by ipv4HostRange.
	if _, err := exec.LookPath("arp-scan"); err != nil {
		return "", fmt.Errorf("arp-scan not available")
	}

	// Use sudo -n to avoid blocking on a password prompt.
	cmd := exec.CommandContext(ctx, "sudo", "-n", "arp-scan", "-q", prefix.Masked().String())
	cmd.WaitDelay = time.Second
	output, err := cmd.Output()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == sudoPermissionErrorCode {
			return "", fmt.Errorf("arp-scan requires sudo access: run 'sudo -v' first or configure passwordless sudo for arp-scan")
		}
		return "", fmt.Errorf("arp-scan failed: %w", err)
	}

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || parseMACField(fields[1]) != mac {
			continue
		}
		addr, err := netip.ParseAddr(fields[0])
		if err != nil || !addr.Is4() {
			continue
		}
		bytes := addr.As4()
		value := uint64(binary.BigEndian.Uint32(bytes[:]))
		if value >= first && value <= last {
			return addr.String(), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("reading arp-scan output: %w", err)
	}
	return "", fmt.Errorf("MAC %s not found", mac)
}

func (d *Discovery) scanSubnetForMAC(ctx context.Context, mac string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	first, last, err := ipv4HostRange(d.subnet)
	if err != nil {
		return "", err
	}
	scanCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := scanSubnet(scanCtx, first, last, func(ctx context.Context, ip string) *DiscoveryResult {
		// A short TCP attempt populates ARP even when SSH is unavailable.
		conn, err := (&net.Dialer{Timeout: tcpDialTimeout}).DialContext(ctx, "tcp", net.JoinHostPort(ip, sshPort))
		if err == nil {
			conn.Close()
		}
		foundMAC, err := GetMACFromARP(ctx, ip)
		if err == nil && foundMAC == mac {
			return &DiscoveryResult{IP: ip, MAC: foundMAC}
		}
		return nil
	})
	var found string
	for result := range results {
		if found == "" {
			found = result.IP
			cancel()
		}
	}
	// Draining joins every worker before returning, including after a match.
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if found != "" {
		return found, nil
	}
	return "", fmt.Errorf("MAC %s not found on subnet %s", mac, d.subnet)
}

func ipv4HostRange(subnet string) (first, last uint64, err error) {
	prefix, err := netip.ParsePrefix(subnet)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid subnet: %w", err)
	}
	if !prefix.Addr().Is4() {
		return 0, 0, fmt.Errorf("unsupported subnet %q: IPv4 required", subnet)
	}
	bytes := prefix.Masked().Addr().As4()
	first = uint64(binary.BigEndian.Uint32(bytes[:]))
	last = first + (uint64(1) << (32 - prefix.Bits())) - 1
	if prefix.Bits() <= 30 {
		first++
		last--
	}
	return first, last, nil
}

// scanSubnet schedules lazily; consumers must drain results or cancel ctx.
// The channel closes only after all probes have returned.
func scanSubnet(ctx context.Context, first, last uint64, probe func(context.Context, string) *DiscoveryResult) <-chan DiscoveryResult {
	results := make(chan DiscoveryResult)
	go func() {
		defer close(results)
		var wg sync.WaitGroup
		defer wg.Wait()
		semaphore := make(chan struct{}, maxConcurrentScans)
		for value := first; value <= last; value++ {
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				return
			}
			if ctx.Err() != nil {
				<-semaphore
				return
			}
			var bytes [4]byte
			binary.BigEndian.PutUint32(bytes[:], uint32(value))
			ip := netip.AddrFrom4(bytes).String()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-semaphore }()
				if ctx.Err() != nil {
					return
				}
				result := probe(ctx, ip)
				if result == nil || ctx.Err() != nil {
					return
				}
				select {
				case results <- *result:
				case <-ctx.Done():
				}
			}()
		}
	}()
	return results
}

// DiscoverDevices scans the subnet for devices. Drain results or cancel ctx.
func (d *Discovery) DiscoverDevices(ctx context.Context) <-chan DiscoveryResult {
	first, last, err := ipv4HostRange(d.subnet)
	if err == nil {
		return scanSubnet(ctx, first, last, d.probeIP)
	}
	results := make(chan DiscoveryResult)
	go func() {
		defer close(results)
		if ctx.Err() != nil {
			return
		}
		select {
		case results <- DiscoveryResult{Error: err}:
		case <-ctx.Done():
		}
	}()
	return results
}

func (d *Discovery) probeIP(ctx context.Context, ip string) *DiscoveryResult {
	if ctx.Err() != nil {
		return nil
	}
	var mode Mode
	if CheckReachable(ctx, ip, discoverProbeTimeout) {
		mode = ModeLinux
	} else if CheckFastbootReachable(ctx, ip, discoverProbeTimeout) {
		mode = ModeFastboot
	} else {
		return nil
	}

	result := &DiscoveryResult{IP: ip, Mode: mode}
	// Optional metadata must not retain a worker indefinitely.
	metadataCtx, cancel := context.WithTimeout(ctx, discoverProbeTimeout)
	defer cancel()
	if mac, err := GetMACFromARP(metadataCtx, ip); err == nil {
		result.MAC = mac
	}
	if mode == ModeLinux {
		if names, err := net.DefaultResolver.LookupAddr(metadataCtx, ip); err == nil && len(names) > 0 {
			result.Hostname = strings.TrimSuffix(names[0], ".")
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	return result
}

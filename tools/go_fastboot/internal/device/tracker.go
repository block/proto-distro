// This file implements device tracking and automatic IP rediscovery.
// It uses multiple strategies (direct, DNS, ARP) to relocate devices after IP changes.
package device

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
)

const trackerProbeTimeout = 2 * time.Second

// Tracker handles device tracking and IP rediscovery
type Tracker struct {
	device *Device
	cache  *Cache
}

// NewTracker creates a new tracker for the given device
func NewTracker(d *Device, cache *Cache) *Tracker {
	return &Tracker{
		device: d,
		cache:  cache,
	}
}

// RelocateResult contains the result of a relocation attempt
type RelocateResult struct {
	Found  bool
	NewIP  string
	OldIP  string
	Method string // "direct", "dns", "arp"
	Device *Device
	Error  error
}

// checkReachableAnyMode checks if a device is reachable in either Linux or Fastboot mode
func checkReachableAnyMode(ctx context.Context, ip string) bool {
	if ctx.Err() != nil {
		return false
	}
	// Check fastboot first (more definitive protocol check)
	if CheckFastbootReachable(ctx, ip, trackerProbeTimeout) {
		return true
	}
	// Then check SSH/Linux
	return CheckReachable(ctx, ip, trackerProbeTimeout)
}

// Relocate attempts to find the device if its IP has changed.
// Cancellation stops relocation without trying another discovery method.
func (t *Tracker) Relocate(ctx context.Context) *RelocateResult {
	result := &RelocateResult{
		OldIP:  t.device.IP,
		Device: t.device,
	}
	if err := ctx.Err(); err != nil {
		result.Error = err
		return result
	}

	// 1. Try direct IP first (check both fastboot and SSH modes)
	reachable := checkReachableAnyMode(ctx, t.device.IP)
	if err := ctx.Err(); err != nil {
		result.Error = err
		return result
	}
	if reachable {
		result.Found = true
		result.NewIP = t.device.IP
		result.Method = "direct"
		t.device.LastSeen = time.Now()
		return result
	}

	// 2. Try hostname DNS resolution
	if t.device.HasHostname() {
		if newIP, err := ResolveHostname(ctx, t.device.Hostname); err == nil {
			reachable := checkReachableAnyMode(ctx, newIP)
			if err := ctx.Err(); err != nil {
				result.Error = err
				return result
			}
			if reachable {
				result.Found = true
				result.NewIP = newIP
				result.Method = "dns"
				t.device.UpdateIP(newIP)
				// Save updated IP to cache
				if t.cache != nil {
					if err := t.cache.SaveTarget(t.device); err != nil {
						log.Printf("warning: failed to save target to cache: %v", err)
					}
				}
				return result
			}
		}
	}
	if err := ctx.Err(); err != nil {
		result.Error = err
		return result
	}

	// 3. Try ARP scan for MAC address
	if t.device.HasMAC() && t.device.Subnet != "" {
		discovery := NewDiscovery(t.device.Subnet)
		newIP, err := discovery.FindByMAC(ctx, t.device.MAC)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			result.Error = err
			return result
		}
		if err == nil {
			reachable := checkReachableAnyMode(ctx, newIP)
			if err := ctx.Err(); err != nil {
				result.Error = err
				return result
			}
			if reachable {
				result.Found = true
				result.NewIP = newIP
				result.Method = "arp"
				t.device.UpdateIP(newIP)
				// Save updated IP to cache
				if t.cache != nil {
					if err := t.cache.SaveTarget(t.device); err != nil {
						log.Printf("warning: failed to save target to cache: %v", err)
					}
				}
				return result
			}
		}
	}
	if err := ctx.Err(); err != nil {
		result.Error = err
		return result
	}

	// Device not found
	result.Found = false
	result.Error = fmt.Errorf("device not found: tried direct IP, DNS (%s), ARP (%s)",
		t.device.Hostname, t.device.MAC)
	return result
}

// EnsureReachable ensures the device is reachable, relocating if necessary
// Returns an error if the device cannot be found
func (t *Tracker) EnsureReachable(ctx context.Context) error {
	result := t.Relocate(ctx)
	if !result.Found {
		return result.Error
	}
	return nil
}

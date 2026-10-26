package device

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestParseTarget(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantIP    string
		wantMAC   string
		wantHost  string
		wantError bool
	}{
		{
			name:   "valid IP address",
			input:  "192.168.1.100",
			wantIP: "192.168.1.100",
		},
		{
			name:   "valid IP address with zeros",
			input:  "10.0.0.1",
			wantIP: "10.0.0.1",
		},
		{
			name:    "valid MAC address with colons",
			input:   "aa:bb:cc:dd:ee:ff",
			wantMAC: "aa:bb:cc:dd:ee:ff",
		},
		{
			name:    "valid MAC address with hyphens",
			input:   "AA-BB-CC-DD-EE-FF",
			wantMAC: "aa:bb:cc:dd:ee:ff",
		},
		{
			name:     "hostname",
			input:    "proto-miner-001",
			wantHost: "proto-miner-001",
		},
		{
			name:     "hostname with dots",
			input:    "miner.local",
			wantHost: "miner.local",
		},
		{
			name:      "empty target",
			input:     "",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseTarget(tt.input)

			if tt.wantError {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if result.IP != tt.wantIP {
				t.Errorf("IP = %q, want %q", result.IP, tt.wantIP)
			}
			if result.MAC != tt.wantMAC {
				t.Errorf("MAC = %q, want %q", result.MAC, tt.wantMAC)
			}
			if result.Hostname != tt.wantHost {
				t.Errorf("Hostname = %q, want %q", result.Hostname, tt.wantHost)
			}
		})
	}
}

func TestNormalizeMAC(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"aa:bb:cc:dd:ee:ff", "aa:bb:cc:dd:ee:ff"},
		{"AA:BB:CC:DD:EE:FF", "aa:bb:cc:dd:ee:ff"},
		{"AA-BB-CC-DD-EE-FF", "aa:bb:cc:dd:ee:ff"},
		{"aA-bB-cC-dD-eE-fF", "aa:bb:cc:dd:ee:ff"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeMAC(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeMAC(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestCanceledDiscoveryAndTracking(t *testing.T) {
	for _, want := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(want.Error(), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			if want == context.DeadlineExceeded {
				cancel()
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			} else {
				cancel()
			}
			defer cancel()

			discovery := NewDiscovery("127.0.0.0/30")
			dev := &Device{
				IP: "127.0.0.1", Hostname: "miner.invalid",
				MAC: "aa:bb:cc:dd:ee:ff", Subnet: "127.0.0.0/30",
			}
			tracker := NewTracker(dev, nil)
			checks := map[string]func() error{
				"DNS": func() error {
					_, err := ResolveHostname(ctx, dev.Hostname)
					return err
				},
				"ARP": func() error {
					_, err := GetMACFromARP(ctx, dev.IP)
					return err
				},
				"MAC lookup": func() error {
					_, err := discovery.FindByMAC(ctx, dev.MAC)
					return err
				},
				"ARP scan": func() error {
					_, err := discovery.arpScanForMAC(ctx, dev.MAC)
					return err
				},
				"subnet scan": func() error {
					_, err := discovery.scanSubnetForMAC(ctx, dev.MAC)
					return err
				},
				"ensure reachable": func() error { return tracker.EnsureReachable(ctx) },
			}
			for name, check := range checks {
				t.Run(name, func(t *testing.T) {
					if err := check(); !errors.Is(err, want) {
						t.Fatalf("error = %v, want %v", err, want)
					}
				})
			}
			result := tracker.Relocate(ctx)
			if result.Found || !errors.Is(result.Error, want) {
				t.Fatalf("relocation = %+v, want cancellation", result)
			}
			if !dev.LastSeen.IsZero() || dev.IP != "127.0.0.1" {
				t.Fatalf("canceled relocation changed device: %+v", dev)
			}
			if CheckReachable(ctx, dev.IP, time.Second) || CheckFastbootReachable(ctx, dev.IP, time.Second) || checkReachableAnyMode(ctx, dev.IP) {
				t.Fatal("canceled probe reported reachable")
			}
			if discovery.probeIP(ctx, dev.IP) != nil {
				t.Fatal("canceled discovery probe returned a device")
			}
			select {
			case result, ok := <-discovery.DiscoverDevices(ctx):
				if ok {
					t.Fatalf("canceled scan returned %+v", result)
				}
			case <-time.After(time.Second):
				t.Fatal("canceled scan did not finish")
			}
		})
	}
}

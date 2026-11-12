package device

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDiscoveryPrefixRanges(t *testing.T) {
	for _, tt := range []struct {
		subnet      string
		first, last uint64
	}{
		{"0.0.0.0/0", 1, 0xfffffffe},
		{"192.0.2.3/30", 0xc0000201, 0xc0000202},
		{"192.0.2.1/31", 0xc0000200, 0xc0000201},
		{"192.0.2.1/32", 0xc0000201, 0xc0000201},
		{"255.255.255.255/30", 0xfffffffd, 0xfffffffe},
		{"255.255.255.255/31", 0xfffffffe, 0xffffffff},
		{"255.255.255.255/32", 0xffffffff, 0xffffffff},
	} {
		t.Run(tt.subnet, func(t *testing.T) {
			first, last, err := ipv4HostRange(tt.subnet)
			if err != nil || first != tt.first || last != tt.last {
				t.Fatalf("range = (%x, %x, %v), want (%x, %x, nil)", first, last, err, tt.first, tt.last)
			}
			if tt.subnet == "0.0.0.0/0" {
				return // Test numerical bounds, never enumerate the full address space.
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			seen := make(map[string]bool)
			for result := range scanSubnet(ctx, first, last, func(_ context.Context, ip string) *DiscoveryResult {
				return &DiscoveryResult{IP: ip}
			}) {
				if seen[result.IP] {
					t.Errorf("duplicate probe result: %s", result.IP)
				}
				seen[result.IP] = true
			}
			if ctx.Err() != nil || uint64(len(seen)) != last-first+1 {
				t.Fatalf("scan returned %v, context error %v", seen, ctx.Err())
			}
			for address := first; address <= last; address++ {
				ip := fmt.Sprintf("%d.%d.%d.%d", address>>24, (address>>16)&255, (address>>8)&255, address&255)
				if !seen[ip] {
					t.Errorf("missing address %s", ip)
				}
			}
		})
	}
	for _, subnet := range []string{"bad", "192.0.2.1", "192.0.2.1/33", "2001:db8::/64", "::ffff:192.0.2.1/128"} {
		t.Run(subnet, func(t *testing.T) {
			if _, _, err := ipv4HostRange(subnet); err == nil {
				t.Fatal("unsupported subnet accepted")
			}
		})
	}
}

func TestDiscoverySchedulerBoundedAndJoined(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, last, err := ipv4HostRange("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	var active, peak, started atomic.Int32
	ready := make(chan struct{}, 50)
	baseline := runtime.NumGoroutine()
	results := scanSubnet(ctx, first, last, func(ctx context.Context, ip string) *DiscoveryResult {
		started.Add(1)
		current := active.Add(1)
		defer active.Add(-1)
		for previous := peak.Load(); current > previous; previous = peak.Load() {
			if peak.CompareAndSwap(previous, current) {
				break
			}
		}
		ready <- struct{}{}
		<-ctx.Done()
		return &DiscoveryResult{IP: ip}
	})
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for range 50 {
		select {
		case <-ready:
		case <-deadline.C:
			t.Fatal("scheduler did not admit 50 probes")
		}
	}
	// All slots remain held. A producer must not create a goroutine per address.
	for range 100 {
		runtime.Gosched()
	}
	if extra := runtime.NumGoroutine() - baseline; extra > 60 {
		t.Errorf("blocked scan created %d goroutines; want bounded producer plus 50 workers", extra)
	}
	cancel()
	drainDiscoveryResults(t, results)
	if peak.Load() > 50 || started.Load() != 50 || active.Load() != 0 {
		t.Fatalf("peak=%d started=%d active=%d; want <=50, 50, 0 after joining", peak.Load(), started.Load(), active.Load())
	}
	for range 100 {
		runtime.Gosched()
	}
	if started.Load() != 50 {
		t.Fatal("probe started after scan shutdown")
	}
}

func TestDiscoverySchedulerImmediateCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	first, last, err := ipv4HostRange("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	var probes atomic.Int32
	results := scanSubnet(ctx, first, last, func(context.Context, string) *DiscoveryResult {
		probes.Add(1)
		return nil
	})
	drainDiscoveryResults(t, results)
	if probes.Load() != 0 {
		t.Fatalf("canceled scan started %d probes", probes.Load())
	}
}

func TestDiscoverySchedulerReusesSlots(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var probes atomic.Int32
	results := scanSubnet(ctx, 1, 200, func(_ context.Context, ip string) *DiscoveryResult {
		if probes.Add(1)%2 == 0 {
			return nil
		}
		return &DiscoveryResult{IP: ip}
	})
	seen := make(map[string]bool)
	for result := range results {
		if seen[result.IP] {
			t.Errorf("duplicate result for %s", result.IP)
		}
		seen[result.IP] = true
	}
	if ctx.Err() != nil || probes.Load() != 200 || len(seen) != 100 {
		t.Fatalf("probes=%d results=%d context=%v; want 200, 100, nil", probes.Load(), len(seen), ctx.Err())
	}
}

func TestDiscoverySchedulerStoppedConsumer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan struct{}, 50)
	baseline := runtime.NumGoroutine()
	results := scanSubnet(ctx, 1, 1000, func(_ context.Context, ip string) *DiscoveryResult {
		returned <- struct{}{}
		return &DiscoveryResult{IP: ip}
	})
	for range 50 {
		select {
		case <-returned:
		case <-time.After(2 * time.Second):
			t.Fatal("probes did not reach result delivery")
		}
	}
	// No receiver exists until after cancellation: result sends must be cancelable.
	cancel()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for runtime.NumGoroutine() > baseline {
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatal("canceled scan retained goroutines without a result consumer")
		}
	}
	drainDiscoveryResults(t, results)
}

func drainDiscoveryResults(t *testing.T, results <-chan DiscoveryResult) {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-results:
			if !ok {
				return
			}
		case <-timer.C:
			t.Fatal("scan did not join workers and close results")
		}
	}
}

func TestDiscoveryInvalidSubnet(t *testing.T) {
	for _, subnet := range []string{"not-a-subnet", "2001:db8::/64"} {
		t.Run(subnet, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			results := NewDiscovery(subnet).DiscoverDevices(ctx)
			select {
			case result, ok := <-results:
				if !ok || result.Error == nil {
					t.Fatalf("invalid prefix result = %+v, open=%v", result, ok)
				}
			case <-ctx.Done():
				t.Fatal("invalid prefix error was not delivered")
			}
			drainDiscoveryResults(t, results)
			ctx, stop := context.WithCancel(context.Background())
			results = NewDiscovery(subnet).DiscoverDevices(ctx)
			stop()
			drainDiscoveryResults(t, results)
		})
	}
}

func TestDiscoveryARPParsers(t *testing.T) {
	for _, tt := range []struct {
		name, output, ip, want string
		parse                  func(string, string) string
	}{
		{"macOS exact IP", "? (192.0.2.10) at aa:bb:cc:dd:ee:ff on en0 ifscope [ether]\n? (192.0.2.1) at 0:1:a:B:0:ff on en0 ifscope [ether]", "192.0.2.1", "00:01:0a:0b:00:ff", parseARPOutput},
		{"macOS neighbor only", "? (192.0.2.10) at aa:bb:cc:dd:ee:ff on en0 ifscope [ether]", "192.0.2.1", "", parseARPOutput},
		{"Linux arp", "Address HWtype HWaddress Flags Mask Iface\n192.0.2.10 ether 11:22:33:44:55:66 C eth0\n192.0.2.1 ether AA-BB-CC-DD-EE-FF C eth0", "192.0.2.1", "aa:bb:cc:dd:ee:ff", parseARPOutput},
		{"incomplete arp", "? (192.0.2.1) at (incomplete) on en0", "192.0.2.1", "", parseARPOutput},
		{"MAC substring rejected", "? (192.0.2.1) at xaa:bb:cc:dd:ee:ffy on en0", "192.0.2.1", "", parseARPOutput},
		{"MAC extra octet rejected", "? (192.0.2.1) at aa:bb:cc:dd:ee:ff:00 on en0", "192.0.2.1", "", parseARPOutput},
		{"MAC missing octet rejected", "? (192.0.2.1) at aa:bb:cc:dd:ee on en0", "192.0.2.1", "", parseARPOutput},
		{"hostname is not address", "192.0.2.1 (192.0.2.10) at aa:bb:cc:dd:ee:ff on en0", "192.0.2.1", "", parseARPOutput},
		{"Linux neigh exact IP", "192.0.2.10 dev eth0 lladdr 11:22:33:44:55:66 REACHABLE\n192.0.2.1 dev eth0 lladdr AA:BB:CC:DD:EE:FF STALE", "192.0.2.1", "aa:bb:cc:dd:ee:ff", parseIPNeighOutput},
		{"Linux neigh wrong IP", "192.0.2.10 dev eth0 lladdr aa:bb:cc:dd:ee:ff REACHABLE", "192.0.2.1", "", parseIPNeighOutput},
		{"Linux neigh incomplete", "192.0.2.1 dev eth0 INCOMPLETE", "192.0.2.1", "", parseIPNeighOutput},
		{"Linux neigh malformed MAC", "192.0.2.1 dev eth0 lladdr aa:bb:cc:dd:ee:fff REACHABLE", "192.0.2.1", "", parseIPNeighOutput},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.parse(tt.output, tt.ip); got != tt.want {
				t.Fatalf("MAC = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDiscoverySSHBanner(t *testing.T) {
	for _, mode := range []string{"fragmented", "wrong-prefix", "truncated", "canceled", "deadline", "banner-ceiling"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			timeout := 5 * time.Second
			if mode == "deadline" {
				timeout = 100 * time.Millisecond
			}
			result := make(chan bool, 1)
			start := time.Now()
			go func() { result <- checkSSHBanner(ctx, listener.Addr().String(), timeout) }()
			if err := listener.(*net.TCPListener).SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatal(err)
			}
			conn, err := listener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "fragmented":
				if _, err := conn.Write([]byte("S")); err != nil {
					t.Fatal(err)
				}
				select {
				case got := <-result:
					t.Fatalf("probe returned %v before complete banner", got)
				case <-time.After(20 * time.Millisecond):
				}
				for _, fragment := range []string{"S", "H", "-"} {
					if _, err := conn.Write([]byte(fragment)); err != nil {
						t.Fatal(err)
					}
				}
			case "wrong-prefix":
				if _, err := conn.Write([]byte("SSHx")); err != nil {
					t.Fatal(err)
				}
			case "truncated":
				if _, err := conn.Write([]byte("SSH")); err != nil {
					t.Fatal(err)
				}
				conn.Close()
			case "canceled":
				cancel()
			}
			select {
			case got := <-result:
				if got != (mode == "fragmented") {
					t.Fatalf("reachable = %v for %s", got, mode)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("banner probe ignored timeout or cancellation")
			}
			if (mode == "deadline" || mode == "canceled") && time.Since(start) > time.Second {
				t.Fatalf("short probe took %s", time.Since(start))
			}
			if mode == "canceled" {
				var data [1]byte
				if _, err := conn.Read(data[:]); !errors.Is(err, io.EOF) {
					t.Fatalf("cancellation did not close connection: %v", err)
				}
			}
		})
	}
}

// The helpers below run only copies of this test executable. PATH contains no
// system arp/ip binaries, and all control traffic uses an ephemeral loopback port.
func discoveryCommandFixture(t *testing.T, mode string) net.Listener {
	t.Helper()
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"arp", "ip"} {
		script := "#!/bin/sh\nexec '" + strings.ReplaceAll(executable, "'", "'\\''") + "' '-test.run=^TestDiscoveryCommandHelper$' -- " + name + "\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	if err := listener.(*net.TCPListener).SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("DISCOVERY_TEST_MODE", mode)
	t.Setenv("GORACE", os.Getenv("GORACE")+" atexit_sleep_ms=0")
	t.Setenv("DISCOVERY_TEST_CONTROL", listener.Addr().String())
	t.Setenv("DISCOVERY_TEST_FALLBACK", filepath.Join(dir, "fallback"))
	return listener
}

func acceptDiscoveryCommand(t *testing.T, listener net.Listener, want string) net.Conn {
	t.Helper()
	conn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	role, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || role != want+"\n" {
		t.Fatalf("helper role=%q error=%v, want %q", role, err, want)
	}
	return conn
}

func TestDiscoveryARPCommandCancellation(t *testing.T) {
	listener := discoveryCommandFixture(t, "cancel")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := GetMACFromARP(ctx, "192.0.2.1")
		result <- err
	}()
	acceptDiscoveryCommand(t, listener, "arp")
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ARP error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ARP command did not stop after cancellation")
	}
	if _, err := os.Stat(os.Getenv("DISCOVERY_TEST_FALLBACK")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fallback ran after cancellation: %v", err)
	}
}

func TestDiscoveryARPInheritedPipeWait(t *testing.T) {
	listener := discoveryCommandFixture(t, "inherited")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	type arpResult struct {
		mac string
		err error
	}
	result := make(chan arpResult, 1)
	go func() {
		mac, err := GetMACFromARP(ctx, "192.0.2.1")
		result <- arpResult{mac, err}
	}()
	holder := acceptDiscoveryCommand(t, listener, "hold")
	defer holder.Close()
	select {
	case got := <-result:
		if got.err != nil || got.mac != "aa:bb:cc:dd:ee:ff" {
			t.Fatalf("ARP fallback = (%q, %v)", got.mac, got.err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("inherited stdout prevented finite ARP wait")
	}
	if ctx.Err() != nil {
		t.Fatalf("wait exhausted parent deadline: %v", ctx.Err())
	}
	// Release the descendant only after GetMACFromARP has returned. Until this
	// point it still owns the original output pipe, proving WaitDelay bounded it.
	if _, err := holder.Write([]byte("release")); err != nil {
		t.Fatalf("pipe holder exited before release: %v", err)
	}
	var data [1]byte
	if _, err := holder.Read(data[:]); !errors.Is(err, io.EOF) {
		t.Fatalf("pipe holder did not exit after release: %v", err)
	}
}

func TestDiscoveryCommandHelper(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "--" || os.Getenv("DISCOVERY_TEST_MODE") == "" {
		return
	}
	role := os.Args[len(os.Args)-1]
	if role == "ip" {
		if err := os.WriteFile(os.Getenv("DISCOVERY_TEST_FALLBACK"), []byte("called"), 0600); err != nil {
			os.Exit(2)
		}
		fmt.Println("192.0.2.1 dev fixture lladdr aa:bb:cc:dd:ee:ff REACHABLE")
		os.Exit(0)
	}
	if role == "arp" && os.Getenv("DISCOVERY_TEST_MODE") == "inherited" {
		command := exec.Command(os.Args[0], "-test.run=^TestDiscoveryCommandHelper$", "--", "hold")
		command.Stdout, command.Stderr = os.Stdout, os.Stderr
		if err := command.Start(); err != nil {
			os.Exit(2)
		}
		if err := command.Process.Release(); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	conn, err := net.DialTimeout("tcp", os.Getenv("DISCOVERY_TEST_CONTROL"), 2*time.Second)
	if err != nil {
		os.Exit(2)
	}
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		os.Exit(2)
	}
	if _, err := fmt.Fprintln(conn, role); err != nil {
		os.Exit(2)
	}
	var data [16]byte
	_, _ = conn.Read(data[:])
	conn.Close()
	os.Exit(0)
}

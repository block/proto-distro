package gofastboot

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"proto-distro/tools/go_fastboot/internal/device"
	"proto-distro/tools/go_fastboot/internal/fastboot/udp"
)

// startWorkflowPeer serves command-only workflow regressions on the CLI's fixed port.
func startWorkflowPeer(t *testing.T, reply func(string) string) {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: udp.DefaultPort})
	if err != nil {
		t.Skipf("loopback fastboot port unavailable: %v", err)
	}
	done := make(chan struct{})
	t.Cleanup(func() { conn.Close(); <-done })
	go func() {
		defer close(done)
		buffer := make([]byte, 2048)
		for {
			n, addr, err := conn.ReadFromUDP(buffer)
			if err != nil {
				return
			}
			packet, err := udp.DecodePacket(buffer[:n])
			if err != nil {
				continue
			}
			response := &udp.Packet{ID: packet.ID, Sequence: packet.Sequence}
			switch packet.ID {
			case udp.PacketQuery:
				response.Data = []byte{0, 1}
			case udp.PacketInit:
				response.Data = []byte{0, 1, 4, 0}
			case udp.PacketFastboot:
				response.Data = []byte(reply(string(packet.Data)))
			}
			conn.WriteToUDP(response.Encode(), addr)
		}
	}()
}

func TestExitRejectsFastbootOnlyRelocation(t *testing.T) {
	// A listener may delay its SSH banner beyond the old 100 ms probe.
	if conn, err := net.DialTimeout("tcp", "127.0.0.1:22", reachabilityCheckTimeout); err == nil {
		conn.Close()
		t.Skip("loopback SSH port in use; cannot exercise fastboot-only relocation")
	}
	var products, reboots atomic.Int32
	startWorkflowPeer(t, func(command string) string {
		switch command {
		case "getvar:product":
			products.Add(1)
			return "OKAYloopback"
		case "reboot":
			reboots.Add(1)
			return "OKAY"
		default:
			return "FAILunexpected command"
		}
	})
	t.Chdir(t.TempDir())
	cache, err := device.NewCache()
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.SaveTarget(&device.Device{IP: "127.0.0.1", Mode: device.ModeFastboot}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Exit(ctx, ExitRequest{Timeout: time.Nanosecond}); err == nil {
		t.Fatal("fastboot-only relocation reported Linux success")
	}
	if ctx.Err() != nil || reboots.Load() != 1 || products.Load() < 2 {
		t.Fatalf("did not exercise reboot and relocation: context=%v reboots=%d products=%d", ctx.Err(), reboots.Load(), products.Load())
	}
	saved, err := cache.LoadTarget()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Mode != device.ModeFastboot {
		t.Fatalf("cached mode = %s", saved.Mode)
	}
}

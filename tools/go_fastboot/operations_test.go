package gofastboot

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"proto-distro/tools/go_fastboot/internal/device"
)

func TestCanceledOperations(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for name, run := range map[string]func() error{
		"enter":       func() error { return Enter(ctx, EnterRequest{Target: "127.0.0.1"}) },
		"exit":        func() error { return Exit(ctx, ExitRequest{Target: "127.0.0.1"}) },
		"flash":       func() error { return Flash(ctx, FlashRequest{Target: "127.0.0.1"}) },
		"reboot wait": func() error { return sleepContext(ctx, time.Hour) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("got %v, want context.Canceled", err)
			}
		})
	}
}

func TestExplicitTargetCommandsPreserveCache(t *testing.T) {
	if conn, err := net.DialTimeout("tcp", "127.0.0.1:22", reachabilityCheckTimeout); err == nil {
		conn.Close()
		t.Skip("loopback SSH port in use")
	}
	startWorkflowPeer(t, func(command string) string {
		switch command {
		case "getvar:product":
			return "OKAYloopback"
		case "reboot":
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
	if err := cache.SaveTarget(&device.Device{IP: "192.0.2.1", MAC: "aa:bb:cc:dd:ee:ff", Hostname: "saved-miner"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(".go_fastboot/target.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"GO_FASTBOOT_USERNAME", "GO_FASTBOOT_PASSWORD", "GO_FASTBOOT_TARGET"} {
		t.Setenv(key, "")
	}
	for _, args := range [][]string{{"enter"}, {"exit", "--no-wait"}, {"reboot"}} {
		if err := RunCLI(append([]string{"go_fastboot", "--target", "127.0.0.1"}, args...)); err != nil {
			t.Fatal(err)
		}
		after, err := os.ReadFile(".go_fastboot/target.json")
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("%s changed saved target: %s, %v", args[0], after, err)
		}
	}
}

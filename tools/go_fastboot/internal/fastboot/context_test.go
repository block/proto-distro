package fastboot

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNativeRejectsCanceledOperations(t *testing.T) {
	client := NewNativeClient("127.0.0.1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	checks := map[string]func() error{
		"responsive": func() error { return client.CheckResponsive(ctx) },
		"getvar":     func() error { _, err := client.GetVar(ctx, "product"); return err },
		"reboot":     func() error { return client.Reboot(ctx) },
		"erase":      func() error { return client.Erase(ctx, "boot") },
		"wait":       func() error { return client.WaitForDevice(ctx, time.Minute) },
	}
	for operation, check := range checks {
		t.Run(operation, func(t *testing.T) {
			if err := check(); !errors.Is(err, context.Canceled) {
				t.Fatalf("got %v, want context.Canceled", err)
			}
		})
	}
	for _, timeout := range []time.Duration{0, time.Minute} {
		result, err := client.Flash(ctx, "boot", "missing-image", timeout, nil)
		if !errors.Is(err, context.Canceled) || result == nil || !errors.Is(result.Error, context.Canceled) {
			t.Fatalf("Flash = %+v, %v; want canceled result and error", result, err)
		}
	}
}

func TestNativeExpiredFlashBudget(t *testing.T) {
	client := NewNativeClient("127.0.0.1")
	result, err := client.Flash(context.Background(), "boot", "missing-image", -time.Second, nil)
	if !errors.Is(err, context.DeadlineExceeded) || result == nil || !errors.Is(result.Error, context.DeadlineExceeded) {
		t.Fatalf("Flash = %+v, %v; want deadline result and error", result, err)
	}
	if err := client.WaitForDevice(context.Background(), -time.Second); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitForDevice = %v, want deadline", err)
	}
}

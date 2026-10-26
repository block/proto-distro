package gofastboot

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"proto-distro/tools/go_fastboot/internal/device"
	"time"
)

func TestTargetCredentialPrecedence(t *testing.T) {
	t.Chdir(t.TempDir())
	cache, err := device.NewCache()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, target, cachedUser, user, password, wantUser, wantPassword string }{
		{name: "defaults", wantUser: "admin", wantPassword: "proto"},
		{name: "cached username", cachedUser: "saved", wantUser: "saved", wantPassword: "proto"},
		{name: "explicit overrides", cachedUser: "saved", user: "chosen", password: "chosen-secret", wantUser: "chosen", wantPassword: "chosen-secret"},
		{name: "explicit target ignores cache", target: "127.0.0.1", cachedUser: "saved", wantUser: "admin", wantPassword: "proto"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := cache.SaveTarget(&device.Device{IP: "127.0.0.1", Username: tc.cachedUser, Password: "must-not-persist"}); err != nil {
				t.Fatal(err)
			}
			dev, err := resolveRequestTarget(context.Background(), tc.target, tc.user, tc.password, false)
			if err != nil {
				t.Fatal(err)
			}
			if dev.Username != tc.wantUser || dev.Password != tc.wantPassword {
				t.Fatalf("credentials did not follow flag/cache/default precedence")
			}
		})
	}
}

func TestTargetSetDefaultsAndOverrides(t *testing.T) {
	startWorkflowPeer(t, func(command string) string {
		if command == "getvar:product" {
			return "OKAYloopback"
		}
		return "FAILunexpected command"
	})
	if device.CheckReachable(context.Background(), "127.0.0.1", 100*time.Millisecond) {
		t.Skip("loopback SSH already present")
	}
	t.Chdir(t.TempDir())
	for _, key := range []string{"GO_FASTBOOT_USERNAME", "GO_FASTBOOT_PASSWORD", "GO_FASTBOOT_TARGET"} {
		t.Setenv(key, "")
	}
	for _, tc := range []struct {
		name  string
		flags []string
		user  string
	}{
		{"defaults", nil, "admin"},
		{"explicit", []string{"--username", "chosen", "--password", "not-persisted-secret"}, "chosen"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"go_fastboot"}, tc.flags...)
			if err := RunCLI(append(args, "target", "127.0.0.1")); err != nil {
				t.Fatal(err)
			}
			cache, err := device.NewCache()
			if err != nil {
				t.Fatal(err)
			}
			saved, err := cache.LoadTarget()
			if err != nil {
				t.Fatal(err)
			}
			if saved.Username != tc.user || saved.Mode != device.ModeFastboot || saved.Password != "" {
				t.Fatalf("saved target: %+v", saved)
			}
			data, err := os.ReadFile(filepath.Join(".go_fastboot", "target.json"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "not-persisted-secret") {
				t.Fatal("password persisted")
			}
		})
	}
}

func TestRequestTargetPersistence(t *testing.T) {
	t.Chdir(t.TempDir())
	cache, err := device.NewCache()
	if err != nil {
		t.Fatal(err)
	}
	want := &device.Device{IP: "192.0.2.1", MAC: "aa:bb:cc:dd:ee:ff", Hostname: "saved-miner", Mode: device.ModeLinux}
	if err := cache.SaveTarget(want); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"192.0.2.2", "192.0.2.1", " 192.0.2.1 "} {
		dev, err := resolveRequestTarget(context.Background(), target, "", "", false)
		if err != nil {
			t.Fatal(err)
		}
		dev.Mode = device.ModeFastboot
		if err := saveCachedTarget(target, dev); err != nil {
			t.Fatal(err)
		}
		got, err := cache.LoadTarget()
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("explicit target replaced saved metadata: %+v, %v", got, err)
		}
	}
	dev, err := resolveRequestTarget(context.Background(), "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	dev.Mode = device.ModeFastboot
	if err := saveCachedTarget("", dev); err != nil {
		t.Fatal(err)
	}
	want.Mode = device.ModeFastboot
	want.Username = defaultUsername
	got, err := cache.LoadTarget()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("cached workflow lost metadata or state: %+v, %v", got, err)
	}
}

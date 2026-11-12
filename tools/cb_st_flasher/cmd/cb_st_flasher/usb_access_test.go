package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/gousb"
)

func TestUSBAccessHint(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		goos string
		want bool
	}{
		{"access", gousb.ErrorAccess, "linux", true},
		{"wrapped access", fmt.Errorf("enumerate: %w", gousb.ErrorAccess), "linux", true},
		{"joined access", errors.Join(errors.New("close failed"), gousb.ErrorAccess), "linux", true},
		{"busy", gousb.ErrorBusy, "linux", false},
		{"text only", errors.New("bad access"), "linux", false},
		{"no error", nil, "linux", false},
		{"macOS", gousb.ErrorAccess, "darwin", false},
		{"Windows", gousb.ErrorAccess, "windows", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hint := usbAccessHint(tc.err, tc.goos)
			if (hint != "") != tc.want {
				t.Fatalf("hint = %q, want guidance: %v", hint, tc.want)
			}
			if tc.want && (!strings.Contains(hint, "bash -c") || !strings.Contains(hint, "udevadm trigger") || !strings.Contains(hint, `ATTRS{idVendor}=="0483"`) || !strings.Contains(hint, `TAG+="uaccess"`) || !strings.Contains(hint, `GROUP="plugdev"`) || !strings.Contains(hint, `MODE="0660"`) || strings.Contains(hint, "OWNER=")) {
				t.Fatalf("guidance must use bash, trigger udev, and match standard plugdev rules: %s", hint)
			}
		})
	}
}

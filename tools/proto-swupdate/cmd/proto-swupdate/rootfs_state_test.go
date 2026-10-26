package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/urfave/cli/v2"

	"proto-distro/tools/proto-swupdate/internal/config"
)

func TestReportRunError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantCode   int
		wantOutput string
	}{
		{name: "silent exit", err: cli.Exit("", 3), wantCode: 3},
		{name: "exit with message", err: cli.Exit("not ready", 2), wantCode: 2, wantOutput: "not ready\n"},
		{name: "regular error", err: errors.New("boom"), wantCode: 1, wantOutput: "error: boom\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			if got := reportRunError(&output, tt.err); got != tt.wantCode {
				t.Fatalf("exit code = %d, want %d", got, tt.wantCode)
			}
			if got := output.String(); got != tt.wantOutput {
				t.Fatalf("output = %q, want %q", got, tt.wantOutput)
			}
		})
	}
}

func TestRootfsUpdatePending(t *testing.T) {
	tests := []struct {
		name      string
		ustate    string
		readErr   error
		want      bool
		wantError bool
	}{
		{name: "update pending", ustate: "1", want: true},
		{name: "normal boot", ustate: "0"},
		{name: "rollback boot", ustate: "3"},
		{name: "empty state", ustate: ""},
		{name: "unreadable state", readErr: errors.New("unreadable"), wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			getEnv := func(name string) (string, error) {
				if name != cfg.UstateVar {
					t.Fatalf("read variable %q, want %q", name, cfg.UstateVar)
				}
				return tt.ustate, tt.readErr
			}

			got, err := rootfsUpdatePending(&cfg, getEnv)
			if (err != nil) != tt.wantError {
				t.Fatalf("error = %v, wantError = %v", err, tt.wantError)
			}
			if got != tt.want {
				t.Fatalf("pending = %v, want %v", got, tt.want)
			}
		})
	}
}

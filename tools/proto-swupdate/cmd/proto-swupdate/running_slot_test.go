package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/urfave/cli/v2"
)

func TestRunningSlotCommand(t *testing.T) {
	for _, tc := range []struct {
		name, slot  string
		fail, extra bool
	}{
		{name: "slot A", slot: "7"}, {name: "slot B", slot: "8"},
		{name: "detection fails", fail: true}, {name: "unexpected argument", extra: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			called := false
			sentinel := errors.New("root unavailable")
			app := &cli.App{Writer: &output, Commands: []*cli.Command{runningSlotCmd(func() (string, error) {
				called = true
				if tc.fail {
					return "", sentinel
				}
				return tc.slot, nil
			})}}
			args := []string{"proto-swupdate", "running-slot"}
			if tc.extra {
				args = append(args, "unexpected")
			}
			err := app.Run(args)
			if tc.extra {
				if err == nil || called {
					t.Fatalf("unexpected argument accepted: %v", err)
				}
				return
			}
			if tc.fail {
				if !errors.Is(err, sentinel) || output.Len() != 0 {
					t.Fatalf("failure output=%q err=%v", output.String(), err)
				}
				return
			}
			if err != nil || strings.TrimSpace(output.String()) != tc.slot {
				t.Fatalf("output=%q err=%v", output.String(), err)
			}
		})
	}
}

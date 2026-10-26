package main

import (
	"bytes"
	"errors"
	"proto-distro/tools/proto-swupdate/internal/config"
	"testing"
)

func TestPrintBootcount(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count uint32
		err   error
		want  string
	}{
		{name: "zero", want: "0\n"},
		{name: "decimal", count: 10, want: "10\n"},
		{name: "maximum", count: ^uint32(0), want: "4294967295\n"},
		{name: "read failure", err: errors.New("no access")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			cfg := config.Config{BootcountAddr: 0x12345678}
			calls := 0
			err := printBootcount(&cfg, &out, func(addr uint32) (uint32, error) {
				calls++
				if addr != cfg.BootcountAddr {
					t.Fatalf("ignored configured address: %x", addr)
				}
				return tc.count, tc.err
			})
			if calls != 1 || out.String() != tc.want || !errors.Is(err, tc.err) {
				t.Fatalf("calls=%d output=%q error=%v", calls, out.String(), err)
			}
			if err != nil {
				var stderr bytes.Buffer
				if reportRunError(&stderr, err) == 0 || stderr.Len() == 0 {
					t.Fatal("read error must fail and report to stderr")
				}
			}
		})
	}
}

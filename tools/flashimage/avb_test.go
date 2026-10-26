package flashimage

import (
	"math"
	"testing"
)

func TestValidateAVBSizes(t *testing.T) {
	const largestAligned = math.MaxInt64 - 511
	for _, tc := range []struct {
		name                     string
		capacity, raw, body      int64
		tail, wantBody, wantTail int64
	}{
		{"minimum trio", 1536, 512, 512, 512, 512, 1024},
		{"no gap", 8192, 4096, 3584, 512, 4096, 7680},
		{"current capacity", 1 << 30, 4096, 5632, 512, 4096, 1073741312},
		{"sector aligned raw", 1 << 30, 61952, 5632, 512, 61952, 1073741312},
		{"alternate capacity", 128 << 10, 61952, 5632, 512, 61952, 130560},
		{"largest capacity", largestAligned, 512, 512, 512, 512, largestAligned - 512},
		{"largest raw", largestAligned, largestAligned - 1024, 512, 512, largestAligned - 1024, largestAligned - 512},
		{"largest body", largestAligned, 512, largestAligned - 1024, 512, 512, largestAligned - 512},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, tail, err := ValidateAVBSizes(tc.capacity, tc.raw, tc.body, tc.tail)
			if err != nil || body != tc.wantBody || tail != tc.wantTail {
				t.Fatalf("offsets = (%d, %d), error %v; want (%d, %d)", body, tail, err, tc.wantBody, tc.wantTail)
			}
		})
	}
}

func TestValidateAVBSizesRejectsInvalidInputs(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		capacity, raw, body, tail int64
	}{
		{"zero capacity", 0, 512, 512, 512},
		{"negative capacity", -512, 512, 512, 512},
		{"unaligned capacity", 8191, 512, 512, 512},
		{"MaxInt64 capacity", math.MaxInt64, 512, 512, 512},
		{"MinInt64 capacity", math.MinInt64, 512, 512, 512},
		{"one sector capacity", 512, 512, 512, 512},
		{"two sector capacity", 1024, 512, 512, 512},
		{"zero raw", 8192, 0, 512, 512},
		{"negative raw", 8192, -512, 512, 512},
		{"unaligned raw", 8192, 4095, 512, 512},
		{"oversized raw", 8192, 8192, 512, 512},
		{"raw reaches tail", 8192, 7680, 512, 512},
		{"MaxInt64 raw", 8192, math.MaxInt64, 512, 512},
		{"large aligned raw", 8192, math.MaxInt64 - 511, 512, 512},
		{"MinInt64 raw", 8192, math.MinInt64, 512, 512},
		{"zero body", 8192, 4096, 0, 512},
		{"negative body", 8192, 4096, -512, 512},
		{"unaligned body", 8192, 4096, 1023, 512},
		{"one sector overlap", 8192, 4096, 4096, 512},
		{"oversized body", 8192, 4096, 8192, 512},
		{"MaxInt64 body", 8192, 4096, math.MaxInt64, 512},
		{"large aligned body", 8192, 4096, math.MaxInt64 - 511, 512},
		{"MinInt64 body", 8192, 4096, math.MinInt64, 512},
		{"overflowing sum", math.MaxInt64 - 511, math.MaxInt64 - 511, math.MaxInt64 - 511, 512},
		{"zero tail", 8192, 4096, 512, 0},
		{"negative tail", 8192, 4096, 512, -512},
		{"short tail", 8192, 4096, 512, 511},
		{"long tail", 8192, 4096, 512, 1024},
		{"MaxInt64 tail", 8192, 4096, 512, math.MaxInt64},
		{"MinInt64 tail", 8192, 4096, 512, math.MinInt64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, tail, err := ValidateAVBSizes(tc.capacity, tc.raw, tc.body, tc.tail)
			if err == nil || body != 0 || tail != 0 {
				t.Fatalf("invalid sizes returned (%d, %d), error %v", body, tail, err)
			}
		})
	}
}

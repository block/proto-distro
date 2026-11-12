package main

import (
	"bytes"
	"testing"
)

func TestRewriteOSReleaseVersionReplacesExistingVersion(t *testing.T) {
	input := []byte("NAME=\"Proto\"\nVERSION=\"0.0.0\"\nDISTRO_BASE_VERSION=\"1.2.3\"\n")
	got := rewriteOSReleaseVersion(input, "1.2.3")
	want := []byte("NAME=\"Proto\"\nVERSION=\"1.2.3\"\nDISTRO_BASE_VERSION=\"1.2.3\"\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("rewriteOSReleaseVersion() = %q, want %q", got, want)
	}
}

func TestRewriteOSReleaseVersionAppendsWhenMissing(t *testing.T) {
	input := []byte("NAME=\"Proto\"\n")
	got := rewriteOSReleaseVersion(input, "1.2.3")
	want := []byte("NAME=\"Proto\"\nVERSION=\"1.2.3\"\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("rewriteOSReleaseVersion() = %q, want %q", got, want)
	}
}

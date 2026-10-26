package builder

import (
	"testing"
)

func TestToVolumeRelativePath(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "build subpath", input: "build/tmp-glibc/deploy/images/c3-p1/", want: "tmp-glibc/deploy/images/c3-p1"},
		{name: "build root", input: "build", want: "."},
		{name: "already relative", input: "tmp-glibc/deploy/images/c3-p1", want: "tmp-glibc/deploy/images/c3-p1"},
		{name: "absolute path", input: "/tmp-glibc/deploy/images/c3-p1", wantErr: true},
		{name: "empty", input: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toVolumeRelativePath(tt.input, "build")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("toVolumeRelativePath(%q) error = nil, want error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("toVolumeRelativePath(%q) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("toVolumeRelativePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

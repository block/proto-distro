package fastboot

import (
	"testing"
)

func TestParseResponse(t *testing.T) {
	tests := []struct {
		name        string
		data        []byte
		wantStatus  ResponseStatus
		wantMessage string
		wantErr     bool
	}{
		{
			name:        "OKAY with message",
			data:        []byte("OKAYsuccess"),
			wantStatus:  StatusOkay,
			wantMessage: "success",
		},
		{
			name:        "OKAY empty",
			data:        []byte("OKAY"),
			wantStatus:  StatusOkay,
			wantMessage: "",
		},
		{
			name:        "FAIL with message",
			data:        []byte("FAILdevice locked"),
			wantStatus:  StatusFail,
			wantMessage: "device locked",
		},
		{
			name:        "DATA with size",
			data:        []byte("DATA00001000"),
			wantStatus:  StatusData,
			wantMessage: "00001000",
		},
		{
			name:        "INFO message",
			data:        []byte("INFOwriting partition"),
			wantStatus:  StatusInfo,
			wantMessage: "writing partition",
		},
		{
			name:        "TEXT message",
			data:        []byte("TEXTsome output"),
			wantStatus:  StatusText,
			wantMessage: "some output",
		},
		{
			name:    "too short",
			data:    []byte("OK"),
			wantErr: true,
		},
		{
			name:    "unknown status",
			data:    []byte("BADDunknown"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := parseResponse(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if resp.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q", resp.Status, tt.wantStatus)
			}
			if resp.Message != tt.wantMessage {
				t.Errorf("Message = %q, want %q", resp.Message, tt.wantMessage)
			}
		})
	}
}

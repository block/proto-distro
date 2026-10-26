package swupdate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// FIPInstallStatePath is the tmpfs handoff between `proto-swupdate install`
// (writer) and `proto-swupdate fip post-install` (reader). It holds the SWU
// path so post-install can re-parse fip.bin and call VerifyFIP.
const FIPInstallStatePath = "/run/proto-swupdate/fip-install"

type FIPInstallState struct {
	SWUPath string `json:"swu_path"`
}

func WriteFIPInstallState(s FIPInstallState) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(FIPInstallStatePath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(FIPInstallStatePath, data, 0o644)
}

func ReadFIPInstallState() (FIPInstallState, error) {
	var s FIPInstallState
	data, err := os.ReadFile(FIPInstallStatePath)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("parse %s: %w", FIPInstallStatePath, err)
	}
	return s, nil
}

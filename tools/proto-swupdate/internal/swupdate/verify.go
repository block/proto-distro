package swupdate

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"proto-distro/tools/proto-swupdate/internal/fwumeta"
	"proto-distro/tools/proto-swupdate/internal/swu"
)

const PartlabelDir = "/dev/disk/by-partlabel"

// VerifyFIP returns nil iff the fip.bin embedded in swuPath matches the
// content at the start of the FIP partition for the given bank (bank 0 →
// fip-a, bank 1 → fip-b). Matching = sha256 of the first fip-size bytes
// of the partition equals sha256 of fip.bin inside the SWU.
//
// Used as:
//   - install pre-flight skip check (is the currently-booted bank already
//     the requested fip?),
//   - post-install acceptance gate (did the raw write land bit-perfect?),
//   - `proto-swupdate fip verify` CLI for manual checks.
func VerifyFIP(swuPath string, bank uint32) error {
	if bank >= fwumeta.NumBanks {
		return fmt.Errorf("invalid bank %d", bank)
	}
	entry, err := swu.FindFile(swuPath, fipNamePattern)
	if err != nil {
		return fmt.Errorf("locate fip in swu: %w", err)
	}
	expected, err := hashFileRange(swuPath, entry.DataOffset, entry.Size)
	if err != nil {
		return fmt.Errorf("hash fip from swu: %w", err)
	}
	partDev := filepath.Join(PartlabelDir, "fip-"+fwumeta.BankLabels[bank])
	got, err := hashFileRange(partDev, 0, entry.Size)
	if err != nil {
		return fmt.Errorf("hash %s: %w", partDev, err)
	}
	if !bytes.Equal(expected, got) {
		return fmt.Errorf("fip mismatch on %s: expected sha256=%x got=%x",
			partDev, expected, got)
	}
	return nil
}

func hashFileRange(path string, offset, length int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	h := sha256.New()
	if _, err := io.CopyN(h, f, length); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

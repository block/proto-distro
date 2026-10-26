package flashimage

import "fmt"

const SectorSize int64 = 512

// ValidateAVBSizes returns the body and tail offsets in a fixed-size partition.
// It checks placement, not AVB authenticity. No gap is required between the body
// and tail. All offsets are zero on failure.
func ValidateAVBSizes(partitionSize, rawSize, bodySize, tailSize int64) (bodyOffset, tailOffset int64, err error) {
	if partitionSize <= 0 || partitionSize%SectorSize != 0 {
		return 0, 0, fmt.Errorf("partition size must be positive and sector-aligned: %d", partitionSize)
	}
	if rawSize <= 0 || rawSize%SectorSize != 0 {
		return 0, 0, fmt.Errorf("raw size must be positive and sector-aligned: %d", rawSize)
	}
	if bodySize <= 0 || bodySize%SectorSize != 0 {
		return 0, 0, fmt.Errorf("AVB body size must be positive and sector-aligned: %d", bodySize)
	}
	if tailSize != SectorSize {
		return 0, 0, fmt.Errorf("AVB tail must be exactly %d bytes: %d", SectorSize, tailSize)
	}
	remaining := partitionSize - SectorSize
	if rawSize > remaining {
		return 0, 0, fmt.Errorf("raw image exceeds space before AVB tail")
	}
	if bodySize > remaining-rawSize {
		return 0, 0, fmt.Errorf("AVB body overlaps tail")
	}
	return rawSize, remaining, nil
}

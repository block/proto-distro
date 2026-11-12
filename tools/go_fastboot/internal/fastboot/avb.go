package fastboot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"

	"proto-distro/tools/flashimage"
)

// PrepareAVBImage combines a raw image and its exact AVB companions into a sparse
// image. Missing companions are optional; every other preparation failure is fatal.
// The caller owns removal of the returned file. DONT_CARE preserves the gap; this
// validates placement and target geometry, not AVB authenticity.
func PrepareAVBImage(ctx context.Context, imagePath, partition string, partitionSize int64, getVar func(context.Context, string) (string, error)) (preparedPath string, missing []string, err error) {
	paths := [3]string{imagePath, imagePath + ".avb.body", imagePath + ".avb.tail"}
	for _, path := range paths[1:] {
		info, statErr := os.Stat(path)
		if os.IsNotExist(statErr) {
			missing = append(missing, path)
			continue
		}
		if statErr != nil {
			err = errors.Join(err, fmt.Errorf("stat AVB companion %s: %w", path, statErr))
		} else if !info.Mode().IsRegular() {
			err = errors.Join(err, fmt.Errorf("AVB companion %s is not a regular file", path))
		}
	}
	if err != nil {
		return "", nil, err
	}
	if len(missing) != 0 {
		return "", missing, nil
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}

	var inputs [3]*os.File
	var sizes [3]int64
	var output *os.File
	defer func() {
		for _, input := range inputs {
			if input != nil {
				err = errors.Join(err, input.Close())
			}
		}
		if output != nil {
			err = errors.Join(err, output.Close())
			if err != nil {
				err = errors.Join(err, os.Remove(output.Name()))
				preparedPath = ""
			}
		}
	}()
	for i, path := range paths {
		// Check before opening so a nonregular source cannot block on open.
		info, statErr := os.Stat(path)
		if statErr != nil {
			return "", nil, fmt.Errorf("stat %s: %w", path, statErr)
		}
		if !info.Mode().IsRegular() {
			return "", nil, fmt.Errorf("%s is not a regular file", path)
		}
		inputs[i], err = os.Open(path)
		if err != nil {
			return "", nil, fmt.Errorf("open %s: %w", path, err)
		}
		info, err = inputs[i].Stat()
		if err != nil {
			return "", nil, fmt.Errorf("stat opened %s: %w", path, err)
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return "", nil, fmt.Errorf("%s must be a nonempty regular file", path)
		}
		sizes[i] = info.Size()
	}
	isSparse, err := IsSparseImage(imagePath)
	if err != nil {
		return "", nil, fmt.Errorf("inspect raw image: %w", err)
	}
	if isSparse {
		return "", nil, fmt.Errorf("AVB preparation requires a raw, not sparse, image")
	}
	bodyOffset, tailOffset, err := flashimage.ValidateAVBSizes(partitionSize, sizes[0], sizes[1], sizes[2])
	if err != nil {
		return "", nil, err
	}
	if partitionSize/flashimage.SectorSize > math.MaxUint32 {
		return "", nil, fmt.Errorf("partition size exceeds sparse block count")
	}
	getSize := func(name string) (uint64, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		value, err := getVar(ctx, name)
		if err != nil {
			return 0, fmt.Errorf("getvar %s: %w", name, err)
		}
		size, err := parseHexSize(value)
		if err != nil {
			return 0, fmt.Errorf("getvar %s: %w", name, err)
		}
		return size, nil
	}
	targetSize, err := getSize("partition-size:" + partition)
	if err != nil {
		return "", nil, err
	}
	if targetSize != uint64(partitionSize) {
		return "", nil, fmt.Errorf("partition %s size %d differs from profile size %d", partition, targetSize, partitionSize)
	}
	maxDownloadSize, err := getSize("max-download-size")
	if err != nil {
		return "", nil, err
	}
	limit, err := effectiveDownloadSize(maxDownloadSize)
	if err != nil {
		return "", nil, err
	}
	// Reserve the file header, one RAW header, and leading/trailing skip headers.
	reserve := int64(sparseHeaderSize + 3*chunkHeaderSize)
	if limit < reserve+flashimage.SectorSize {
		return "", nil, fmt.Errorf("max-download-size is too small for a sparse sector")
	}
	maxRaw := (limit - reserve) / flashimage.SectorSize * flashimage.SectorSize
	maxRaw = min(maxRaw, (int64(math.MaxUint32)-chunkHeaderSize)/flashimage.SectorSize*flashimage.SectorSize)
	gap := tailOffset - bodyOffset - sizes[1]
	chunks := int64(1) // Tail RAW chunk.
	for _, size := range sizes[:2] {
		chunks += (size-1)/maxRaw + 1
	}
	if gap > 0 {
		chunks++
	}
	if chunks > math.MaxUint32 {
		return "", nil, fmt.Errorf("AVB image exceeds sparse chunk count")
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	output, err = os.CreateTemp("", "go-fastboot-avb-*.sparse")
	if err != nil {
		return "", nil, fmt.Errorf("create AVB sparse image: %w", err)
	}
	if err := writeSparseHeader(output, uint32(flashimage.SectorSize), uint32(partitionSize/flashimage.SectorSize), uint32(chunks)); err != nil {
		return "", nil, err
	}
	buffer := make([]byte, min(maxRaw, 64*1024))
	for i, input := range inputs {
		if i == 2 && gap > 0 {
			if err := writeChunkHeader(output, chunkTypeDontCare, uint32(gap/flashimage.SectorSize), 0); err != nil {
				return "", nil, err
			}
		}
		for remaining := sizes[i]; remaining > 0; {
			chunkSize := min(remaining, maxRaw)
			if err := ctx.Err(); err != nil {
				return "", nil, err
			}
			if err := writeChunkHeader(output, chunkTypeRaw, uint32(chunkSize/flashimage.SectorSize), uint32(chunkSize)); err != nil {
				return "", nil, err
			}
			for left := chunkSize; left > 0; {
				if err := ctx.Err(); err != nil {
					return "", nil, err
				}
				data := buffer[:min(left, int64(len(buffer)))]
				if _, err := io.ReadFull(input, data); err != nil {
					return "", nil, fmt.Errorf("read %s: %w", paths[i], err)
				}
				if _, err := output.Write(data); err != nil {
					return "", nil, fmt.Errorf("write AVB sparse image: %w", err)
				}
				left -= int64(len(data))
			}
			remaining -= chunkSize
		}
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	return output.Name(), nil, nil
}

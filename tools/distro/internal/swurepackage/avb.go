package swurepackage

import (
	"fmt"
	"os"
	"path/filepath"

	"proto-distro/tools/distro/internal/fileutil"
)

// 1 GiB in bytes: U-Boot sets rootfs_part_size to 0x200000 512-byte sectors in
// sources/meta-miner/recipes-bsp/u-boot/files/0002-add-default-boot-partition-layout-env-variables.patch.
const avbPartitionSize = int64(1073741824)

// ValidAVBGeometry checks placement only, not AVB content or authenticity.
// The final 512-byte sector of the 1 GiB partition holds the footer tail.
func ValidAVBGeometry(raw, body, tail uint64) bool {
	const tailOffset = uint64(avbPartitionSize - 512)
	return raw > 0 && body > 0 && raw%512 == 0 && body%512 == 0 &&
		tail == 512 && raw <= tailOffset && body <= tailOffset-raw
}

type imagePayload struct {
	name, hash string
	offset     int64
	compressed bool
}

func regularFile(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil, fmt.Errorf("%s must be a nonempty regular file", path)
	}
	return info, nil
}

// inspectAVBMetadata checks only file geometry, not AVB content or authenticity.
// Callers supply matching raw, body, and tail files from a trusted signer.
func inspectAVBMetadata(rootfs, body, tail, outputRoot string) ([]imagePayload, error) {
	raw, err := regularFile(rootfs)
	if err != nil {
		return nil, err
	}
	bodyInfo, err := regularFile(body)
	if err != nil {
		return nil, err
	}
	tailInfo, err := regularFile(tail)
	if err != nil {
		return nil, err
	}
	if !ValidAVBGeometry(uint64(raw.Size()), uint64(bodyInfo.Size()), uint64(tailInfo.Size())) {
		return nil, fmt.Errorf("invalid AVB file geometry: require sector-aligned raw/body, 512-byte tail, and no overlap in the 1 GiB partition")
	}
	root := filepath.Base(rootfs)
	for i, src := range []string{body, tail} {
		if filepath.Base(src) != root+[]string{".avb.body", ".avb.tail"}[i] {
			return nil, fmt.Errorf("AVB metadata must match rootfs basename: %s", src)
		}
	}
	return []imagePayload{
		{name: outputRoot + ".avb.body", offset: raw.Size()},
		{name: outputRoot + ".avb.tail", offset: avbPartitionSize - 512},
	}, nil
}

func copyAVBMetadata(rootfs, body, tail, dir, outputRoot string) ([]imagePayload, error) {
	payloads, err := inspectAVBMetadata(rootfs, body, tail, outputRoot)
	if err != nil {
		return nil, err
	}
	for i, src := range []string{body, tail} {
		dest := filepath.Join(dir, payloads[i].name)
		if err := copyPayload(src, dest); err != nil {
			return nil, err
		}
		payloads[i].hash, err = fileutil.SHA256File(dest)
		if err != nil {
			return nil, err
		}
	}
	return payloads, nil
}

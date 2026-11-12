package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"proto-distro/tools/flashimage"
)

var (
	rootfsRegex        = regexp.MustCompile(`.*miner-image-.*\.rootfs\.ext4$`)
	avbBodyRegex       = regexp.MustCompile(`.*miner-image-.*\.rootfs\.ext4\.avb\.body$`)
	avbTailRegex       = regexp.MustCompile(`.*miner-image-.*\.rootfs\.ext4\.avb\.tail$`)
	bootfsRegex        = regexp.MustCompile(`.*miner-.*\.bootfs\.ext4$`)
	datafsRegex        = regexp.MustCompile(`.*miner-.*\.datafs\.ext4$`)
	tfaUSBRegex        = regexp.MustCompile(`.*arm-trusted-firmware[/\\].*-usb\.stm32$`)
	tfaUSBSignedRegex  = regexp.MustCompile(`.*arm-trusted-firmware[/\\].*-usb_Signed\.stm32$`)
	tfaEMMCRegex       = regexp.MustCompile(`.*arm-trusted-firmware[/\\].*-emmc\.stm32$`)
	tfaEMMCSignedRegex = regexp.MustCompile(`.*arm-trusted-firmware[/\\].*-emmc_Signed\.stm32$`)
	tfaSDRegex         = regexp.MustCompile(`.*arm-trusted-firmware[/\\].*-sdcard\.stm32$`)
	tfaMetadataRegex   = regexp.MustCompile(`.*arm-trusted-firmware[/\\]metadata\.bin$`)
	fipOpteeRegex      = regexp.MustCompile(`.*fip[/\\]fip-.*-optee.*\.bin$`)
)

// isInDebugDir checks if the path contains a debug directory under arm-trusted-firmware.
func isInDebugDir(path string) bool {
	normalized := filepath.ToSlash(path)
	return strings.Contains(normalized, "arm-trusted-firmware/debug/")
}

func getImageFiles(profile flashimage.Profile, imageDir string, required []string, mmcDev int, signed bool) (map[string]string, error) {
	if mmcDev != 0 && mmcDev != 1 {
		return nil, fmt.Errorf("unsupported MMC device %d: expected 0 or 1", mmcDev)
	}
	patterns := map[string]*regexp.Regexp{
		"tfa_usb":      tfaUSBRegex,
		"tfa":          tfaEMMCRegex,
		"tfa_metadata": tfaMetadataRegex,
		"fip_optee":    fipOpteeRegex,
		"rootfs_a":     rootfsRegex,
		"avb_body":     avbBodyRegex,
		"avb_tail":     avbTailRegex,
		"bootfs":       bootfsRegex,
		"datafs":       datafsRegex,
	}
	if signed {
		patterns["tfa_usb"] = tfaUSBSignedRegex
		patterns["tfa"] = tfaEMMCSignedRegex
	}
	// SD artifacts use the existing sdcard selection, including for signed images.
	if mmcDev == 0 {
		patterns["tfa"] = tfaSDRegex
	}
	canonical := func(key string) string {
		for i := range profile.PartitionCount() {
			row, _ := profile.Partition(i)
			if row.ArtifactKey == key {
				return row.SourceArtifactKey
			}
		}
		return key
	}
	var keys []string
	matches := make(map[string][]string)
	var problems []error
	for _, requested := range required {
		key := canonical(requested)
		if _, ok := patterns[key]; !ok {
			problems = append(problems, fmt.Errorf("unknown artifact key %q", requested))
			continue
		}
		if _, ok := matches[key]; !ok {
			keys = append(keys, key)
			matches[key] = nil
		}
	}
	if len(problems) != 0 {
		return nil, errors.Join(problems...)
	}
	if imageDir == "" {
		return nil, fmt.Errorf("image directory is empty")
	}
	absDir, err := filepath.Abs(imageDir)
	if err != nil {
		return nil, err
	}
	absDir, err = filepath.EvalSymlinks(absDir)
	if err != nil {
		return nil, fmt.Errorf("resolve image directory: %w", err)
	}
	info, err := os.Stat(absDir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("image input %q is not a directory", imageDir)
	}
	logDebug("Debug: Searching in directory: %s", absDir)
	err = filepath.WalkDir(absDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == absDir {
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		for _, key := range keys {
			if !patterns[key].MatchString(path) || (strings.HasPrefix(key, "tfa") && isInDebugDir(path)) {
				continue
			}
			// Follow file symlinks used by image bundles, but not directory symlinks.
			info, err := os.Stat(path)
			if err != nil {
				return fmt.Errorf("inspect artifact %q: %w", path, err)
			}
			if info.Mode().IsRegular() {
				matches[key] = append(matches[key], path)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover image artifacts: %w", err)
	}
	files := make(map[string]string, len(required))
	for _, key := range required {
		if _, seen := files[key]; seen {
			continue
		}
		candidates := matches[canonical(key)]
		switch len(candidates) {
		case 0:
			problems = append(problems, fmt.Errorf("missing required artifact %q (pattern %s)", key, patterns[canonical(key)]))
		case 1:
			files[key] = candidates[0]
		default:
			problems = append(problems, fmt.Errorf("ambiguous required artifact %q: %s", key, strings.Join(candidates, ", ")))
		}
	}
	if len(problems) != 0 {
		return nil, errors.Join(problems...)
	}
	for i := range profile.PartitionCount() {
		row, _ := profile.Partition(i)
		if rootfs := files[row.ArtifactKey]; row.AVB && rootfs != "" {
			for _, suffix := range []string{"body", "tail"} {
				if path := files["avb_"+suffix]; path != "" && path != rootfs+".avb."+suffix {
					return nil, fmt.Errorf("%s requires its same-name .avb.%s, found %s", rootfs, suffix, path)
				}
			}
		}
	}
	return files, nil
}

// prepareImage owns only the temporary directory it creates for a ZIP input.
func prepareImage(path string) (dir string, cleanup func(), err error) {
	cleanup = func() {}
	if path == "" {
		return "", cleanup, fmt.Errorf("image path is empty")
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", cleanup, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", cleanup, fmt.Errorf("inspect image input: %w", err)
	}
	if info.IsDir() {
		return path, cleanup, nil
	}
	if !info.Mode().IsRegular() || !strings.EqualFold(filepath.Ext(path), ".zip") {
		return "", cleanup, fmt.Errorf("image input must be a directory or ZIP file: %q", path)
	}
	dir, err = os.MkdirTemp("", "cb-st-flasher-")
	if err != nil {
		return "", cleanup, err
	}
	ownedDir := dir
	cleanup = func() {
		if removeErr := os.RemoveAll(ownedDir); removeErr != nil {
			logError("Failed to remove temporary image directory %s: %v", ownedDir, removeErr)
		}
	}
	if err := flashimage.ExtractZIP(path, dir, nil); err != nil {
		cleanup()
		return "", cleanup, err
	}
	return dir, cleanup, nil
}

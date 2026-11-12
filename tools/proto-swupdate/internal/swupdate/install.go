package swupdate

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"proto-distro/tools/proto-swupdate/internal/config"
	"proto-distro/tools/proto-swupdate/internal/swu"
	"proto-distro/tools/proto-swupdate/internal/uboot"
)

const (
	fipNamePattern    = "fip-*.bin"
	rootfsNamePattern = "*.rootfs.ext4.gz"
)

// ErrRootfsInstall marks an Install error from the rootfs swupdate run.
var ErrRootfsInstall = errors.New("rootfs install")

// HasRootfs reports whether the SWU contains a rootfs image.
func HasRootfs(imagePath string) (bool, error) {
	return swu.Has(imagePath, rootfsNamePattern)
}

type InstallOptions struct {
	Rootfs bool
	FIP    bool
}

// InspectComponents finds selected payloads and rejects missing explicit requests.
// Unselected components are not inspected. Call this before boot-target writes.
func InspectComponents(imagePath string, options InstallOptions) (bool, bool, error) {
	rootfs := options.Rootfs || !options.FIP
	fip := options.FIP || !options.Rootfs
	hasRootfs, hasFIP := false, false
	var err error
	if rootfs {
		hasRootfs, err = HasRootfs(imagePath)
		if err != nil {
			return false, false, fmt.Errorf("inspect SWU for rootfs: %w", err)
		}
		if options.Rootfs && !hasRootfs {
			return false, false, fmt.Errorf("SWU has no requested rootfs image")
		}
	}
	if fip {
		hasFIP, err = swu.Has(imagePath, fipNamePattern)
		if err != nil {
			return false, false, fmt.Errorf("inspect SWU for fip: %w", err)
		}
		if options.FIP && !hasFIP {
			return false, false, fmt.Errorf("SWU has no requested FIP image")
		}
	}
	return hasRootfs, hasFIP, nil
}

// Install selects requested components. Explicit payloads are checked before writes.
func Install(cfg config.Config, imagePath string, options InstallOptions) error {
	hasRootfs, hasFIP, err := InspectComponents(imagePath, options)
	if err != nil {
		return err
	}
	hwVersion, err := readHWVersion(cfg.OSReleaseFile)
	if err != nil {
		return fmt.Errorf("read hw version: %w", err)
	}
	rootfsSel := ""
	if hasRootfs {
		rootfsSel, err = detectRootfsSelection(cfg.RootfsAEnvVar)
		if err != nil {
			return fmt.Errorf("detect rootfs: %w", err)
		}
		log.Printf("rootfs selection: %s", rootfsSel)
	}
	// Always target the bank opposite this boot, not metadata.active_index.
	var bootBank uint32
	fipSel := ""
	if hasFIP {
		var tampErr error
		bootBank, _, tampErr = uboot.ReadFWUBootInfo(cfg.TAMPFWUInfoAddr)
		if tampErr == nil {
			fipSel, err = fipSelectionForBank(bootBank)
			if err != nil {
				return err
			}
			log.Printf("FIP selection: %s", fipSel)
		} else if options.FIP {
			return fmt.Errorf("read FIP TAMP: %w", tampErr)
		} else {
			log.Printf("FIP TAMP read failed (%v), skipping FIP selection", tampErr)
		}
	}
	// Native SWUpdate honors only the last -e, so invoke once per component.
	hwArg := fmt.Sprintf("%s:%s", hwVersion, cfg.HWCompatibility)
	if hasRootfs {
		if err := runSWUpdate(cfg, hwArg, rootfsSel, imagePath); err != nil {
			return fmt.Errorf("%w (%s): %w", ErrRootfsInstall, rootfsSel, err)
		}
	}
	if !hasFIP || fipSel == "" {
		return nil
	}
	if err := VerifyFIP(imagePath, bootBank); err == nil {
		log.Printf("FIP already matches current bank %d, skipping fip install", bootBank)
		return nil
	} else {
		log.Printf("current bank %d differs from SWU fip (%v), installing", bootBank, err)
	}
	if err := WriteFIPInstallState(FIPInstallState{SWUPath: imagePath}); err != nil {
		return fmt.Errorf("stash fip install state: %w", err)
	}
	if err := runSWUpdate(cfg, hwArg, fipSel, imagePath); err != nil {
		return fmt.Errorf("fip install (%s): %w", fipSel, err)
	}
	return nil
}

func runSWUpdate(cfg config.Config, hwArg, selection, imagePath string) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve running proto-swupdate: %w", err)
	}
	childPath := filepath.Dir(executable)
	if inheritedPath := os.Getenv("PATH"); inheritedPath != "" {
		childPath += string(os.PathListSeparator) + inheritedPath
	}

	args := swupdateArgs(cfg, hwArg, selection, imagePath)
	log.Printf("swupdate args: %v", args)

	cmd := exec.Command(cfg.SWUpdateBinary, args...)
	// Modern proto-swupdate owns the FIP pass; never invoke legacy bootstrap.
	cmd.Env = append(cmd.Environ(), "PATH="+childPath, "PROTO_SWUPDATE_SKIP_FIP=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func swupdateArgs(cfg config.Config, hwArg, selection, imagePath string) []string {
	args := []string{
		"-H", hwArg,
		"-e", selection,
		"-f", cfg.SWUpdateCfg,
		"-k", cfg.SWUpdateKey,
		"-i", imagePath,
	}
	if strings.HasPrefix(selection, "fip,") {
		args = append(args, "-M", "-m", "-P", "", "-p", "")
	}
	return args
}

func readHWVersion(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || k != "MACHINE" {
			continue
		}
		v = strings.Trim(v, `"'`)
		if v == "" {
			return "", fmt.Errorf("empty MACHINE in %s", path)
		}
		return v, nil
	}
	return "", fmt.Errorf("MACHINE not found in %s", path)
}

func detectRootfsSelection(rootfsAVar string) (string, error) {
	active, err := uboot.ActivePartition()
	if err != nil {
		return "", err
	}

	rootfsA, err := uboot.GetEnv(rootfsAVar)
	if err != nil {
		return "", err
	}

	if active == rootfsA {
		return "stable,copy2", nil
	}
	return "stable,copy1", nil
}

func fipSelectionForBank(bootBank uint32) (string, error) {
	if bootBank > 1 {
		return "", fmt.Errorf("TAMP boot_bank out of range: %d", bootBank)
	}
	if bootBank == 0 {
		return "fip,bank_1", nil
	}
	return "fip,bank_0", nil
}

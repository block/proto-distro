package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"github.com/urfave/cli/v2"

	"proto-distro/tools/proto-swupdate/internal/config"
	"proto-distro/tools/proto-swupdate/internal/fwumeta"
	"proto-distro/tools/proto-swupdate/internal/swupdate"
	"proto-distro/tools/proto-swupdate/internal/uboot"
)

var (
	Version   = "dev"
	GitCommit = "unknown"
)

// resolveDev turns a bare partition label into /dev/disk/by-partlabel/<name>.
// Absolute paths are returned unchanged.
func resolveDev(s string) string {
	if filepath.IsAbs(s) {
		return s
	}
	return filepath.Join(swupdate.PartlabelDir, s)
}

func main() {
	// Send informational log output to stdout. SWUpdate treats anything a
	// pre/post-install script writes to stderr as an [ERROR] line, which
	// pollutes its summary even when our status messages are just info.
	log.SetOutput(os.Stdout)

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: load config: %v\n", err)
		os.Exit(1)
	}

	app := &cli.App{
		Name:     "proto-swupdate",
		Usage:    "SWUpdate orchestration and FIP/FWU metadata tool",
		Version:  Version,
		Metadata: map[string]interface{}{"git_commit": GitCommit},
		Commands: []*cli.Command{
			installCmd(&cfg),
			runningSlotCmd(uboot.ActivePartition),
			isRootfsUpdatePendingCmd(&cfg),
			confirmCmd(&cfg),
			bootcountCmd(&cfg),
			fipCmd(&cfg),
		},
	}

	if err := app.Run(os.Args); err != nil {
		os.Exit(reportRunError(os.Stderr, err))
	}
}

func reportRunError(w io.Writer, err error) int {
	var exitCoder cli.ExitCoder
	if errors.As(err, &exitCoder) {
		if err.Error() != "" {
			fmt.Fprintln(w, err)
		}
		return exitCoder.ExitCode()
	}

	fmt.Fprintf(w, "error: %v\n", err)
	return 1
}

func isRootfsUpdatePendingCmd(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "is-rootfs-update-pending",
		Usage: "Exit successfully only during an unconfirmed rootfs trial boot",
		Action: func(c *cli.Context) error {
			pending, err := rootfsUpdatePending(cfg, uboot.GetEnv)
			if err != nil {
				return err
			}
			if !pending {
				return cli.Exit("", 1)
			}
			return nil
		},
	}
}

func rootfsUpdatePending(cfg *config.Config, getEnv func(string) (string, error)) (bool, error) {
	ustate, err := getEnv(cfg.UstateVar)
	if err != nil {
		return false, fmt.Errorf("read U-Boot %s: %w", cfg.UstateVar, err)
	}
	return ustate == cfg.UstateUpdating, nil
}

// exitUpdatePending is the install exit code while the previous rootfs update
// still waits for boot confirmation (EX_TEMPFAIL: retry later).
const exitUpdatePending = 75

// installEnv holds the SWU, U-Boot and rootfs accessors install needs, so
// tests can replace them.
type installEnv struct {
	inspectSWU      func(string, swupdate.InstallOptions) (bool, bool, error)
	activePartition func() (string, error)
	getEnv          func(string) (string, error)
	setEnvs         func(map[string]string) error
	clearBootcount  func(uint32) error
}

var deviceEnv = installEnv{
	inspectSWU:      swupdate.InspectComponents,
	activePartition: uboot.ActivePartition,
	getEnv:          uboot.GetEnv,
	setEnvs:         uboot.SetEnvs,
	clearBootcount:  uboot.ClearBootcount,
}

func installCmd(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "install",
		Usage:     "Detect partitions, construct args, invoke swupdate",
		ArgsUsage: "<image.swu>",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "rootfs", Usage: "Install rootfs only (both components if also --fip)"},
			&cli.BoolFlag{Name: "fip", Usage: "Install FIP only (both components if also --rootfs)"},
		},
		Action: func(c *cli.Context) error {
			if c.NArg() != 1 {
				return fmt.Errorf("usage: proto-swupdate install <image.swu>")
			}
			options := swupdate.InstallOptions{Rootfs: c.Bool("rootfs"), FIP: c.Bool("fip")}
			return runInstall(*cfg, c.Args().Get(0), options, deviceEnv, swupdate.Install)
		},
	}
}

// runInstall checks requested payloads before resetting the boot target around
// a selected rootfs install. FIP-only installs leave pending rootfs updates alone.
func runInstall(cfg config.Config, imagePath string, options swupdate.InstallOptions, env installEnv, install func(config.Config, string, swupdate.InstallOptions) error) error {
	hasRootfs, _, err := env.inspectSWU(imagePath, options)
	if err != nil {
		return err
	}
	if !hasRootfs {
		return install(cfg, imagePath, options)
	}
	if err := resetBootTarget(cfg, env, true); err != nil {
		return err
	}
	err = install(cfg, imagePath, options)
	// SWUpdate leaves ustate=3 or 7 after a failed rootfs run. The boot script
	// switches slots on 3, which would boot the partly written slot.
	if errors.Is(err, swupdate.ErrRootfsInstall) {
		if resetErr := resetBootTarget(cfg, env, false); resetErr != nil {
			return errors.Join(err, fmt.Errorf("after failed rootfs install: %w", resetErr))
		}
	}
	return err
}

// resetBootTarget makes U-Boot boot the running slot: rootpart=<running>,
// ustate=idle. Before swupdate starts, this stops an interrupted install
// (ustate=7, no boot rule) from booting the slot that is being written, which
// a second install before reboot would otherwise do. With refusePending set,
// it refuses during an unconfirmed trial boot instead, because the inactive
// slot is then the only known-good fallback.
func resetBootTarget(cfg config.Config, env installEnv, refusePending bool) error {
	running, err := env.activePartition()
	if err != nil {
		return fmt.Errorf("detect running rootfs: %w", err)
	}
	vals := map[string]string{}
	for _, name := range []string{cfg.RootfsAEnvVar, cfg.RootfsBEnvVar, "rootpart", cfg.UstateVar} {
		if vals[name], err = env.getEnv(name); err != nil {
			return fmt.Errorf("read U-Boot %s: %w", name, err)
		}
	}
	if running != vals[cfg.RootfsAEnvVar] && running != vals[cfg.RootfsBEnvVar] {
		return fmt.Errorf("running rootfs partition %s is neither %s=%s nor %s=%s", running,
			cfg.RootfsAEnvVar, vals[cfg.RootfsAEnvVar], cfg.RootfsBEnvVar, vals[cfg.RootfsBEnvVar])
	}
	rootpart, ustate := vals["rootpart"], vals[cfg.UstateVar]
	if refusePending && ustate == cfg.UstateUpdating && rootpart == running {
		return cli.Exit(fmt.Sprintf("previous update is waiting for boot confirmation (%s=%s, rootpart=%s); retry after it is confirmed",
			cfg.UstateVar, ustate, rootpart), exitUpdatePending)
	}

	changes := map[string]string{}
	if rootpart != running {
		changes["rootpart"] = running
	}
	if ustate != cfg.UstateIdle {
		changes[cfg.UstateVar] = cfg.UstateIdle
	}
	if len(changes) > 0 {
		if err := env.setEnvs(changes); err != nil {
			return fmt.Errorf("reset boot target: %w", err)
		}
		log.Printf("reset boot target to running slot: rootpart %q -> %q, %s %q -> %q",
			rootpart, running, cfg.UstateVar, ustate, cfg.UstateIdle)
	}
	if err := env.clearBootcount(cfg.BootcountAddr); err != nil {
		log.Printf("WARNING: clear bootcount: %v", err)
	}
	return nil
}

// bootcount is read-only; confirmation remains responsible for clearing it.
func bootcountCmd(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "bootcount",
		Usage: "Print the current rootfs bootcount as a decimal value without clearing it",
		Action: func(c *cli.Context) error {
			if c.NArg() != 0 {
				return fmt.Errorf("usage: proto-swupdate bootcount")
			}
			return printBootcount(cfg, c.App.Writer, uboot.ReadDevmem)
		},
	}
}

func printBootcount(cfg *config.Config, output io.Writer, read func(uint32) (uint32, error)) error {
	count, err := read(cfg.BootcountAddr)
	if err != nil {
		return fmt.Errorf("read bootcount: %w", err)
	}
	_, err = fmt.Fprintln(output, count)
	return err
}

func confirmCmd(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "confirm",
		Usage: "Post-reboot: confirm rootfs and FIP updates",
		Action: func(c *cli.Context) error {
			return runConfirm(cfg)
		},
	}
}

func runConfirm(cfg *config.Config) error {
	// FIP confirmation (must not block rootfs confirmation)
	if err := confirmFIP(cfg); err != nil {
		log.Printf("WARNING: FIP confirmation failed: %v", err)
	}

	// Rootfs confirmation
	return confirmRootfs(cfg)
}

// confirmFIP records the FIP boot state. No service health check — if this
// runs, Linux booted far enough for systemd to fire update-confirm.service,
// which is the "boot OK" signal we care about. With both banks marked
// accepted=1 by `fip post-install`, there is no metadata flip to do here;
// we just log state for debuggability. Service-level health and any revert
// decision are the responsibility of a separate caller (miner-api or an
// external watchdog).
func confirmFIP(cfg *config.Config) error {
	_, meta := fwumeta.FindValid(cfg.MetadataDevices)
	if meta == nil {
		log.Printf("no valid FWU metadata found, skipping FIP confirmation")
		return nil
	}

	bootBank, trialCount, err := uboot.ReadFWUBootInfo(cfg.TAMPFWUInfoAddr)
	if err != nil {
		log.Printf("FIP state: metadata active=%d previous=%d (TAMP read failed: %v)",
			meta.ActiveIndex, meta.PreviousActiveIndex, err)
		return nil
	}
	log.Printf("FIP state: metadata active=%d previous=%d boot_bank=%d trial_count=%d",
		meta.ActiveIndex, meta.PreviousActiveIndex, bootBank, trialCount)
	if bootBank != meta.ActiveIndex {
		log.Printf("NOTE: TAMP boot_bank=%d but metadata.active=%d — TF-A may have picked previous",
			bootBank, meta.ActiveIndex)
	}
	return nil
}

// confirmRootfs records rootfs boot success. No service health check —
// reaching this point means the new rootfs booted far enough for systemd
// to run update-confirm.service. Clear ustate (if pending) and bootcount.
func confirmRootfs(cfg *config.Config) error {
	ustate, err := uboot.GetEnv(cfg.UstateVar)
	if err != nil {
		ustate = ""
	}
	log.Printf("ustate=%s", ustate)

	switch ustate {
	case cfg.UstateUpdating:
		log.Printf("rootfs update pending, marking confirmed")
		if err := uboot.SetEnvs(map[string]string{cfg.UstateVar: cfg.UstateIdle}); err != nil {
			return fmt.Errorf("fw_setenv %s=%s: %w", cfg.UstateVar, cfg.UstateIdle, err)
		}
	case "3":
		log.Printf("rootfs was reverted, clearing ustate")
		_ = uboot.SetEnvs(map[string]string{cfg.UstateVar: cfg.UstateIdle})
	default:
		log.Printf("no rootfs update pending")
	}

	if err := uboot.ClearBootcount(cfg.BootcountAddr); err != nil {
		log.Printf("clear bootcount: %v", err)
	}
	return nil
}

// --- fip subcommands ---

func fipCmd(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "fip",
		Usage: "FIP partition and FWU metadata operations",
		Subcommands: []*cli.Command{
			{
				Name:      "read-metadata",
				Usage:     "Dump FWU metadata from device",
				ArgsUsage: "<dev>",
				Action:    metaRead,
			},
			{
				Name: "post-install",
				Usage: "Point FWU metadata at the FIP bank opposite of the one " +
					"currently booted (inferred from TAMP). Idempotent. " +
					"Invoked by fip-post-install.sh.",
				Action: fipPostInstallAction(cfg),
			},
			{
				Name:      "verify",
				Usage:     "Verify the FIP in <swu> matches the content of bank <bank>",
				ArgsUsage: "<swu> <bank>",
				Action:    fipVerifyAction,
			},
			{
				Name:   "tamp",
				Usage:  "Read TAMP_BKP10R and decode FWU boot info",
				Action: tampReadAction(cfg),
			},
		},
	}
}

func tampReadAction(cfg *config.Config) cli.ActionFunc {
	return func(c *cli.Context) error {
		raw, err := uboot.ReadDevmem(cfg.TAMPFWUInfoAddr)
		if err != nil {
			return err
		}
		bootBank := raw & 0xF
		trialCount := (raw >> 4) & 0xF
		fmt.Printf("addr: 0x%08x\n", cfg.TAMPFWUInfoAddr)
		fmt.Printf("raw: 0x%08x\n", raw)
		fmt.Printf("boot_bank: %d\n", bootBank)
		fmt.Printf("trial_count: %d\n", trialCount)
		return nil
	}
}

func metaRead(c *cli.Context) error {
	if c.NArg() != 1 {
		return fmt.Errorf("usage: proto-swupdate fip read-metadata <dev>")
	}
	dev := resolveDev(c.Args().Get(0))
	m, err := fwumeta.ReadFromDevice(dev)
	if err != nil {
		return err
	}
	fmt.Printf("device: %s\n", dev)
	fmt.Printf("crc32: 0x%08x\n", m.CRC32)
	fmt.Printf("version: %d\n", m.Version)
	fmt.Printf("active_index: %d\n", m.ActiveIndex)
	fmt.Printf("previous_active_index: %d\n", m.PreviousActiveIndex)
	for i := 0; i < fwumeta.NumBanks; i++ {
		fmt.Printf("bank[%d].img_uuid: %s\n", i, fwumeta.FormatUUID(m.ImgEntry[0].Banks[i].ImgUUID))
		fmt.Printf("bank[%d].accepted: %d\n", i, m.ImgEntry[0].Banks[i].Accepted)
	}
	return nil
}

// fipPostInstallAction is the sole post-install hook for FIP SWU updates.
// It's idempotent — re-running before reboot leaves the same end state.
//
// TAMP_BKP10R is unreliable across soft reboot on this HW, so the scheme is
// "commit-on-install" rather than "trial-then-accept":
//
//  1. Read TAMP_BKP10R → boot_bank (the bank TF-A actually loaded THIS boot).
//  2. target = 1 - boot_bank (the partition SWUpdate's raw handler just wrote).
//  3. Verify the just-written target partition: sha256(target[0:expected.size])
//     must match the sha256 of the fip.bin embedded in the SWU. The SWU path
//     is stashed to swupdate.FIPInstallStatePath (/run/proto-swupdate/fip-install)
//     by `proto-swupdate install` before invoking swupdate.
//  4. If the check passes, rewrite metadata:
//     active_index=target, previous_active_index=boot_bank,
//     banks[0].accepted = banks[1].accepted = 1 (both committed — no trial).
//  5. Write the new metadata to BOTH copies.
//
// If the sha check fails, metadata is NOT flipped — the device stays on the
// old bank. The install is marked failed; no brick.
func fipPostInstallAction(cfg *config.Config) cli.ActionFunc {
	return func(c *cli.Context) error {
		bootBank, trialCount, err := uboot.ReadFWUBootInfo(cfg.TAMPFWUInfoAddr)
		if err != nil {
			return fmt.Errorf("read TAMP: %w", err)
		}
		if bootBank >= fwumeta.NumBanks {
			return fmt.Errorf("TAMP boot_bank out of range: %d", bootBank)
		}
		target := uint32(1) - bootBank
		log.Printf("post-install: TAMP boot_bank=%d trial_count=%d → target bank=%d",
			bootBank, trialCount, target)

		st, err := swupdate.ReadFIPInstallState()
		if err != nil {
			return fmt.Errorf("read fip install state: %w (did `proto-swupdate install` run first?)", err)
		}
		if err := swupdate.VerifyFIP(st.SWUPath, target); err != nil {
			return fmt.Errorf("verify target: %w", err)
		}
		log.Printf("post-install: target bank %d matches SWU fip", target)

		srcDev, meta := fwumeta.FindValid(cfg.MetadataDevices)
		if meta == nil {
			return fmt.Errorf("no valid metadata copy found on %v", cfg.MetadataDevices)
		}
		log.Printf("post-install: using %s as metadata template", srcDev)

		if meta.ActiveIndex != bootBank {
			log.Printf("post-install: WARNING: metadata.active_index=%d but TAMP boot_bank=%d",
				meta.ActiveIndex, bootBank)
		}

		meta.ActiveIndex = target
		meta.PreviousActiveIndex = bootBank
		// TAMP-less trial doesn't work on this HW: if we leave accepted=0 on
		// the new bank, TF-A's fwu_is_trial_run_state() returns true and
		// plat_fwu_get_boot_idx() falls through to previous_active_index
		// because stm32_get_and_dec_fwu_trial_boot_cnt() always reads 0.
		// Mark both banks accepted so TF-A just uses active_index. Revert
		// is handled entirely in `proto-swupdate confirm`.
		meta.ImgEntry[0].Banks[0].Accepted = 1
		meta.ImgEntry[0].Banks[1].Accepted = 1

		for _, dev := range cfg.MetadataDevices {
			if err := fwumeta.WriteToDevice(dev, meta); err != nil {
				return fmt.Errorf("write %s: %w", dev, err)
			}
			log.Printf("post-install: wrote metadata to %s (active=%d previous=%d accepted=[1,1])",
				dev, meta.ActiveIndex, meta.PreviousActiveIndex)
		}
		return nil
	}
}

func fipVerifyAction(c *cli.Context) error {
	if c.NArg() != 2 {
		return fmt.Errorf("usage: proto-swupdate fip verify <swu> <bank>")
	}
	bank, err := parseBank(c.Args().Get(1))
	if err != nil {
		return err
	}
	swuPath := c.Args().Get(0)
	if err := swupdate.VerifyFIP(swuPath, bank); err != nil {
		return err
	}
	fmt.Printf("fip in %s matches bank %d\n", swuPath, bank)
	return nil
}

func parseBank(s string) (uint32, error) {
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil || v >= fwumeta.NumBanks {
		return 0, fmt.Errorf("invalid bank index %q (must be 0 or 1)", s)
	}
	return uint32(v), nil
}

// runningSlotCmd reports the mounted root partition, not rootpart (the next
// boot target). Consumers can compare it across a reboot to verify activation.
func runningSlotCmd(activePartition func() (string, error)) *cli.Command {
	return &cli.Command{
		Name:  "running-slot",
		Usage: "Print the mounted root partition number without changing boot state",
		Action: func(c *cli.Context) error {
			if c.NArg() != 0 {
				return fmt.Errorf("usage: proto-swupdate running-slot")
			}
			slot, err := activePartition()
			if err != nil {
				return fmt.Errorf("detect running root slot: %w", err)
			}
			_, err = fmt.Fprintln(c.App.Writer, slot)
			return err
		},
	}
}

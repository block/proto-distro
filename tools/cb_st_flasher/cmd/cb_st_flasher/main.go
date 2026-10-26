package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v2"
	"proto-distro/tools/cb_st_flasher/internal/version"
	"proto-distro/tools/flashimage"
)

var debugMode bool

const (
	colorWhite = "\033[37m"
	colorCyan  = "\033[36m"
	colorRed   = "\033[31m"
	colorGreen = "\033[32m"
	colorReset = "\033[0m"
)

func partitionChoices(profile flashimage.Profile) []string {
	choices := make([]string, 0, profile.PartitionCount()+1)
	for i := range profile.PartitionCount() {
		row, _ := profile.Partition(i)
		if row.Alias != "" {
			choices = append(choices, row.Alias)
		}
		choices = append(choices, row.Name)
	}
	return choices
}

type flashMode string

const (
	flashModeFull       flashMode = "full"
	flashModeRootfsOnly flashMode = "rootfs-only"
	flashModeNoRootfs   flashMode = "no-rootfs"
)

func logDebug(format string, args ...interface{}) {
	if debugMode {
		fmt.Printf(colorWhite+format+colorReset+"\n", args...)
	}
}

func logInfo(format string, args ...interface{}) {
	fmt.Printf(colorCyan+format+colorReset+"\n", args...)
}

func logSuccess(format string, args ...interface{}) {
	fmt.Printf(colorGreen+format+colorReset+"\n", args...)
}

func logError(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, colorRed+format+colorReset+"\n", args...)
}

func normalizePartitions(profile flashimage.Profile, partitions []string) ([]string, error) {
	normalized := make([]string, 0, len(partitions))
	seen := make(map[string]struct{}, len(partitions))

	for _, partition := range partitions {
		row, ok := profile.Resolve(partition)
		if !ok {
			return nil, fmt.Errorf("invalid partition %q", partition)
		}
		partition = row.Name

		if _, ok := seen[partition]; ok {
			continue
		}

		seen[partition] = struct{}{}
		normalized = append(normalized, partition)
	}

	return normalized, nil
}

func main() {
	profile := flashimage.C3()
	ctx, stop := interruptContext()
	defer stop()
	app := &cli.App{
		Name:      "cb_st_flasher",
		Usage:     "Natively program and verify an STM32MP13 control board over USB",
		Version:   version.Version,
		ArgsUsage: "<image-directory-or-zip>",
		Commands: []*cli.Command{
			{
				Name:  "devices",
				Usage: "List STM32 DFU devices using read-only USB descriptor discovery",
				Action: func(c *cli.Context) error {
					if c.Args().Len() != 0 {
						return fmt.Errorf("devices takes no arguments")
					}
					return listDevices(c.Context, c.App.Writer)
				},
			},
		},
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "debug", Usage: "Enable debug logging"},
			&cli.IntFlag{Name: "mmc-dev", Value: 1, Usage: "MMC device (SD = 0, eMMC = 1)"},
			&cli.StringSliceFlag{Name: "partition", Aliases: []string{"p"}, Usage: "Select a partition; repeat to add more (" + strings.Join(partitionChoices(profile), ", ") + ")"},
			&cli.BoolFlag{Name: "erase-all", Usage: "Erase MMC boot areas and user area"},
			&cli.BoolFlag{Name: "rootfs-only", Aliases: []string{"rootfs"}, Usage: "Write rootfs_a and erase u-boot-env"},
			&cli.BoolFlag{Name: "no-rootfs", Usage: "Write fsbl1/fsbl2, metadata1/metadata2, fip-a and bootfs"},
			&cli.BoolFlag{Name: "signed", Value: true, Usage: "Select signed bootloader artifacts (use --signed=false to disable)", DefaultText: "true"},
			&cli.BoolFlag{Name: "verify", Usage: "Enable read-back comparison of written bytes (slower; disabled by default)"},
			&cli.BoolFlag{Name: "verity", Value: true, Usage: "Flash plain ext4 with automatically discovered .avb.body and .avb.tail files together; omit unused partition padding (not erase; use --no-verity to disable)", DefaultText: "true"},
			&cli.BoolFlag{Name: "no-verity", Usage: "Flash plain ext4 without AVB body and tail files"},
		},
		Action: func(c *cli.Context) error {
			debugMode = c.Bool("debug")
			if c.Args().Len() != 1 {
				return fmt.Errorf("exactly one image directory or ZIP argument is required")
			}
			if c.Bool("rootfs-only") && c.Bool("no-rootfs") {
				return fmt.Errorf("--rootfs-only and --no-rootfs cannot be used together")
			}
			if c.IsSet("verity") && c.Bool("verity") && c.Bool("no-verity") {
				return fmt.Errorf("--verity and --no-verity cannot be used together")
			}
			partitions, err := normalizePartitions(profile, c.StringSlice("partition"))
			if err != nil {
				return err
			}
			mode := flashModeFull
			if c.Bool("rootfs-only") {
				mode = flashModeRootfsOnly
			} else if c.Bool("no-rootfs") {
				mode = flashModeNoRootfs
			}
			return flashImage(c.Context, profile, c.Args().First(), flashOptions{
				mmcDev: c.Int("mmc-dev"), mode: mode, partitions: partitions,
				eraseAll: c.Bool("erase-all"), signed: c.Bool("signed"),
				verity:     c.Bool("verity") && !c.Bool("no-verity"),
				skipVerify: !c.Bool("verify"),
			})
		},
	}
	if err := app.RunContext(ctx, os.Args); err != nil {
		logError("%v", err)
		os.Exit(1)
	}
}

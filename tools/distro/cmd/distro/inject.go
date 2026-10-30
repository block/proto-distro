package main

import (
	"fmt"

	"github.com/urfave/cli/v2"
	"proto-distro/tools/distro/internal/config"
	"proto-distro/tools/distro/internal/distroinject"
)

func injectCommand() *cli.Command {
	return &cli.Command{
		Name:      "inject",
		Usage:     "Inject packages, optionally set VERSION, and regenerate release AVB in a distro zip/folder",
		ArgsUsage: "INPUT_PATH [IPK_FILES_OR_DIRS...]",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "output-dir",
				Aliases: []string{"o"},
				Usage:   "Output parent directory (default: modify input in place)",
			},
			&cli.StringFlag{
				Name:  "version",
				Usage: "optional VERSION value to write to /usr/lib/os-release inside the rootfs",
			},
			&cli.StringFlag{
				Name:  "swu-key-file",
				Usage: "path to RSA private key file for SWU signing (default: from config)",
			},
			&cli.StringFlag{
				Name:  "swu-key-pass",
				Usage: "path to key passphrase file for SWU signing (default: from config)",
			},
			&cli.BoolFlag{
				Name:    "verbose",
				Aliases: []string{"v"},
				Usage:   "verbose output",
			},
		},
		Action: func(c *cli.Context) error {
			if c.Args().Len() < 1 {
				return fmt.Errorf("INPUT_PATH argument required")
			}

			cfg, err := config.Load(c.String("config"))
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			args := c.Args().Slice()
			output, err := distroinject.RunWithConfig(c.Context, distroinject.Params{
				Config:             cfg,
				ConfigPath:         c.String("config"),
				InputPath:          args[0],
				OutputDir:          c.String("output-dir"),
				IPKPaths:           args[1:],
				Version:            c.String("version"),
				PrivateKeyOverride: c.String("swu-key-file"),
				PassfileOverride:   c.String("swu-key-pass"),
				Verbose:            c.Bool("verbose"),
			})
			if err != nil {
				return err
			}

			fmt.Println(output)
			return nil
		},
	}
}

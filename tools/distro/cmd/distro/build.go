package main

import (
	"proto-distro/tools/distro/internal/builder"
	"github.com/urfave/cli/v2"
)

func buildCommand() *cli.Command {
	return &cli.Command{
		Name:  "build",
		Usage: "Build the distro image via Docker",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "machine",
				Aliases: []string{"m"},
				Usage:   "Target machine (default from config)",
			},
			&cli.StringFlag{
				Name:    "distro",
				Aliases: []string{"d"},
				Usage:   "Distro name (default from config)",
			},
			&cli.StringFlag{
				Name:    "variant",
				Aliases: []string{"t"},
				Usage:   "Image variant: release, mfg (default from config)",
			},
			&cli.StringFlag{
				Name:  "postbuild-inject",
				Usage: "Run package injection during post-build and set VERSION in os-release to the given value",
			},
		},
		Action: func(c *cli.Context) error {
			return builder.Run(c.Context, builder.Options{
				ConfigPath:    c.String("config"),
				Machine:       c.String("machine"),
				Distro:        c.String("distro"),
				Variant:       c.String("variant"),
				Command:       "build",
				InjectVersion: c.String("postbuild-inject"),
			})
		},
	}
}

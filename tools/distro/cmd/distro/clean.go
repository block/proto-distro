package main

import (
	"proto-distro/tools/distro/internal/builder"
	"github.com/urfave/cli/v2"
)

func cleanCommand() *cli.Command {
	return &cli.Command{
		Name:  "clean",
		Usage: "Clean build artifacts via bitbake",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "variant",
				Aliases: []string{"t"},
				Usage:   "Image variant to clean (default from config)",
			},
		},
		Action: func(c *cli.Context) error {
			return builder.Run(c.Context, builder.Options{
				ConfigPath: c.String("config"),
				Variant:    c.String("variant"),
				Command:    "clean",
			})
		},
	}
}

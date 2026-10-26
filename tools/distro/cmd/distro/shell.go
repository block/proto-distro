package main

import (
	"proto-distro/tools/distro/internal/builder"
	"github.com/urfave/cli/v2"
)

func shellCommand() *cli.Command {
	return &cli.Command{
		Name:  "shell",
		Usage: "Open an interactive shell in the build container with bitbake environment",
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
		},
		Action: func(c *cli.Context) error {
			return builder.Run(c.Context, builder.Options{
				ConfigPath: c.String("config"),
				Machine:    c.String("machine"),
				Distro:     c.String("distro"),
				Command:    "shell",
			})
		},
	}
}

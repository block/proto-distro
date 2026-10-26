package main

import (
	"proto-distro/tools/distro/internal/builder"
	"github.com/urfave/cli/v2"
)

func layersCommand() *cli.Command {
	return &cli.Command{
		Name:  "layers",
		Usage: "Show Yocto layers via bitbake-layers",
		Action: func(c *cli.Context) error {
			return builder.Run(c.Context, builder.Options{
				ConfigPath: c.String("config"),
				Command:    "layers",
			})
		},
	}
}

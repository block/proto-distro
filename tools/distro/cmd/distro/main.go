package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/urfave/cli/v2"
	"proto-distro/tools/distro/internal/color"
	"proto-distro/tools/distro/internal/version"
)

const defaultConfigPath = "distro.yaml"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop() // Re-enable default signal behavior so a second signal force-kills.
	}()

	configPath := configPathFromArgs()

	app := &cli.App{
		Name:    "distro",
		Usage:   "Distro image tools",
		Version: version.Version,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "config",
				Aliases: []string{"c"},
				Usage:   "Path to config file",
				Value:   defaultConfigPath,
			},
		},
		Commands: []*cli.Command{
			buildCommand(),
			shellCommand(),
			cleanCommand(),
			layersCommand(),
			injectCommand(),
			swuRecreateCommand(),
			fetchCommand(configPath),
			postbuildCommand(),
		},
	}

	if err := app.RunContext(ctx, os.Args); err != nil {
		fmt.Fprintln(os.Stderr, color.Errorf("error: %v", err))
		os.Exit(1)
	}
}

// configPathFromArgs extracts the --config/-c flag value from os.Args before
// CLI parsing, so commands that need config at construction time can use it.
func configPathFromArgs() string {
	fs := flag.NewFlagSet("pre-parse", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var configPath string
	fs.StringVar(&configPath, "config", defaultConfigPath, "")
	fs.StringVar(&configPath, "c", defaultConfigPath, "")
	_ = fs.Parse(os.Args[1:])
	return configPath
}

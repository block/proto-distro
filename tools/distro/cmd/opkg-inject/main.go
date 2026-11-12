package main

import (
	"flag"
	"fmt"
	"os"

	"proto-distro/tools/distro/internal/color"
	"proto-distro/tools/distro/internal/opkginject"
	"proto-distro/tools/distro/internal/version"
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")

	output := flag.String("o", "", "output ext4 image path (required)")
	flag.StringVar(output, "output", "", "output ext4 image path (required)")
	containerImage := flag.String("image", "opkg-injector", "Docker image name for the injector container")
	osReleaseVersion := flag.String("os-release-version", "", "optional VERSION value to write to /usr/lib/os-release inside the rootfs")
	verbose := flag.Bool("v", false, "verbose output")
	flag.BoolVar(verbose, "verbose", false, "verbose output")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: opkg-inject -o OUTPUT [--os-release-version VERSION] IMAGE [PACKAGES...]\n\n")
		fmt.Fprintf(os.Stderr, "Inject IPK packages into an ext4 rootfs image using Docker and optionally update /usr/lib/os-release VERSION.\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if *showVersion {
		fmt.Println(version.Version)
		return
	}

	if *output == "" {
		fmt.Fprintln(os.Stderr, "error: -o/--output is required")
		flag.Usage()
		os.Exit(1)
	}

	args := flag.Args()
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "error: IMAGE argument required")
		flag.Usage()
		os.Exit(1)
	}
	if len(args) < 2 && *osReleaseVersion == "" {
		fmt.Fprintln(os.Stderr, "error: at least one PACKAGE argument or --os-release-version is required")
		flag.Usage()
		os.Exit(1)
	}

	if _, err := opkginject.Run(opkginject.Options{
		ContainerImage: *containerImage,
		Image:          args[0],
		Output:         *output,
		Packages:       args[1:],
		Version:        *osReleaseVersion,
		Verbose:        *verbose,
	}); err != nil {
		fmt.Fprintln(os.Stderr, color.Errorf("error: %v", err))
		os.Exit(1)
	}
}

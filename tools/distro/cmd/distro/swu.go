package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v2"
	"proto-distro/tools/distro/internal/config"
	"proto-distro/tools/distro/internal/fileutil"
	"proto-distro/tools/distro/internal/swurepackage"
)

func swuRecreateCommand() *cli.Command {
	return &cli.Command{
		Name:  "swu-recreate",
		Usage: "Replace rootfs and/or FIP components present in a trusted seed SWU",
		Description: "Supply --rootfs with matching adjacent AVB body/tail when seed contains rootfs; supply --fip when seed contains FIP. " +
			"Preserves seed payload names, version, hardware, selections, bootstrap, scripts, and unrelated members. " +
			"Replacement inputs must match seed components. No AVB private key is needed; AVB authenticity is not proved. " +
			"Dry mode is strictly local and read-only; it needs cpio for listing, not Docker, gzip, or OpenSSL. " +
			"Preview checks selection and file geometry, not manifest rewriting or signing-credential usability. " +
			"Reports include signing-credential fingerprints; keep reports private.",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "swu", Required: true, Usage: "trusted seed SWU archive"},
			&cli.StringFlag{Name: "rootfs", Usage: "replacement plain ROOTFS.ext4 with adjacent AVB body/tail (required exactly when seed contains rootfs)"},
			&cli.StringFlag{Name: "fip", Usage: "replacement FIP binary (required exactly when seed contains FIP)"},
			&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Usage: "new output SWU (default: <seed>_recreated.swu)"},
			&cli.StringFlag{Name: "swu-key-file", Usage: "RSA private key file for SWU signing (default: from config)"},
			&cli.StringFlag{Name: "swu-key-pass", Usage: "key passphrase file for SWU signing (default: from config)"},
			&cli.BoolFlag{Name: "dry", Usage: "report local inputs, hashes, and outputs without writing files"},
		},
		Action: func(c *cli.Context) error {
			if c.IsSet("fip") && c.String("fip") == "" {
				return fmt.Errorf("--fip requires a nonempty file path")
			}
			if c.Args().Len() != 0 {
				return fmt.Errorf("positional arguments are unsupported; use --rootfs and --fip")
			}
			rootfs := c.String("rootfs")
			if c.IsSet("rootfs") && !strings.HasSuffix(rootfs, ".ext4") {
				return fmt.Errorf("--rootfs requires a nonempty plain ROOTFS.ext4 file path")
			}
			var err error
			if rootfs != "" {
				rootfs, err = filepath.Abs(rootfs)
				if err != nil {
					return err
				}
			}
			seed, err := fileutil.ResolveArtifact(c.String("swu"))
			if err != nil {
				return err
			}
			cfg, err := config.Load(c.String("config"))
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			keys := config.ResolveSigningKeys(c.String("config"), cfg.Signing, c.String("swu-key-file"), c.String("swu-key-pass"))
			output := c.String("output")
			if output == "" {
				output = strings.TrimSuffix(seed, ".swu") + "_recreated.swu"
			}
			// Existing destinations, including links to any input, are rejected
			// before hashing or creating scratch files.
			output, err = fileutil.Destination(output)
			if err != nil {
				return err
			}
			opts := swurepackage.Options{
				OldSWU: seed, NewRootfs: rootfs,
				NewFIP:     c.String("fip"),
				PrivateKey: keys.PrivateKey, Passfile: keys.Passfile, KeyDir: keys.KeyDir,
				Output: output,
			}
			if rootfs != "" {
				opts.AVBBody, opts.AVBTail = rootfs+".avb.body", rootfs+".avb.tail"
			}
			if c.Bool("dry") {
				inputs, outputs, err := swurepackage.Preview(opts)
				if err != nil {
					return err
				}
				configInput, err := fileutil.FileInput(c.String("config"), "used")
				if err != nil {
					return err
				}
				return fileutil.WritePlan(c.App.Writer, append(inputs, configInput), outputs)
			}
			stage, err := os.MkdirTemp(filepath.Dir(output), ".swu-recreate-*")
			if err != nil {
				return err
			}
			defer os.RemoveAll(stage)
			opts.Output = filepath.Join(stage, "output.swu")
			if err := swurepackage.Run(opts); err != nil {
				return err
			}
			// Same-filesystem hard linking publishes the complete file atomically
			// and cannot overwrite a destination created since validation.
			if err := os.Link(opts.Output, output); err != nil {
				return fmt.Errorf("publishing SWU: %w", err)
			}
			_, err = fmt.Fprintln(c.App.Writer, output)
			return err
		},
	}
}

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"proto-distro/tools/distro/internal/builder"
	"proto-distro/tools/distro/internal/config"
	"proto-distro/tools/distro/internal/fileutil"
	"proto-distro/tools/distro/internal/postbuild"

	"github.com/urfave/cli/v2"
)

func postbuildCommand() *cli.Command {
	return &cli.Command{
		Name:      "postbuild",
		Usage:     "Run post-build steps (injection, zip) on an existing artifact directory or zip",
		ArgsUsage: "ARTIFACT_PATH",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "variant",
				Aliases: []string{"t"},
				Usage:   "Image variant for injection: release, mfg (default from config)",
			},
			&cli.StringFlag{
				Name:  "version",
				Usage: "Version for rootfs /usr/lib/os-release VERSION and rebuilt SWU software.version (default: derived from git)",
			},
			&cli.StringFlag{Name: "output-dir", Aliases: []string{"o"}, Usage: "Output parent directory for artifacts, ZIP, and latest links (default: existing locations)"},
		},
		Action: func(c *cli.Context) error {
			if c.Args().Len() < 1 {
				return fmt.Errorf("ARTIFACT_PATH argument required (zip file or artifact directory)")
			}

			cfg, err := config.Load(c.String("config"))
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			variant := c.String("variant")
			if variant == "" {
				variant = cfg.Defaults.Variant
			}

			injectVersion := c.String("version")
			if injectVersion == "" {
				v, err := builder.DistroVersionString(c.Context, cfg)
				if err != nil {
					return fmt.Errorf("deriving version: %w", err)
				}
				injectVersion = v
			}

			inputPath := c.Args().First()
			configPath := c.String("config")

			return runPostbuild(cfg, configPath, inputPath, variant, injectVersion, c.String("output-dir"))
		},
	}
}

func runPostbuild(cfg *config.Config, configPath, inputPath, variant, injectVersion, outputDir string) error {
	absInput, err := filepath.Abs(inputPath)
	if err != nil {
		return fmt.Errorf("resolving input path: %w", err)
	}

	// The input may be a symlink (e.g. artifacts/miner-image-latest{,.zip})
	// pointing at the real versioned artifact. Resolve it so downstream logic
	// — especially version detection from the basename — operates on the real
	// target, not the alias.
	if resolved, err := filepath.EvalSymlinks(absInput); err == nil {
		absInput = resolved
	}

	info, err := os.Stat(absInput)
	if err != nil {
		return fmt.Errorf("input path not found: %s", absInput)
	}
	if outputDir != "" {
		outputDir, err = filepath.Abs(outputDir)
		if err != nil {
			return fmt.Errorf("resolving output path: %w", err)
		}
	}

	folderPath := absInput
	if outputDir != "" {
		folderPath, err = preparePostbuildOutput(cfg, absInput, variant, injectVersion, outputDir)
		if err != nil {
			return err
		}
	} else {

		// If input is a zip, extract it to a sibling directory.
		if !info.IsDir() {
			if !strings.HasSuffix(absInput, ".zip") {
				return fmt.Errorf("input must be a .zip file or directory: %s", absInput)
			}
			folderPath = strings.TrimSuffix(absInput, ".zip")
			fmt.Fprintf(os.Stderr, "Extracting %s -> %s\n", filepath.Base(absInput), filepath.Base(folderPath))
			if err := os.RemoveAll(folderPath); err != nil {
				return fmt.Errorf("removing existing extraction dir: %w", err)
			}
			if err := os.Mkdir(folderPath, 0o755); err != nil {
				return fmt.Errorf("creating extraction dir: %w", err)
			}
			if err := fileutil.ExtractZip(absInput, folderPath); err != nil {
				return fmt.Errorf("extracting zip: %w", err)
			}
		}

		// Rename exported files first; shared rebuilding updates replaced SWU members and software.version.
		renamed, err := renameArtifactToVersion(cfg, variant, folderPath, injectVersion)
		if err != nil {
			return err
		}
		folderPath = renamed
	}

	if err := postbuild.InjectPackages(cfg, configPath, cfg.Defaults.Machine, variant, folderPath, injectVersion); err != nil {
		return fmt.Errorf("post-build injection: %w", err)
	}
	if outputDir != "" {
		outputConfig := *cfg
		outputConfig.Artifacts.RootDir = outputDir
		cfg = &outputConfig
	}

	return postbuild.FinalizeArtifactOutput(cfg, configPath, cfg.Defaults.Machine, variant, folderPath)
}

func preparePostbuildOutput(cfg *config.Config, inputPath, variant, version, outputDir string) (string, error) {
	info, err := os.Stat(inputPath)
	if err != nil {
		return "", fmt.Errorf("checking input artifact: %w", err)
	}
	sourceBase := filepath.Base(inputPath)
	if !info.IsDir() {
		if !strings.HasSuffix(inputPath, ".zip") {
			return "", fmt.Errorf("input must be a .zip file or directory: %s", inputPath)
		}
		sourceBase = strings.TrimSuffix(sourceBase, ".zip")
	}
	prefix := config.TargetName(variant) + "-" + cfg.Defaults.Machine + "-"
	if !strings.HasPrefix(sourceBase, prefix) {
		return "", fmt.Errorf("cannot rename: folder %q does not start with %q", sourceBase, prefix)
	}
	oldVersion := strings.TrimPrefix(sourceBase, prefix)
	if oldVersion == "" {
		return "", fmt.Errorf("cannot rename: no version token in folder %q", sourceBase)
	}
	finalName := strings.ReplaceAll(sourceBase, oldVersion, version)
	if finalName == "." || finalName == ".." || filepath.Base(finalName) != finalName {
		return "", fmt.Errorf("invalid output artifact name: %q", finalName)
	}

	outputDir, err = fileutil.ResolveOutputParent(outputDir)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		inside, err := fileutil.IsWithin(inputPath, outputDir)
		if err != nil {
			return "", fmt.Errorf("checking output directory: %w", err)
		}
		if inside {
			return "", fmt.Errorf("output directory must not be inside input artifact directory")
		}
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", fmt.Errorf("creating output directory: %w", err)
	}
	finalFolder := filepath.Join(outputDir, finalName)
	for _, path := range []string{finalFolder, finalFolder + ".zip"} {
		if _, err := os.Lstat(path); err == nil {
			return "", fmt.Errorf("output artifact already exists: %s", path)
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("checking output artifact %s: %w", path, err)
		}
	}
	if err := os.Mkdir(finalFolder, 0o755); err != nil {
		return "", fmt.Errorf("creating output artifact directory: %w", err)
	}
	if info.IsDir() {
		if err := fileutil.CopyDir(inputPath, finalFolder); err != nil {
			return "", fmt.Errorf("copying artifact to %s: %w", finalFolder, err)
		}
	} else {
		if err := fileutil.ExtractZip(inputPath, finalFolder); err != nil {
			return "", fmt.Errorf("extracting zip to %s: %w", finalFolder, err)
		}
	}
	if err := renameArtifactFiles(finalFolder, oldVersion, version); err != nil {
		return "", err
	}
	return finalFolder, nil
}

func renameArtifactToVersion(cfg *config.Config, variant, folderPath, newVersion string) (string, error) {
	target := config.TargetName(variant)
	prefix := fmt.Sprintf("%s-%s-", target, cfg.Defaults.Machine)
	base := filepath.Base(folderPath)
	if !strings.HasPrefix(base, prefix) {
		return "", fmt.Errorf("cannot rename: folder %q does not start with %q", base, prefix)
	}
	oldVersion := strings.TrimPrefix(base, prefix)
	if oldVersion == "" {
		return "", fmt.Errorf("cannot rename: no version token in folder %q", base)
	}
	if oldVersion == newVersion {
		return folderPath, nil
	}

	copied, err := copyArtifactsWithVersion(folderPath, oldVersion, newVersion)
	if err != nil {
		return "", fmt.Errorf("copying artifacts to version %s: %w", newVersion, err)
	}
	return copied, nil
}

// copyArtifactsWithVersion duplicates the artifact folder under a new
// version-substituted name, leaving the original in place, and rewrites the
// version token in every filename inside the copy.
func copyArtifactsWithVersion(folderPath, oldVersion, newVersion string) (string, error) {
	oldBase := filepath.Base(folderPath)
	newBase := strings.ReplaceAll(oldBase, oldVersion, newVersion)
	newFolder := filepath.Join(filepath.Dir(folderPath), newBase)

	if err := os.RemoveAll(newFolder); err != nil {
		return folderPath, fmt.Errorf("removing existing %s: %w", newBase, err)
	}

	if err := fileutil.CopyDir(folderPath, newFolder); err != nil {
		return folderPath, fmt.Errorf("copying %s -> %s: %w", oldBase, newBase, err)
	}
	fmt.Fprintf(os.Stderr, "Copied %s -> %s\n", oldBase, newBase)

	if err := renameArtifactFiles(newFolder, oldVersion, newVersion); err != nil {
		return folderPath, err
	}
	return newFolder, nil
}

func renameArtifactFiles(folderPath, oldVersion, newVersion string) error {
	if oldVersion == newVersion {
		return nil
	}
	entries, err := os.ReadDir(folderPath)
	if err != nil {
		return fmt.Errorf("reading artifact filenames: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.Contains(name, oldVersion) {
			continue
		}
		newName := strings.ReplaceAll(name, oldVersion, newVersion)
		from := filepath.Join(folderPath, name)
		to := filepath.Join(folderPath, newName)
		if err := os.Rename(from, to); err != nil {
			return fmt.Errorf("renaming %s -> %s: %w", name, newName, err)
		}
		fmt.Fprintf(os.Stderr, "Renamed %s -> %s\n", name, newName)
	}

	return nil
}

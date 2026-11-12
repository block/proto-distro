package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v2"
	"proto-distro/tools/distro/internal/config"
	"proto-distro/tools/distro/internal/gh"
)

type fetchSource struct {
	name    string
	repo    string
	prefix  string
	pattern string
	tag     string
}

type fetchSourceResult struct {
	Source         string   `json:"source"`
	Repo           string   `json:"repo"`
	RequestedTag   string   `json:"requested_tag"`
	ResolvedTag    string   `json:"resolved_tag"`
	Pattern        string   `json:"pattern"`
	DestinationDir string   `json:"destination_dir"`
	Files          []string `json:"files"`
	SkippedFiles   []string `json:"skipped_files"`
	MatchedAssets  int      `json:"matched_assets"`
}

type fetchResult struct {
	ArtifactRoot string              `json:"artifact_root"`
	Results      []fetchSourceResult `json:"results"`
	AllFiles     []string            `json:"all_files"`
}

func fetchCommand(configPath string) *cli.Command {
	cmd := &cli.Command{
		Name:  "fetch",
		Usage: "Download release artifacts from GitHub",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "pattern",
				Aliases: []string{"p"},
				Usage:   "file pattern to match (overrides default per-source pattern)",
			},
			&cli.StringFlag{
				Name:    "output-dir",
				Aliases: []string{"o"},
				Usage:   "parent directory for downloaded artifacts (default: configured artifact root with per-source folders)",
			},
			&cli.BoolFlag{
				Name:  "json",
				Usage: "print machine-readable JSON output",
			},
		},
		Action: runFetchCommand,
	}
	addFetchFlags(cmd, configPath)
	return cmd
}

func runFetchCommand(c *cli.Context) error {
	configPath := c.String("config")
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	sources, err := fetchSourcesFromConfig(cfg, c)
	if err != nil {
		return err
	}

	if err := gh.CheckAuth(); err != nil {
		return err
	}

	result, err := runFetch(cfg, configPath, sources, c.String("pattern"), c.String("output-dir"), c.String("output-dir") != "")
	if err != nil {
		return err
	}

	if c.Bool("json") {
		return printFetchJSON(result)
	}
	printFetchSummary(result)
	return nil
}

func fetchSourcesFromConfig(cfg *config.Config, c *cli.Context) ([]fetchSource, error) {
	var sources []fetchSource
	for _, cfgSrc := range cfg.Fetch.Sources {
		sources = append(sources, fetchSource{
			name:    cfgSrc.Name,
			repo:    cfgSrc.Repo,
			prefix:  cfgSrc.Prefix,
			pattern: cfgSrc.Pattern,
			tag:     c.String(cfgSrc.Name),
		})
	}

	for _, s := range sources {
		if s.tag != "" {
			return sources, nil
		}
	}

	var flagNames []string
	for _, s := range sources {
		flagNames = append(flagNames, "--"+s.name)
	}
	return nil, fmt.Errorf("at least one source flag is required (%s)", strings.Join(flagNames, ", "))
}

func printFetchJSON(result fetchResult) error {
	simple := make(map[string][]string)
	for _, src := range result.Results {
		var files []string
		files = append(files, src.Files...)
		files = append(files, src.SkippedFiles...)
		simple[src.Source] = files
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(simple)
}

func printFetchSummary(result fetchResult) {
	var lines []string
	for _, src := range result.Results {
		lines = append(lines, fmt.Sprintf("  %s: downloaded=%d skipped=%d matched=%d from %s -> %s",
			src.Source, len(src.Files), len(src.SkippedFiles), src.MatchedAssets, src.ResolvedTag, src.DestinationDir))
	}

	fmt.Printf("\nDownloaded files (%d) into %s:\n%s\n", len(result.AllFiles), result.ArtifactRoot, strings.Join(lines, "\n"))
}

func runFetch(
	cfg *config.Config,
	configPath string,
	sources []fetchSource,
	patternOverride string,
	outputDir string,
	outputDirSet bool,
) (fetchResult, error) {
	baseOutputDir := outputDir
	if !outputDirSet {
		baseOutputDir = config.ArtifactRootDir(cfg, configPath)
	}
	if abs, err := filepath.Abs(baseOutputDir); err == nil {
		baseOutputDir = abs
	}

	result := fetchResult{
		ArtifactRoot: baseOutputDir,
	}

	for _, src := range sources {
		if src.tag == "" {
			continue
		}

		tag, err := gh.ResolveTag(src.tag, src.repo, src.prefix)
		if err != nil {
			return fetchResult{}, fmt.Errorf("resolving %s tag: %w", src.name, err)
		}

		pattern := src.pattern
		if patternOverride != "" {
			pattern = patternOverride
		}

		destDir := baseOutputDir
		if !outputDirSet {
			destDir = filepath.Join(baseOutputDir, fmt.Sprintf("%s-%s", src.name, tag))
		}

		download, err := gh.DownloadAssets(src.repo, tag, pattern, destDir)
		if err != nil {
			return fetchResult{}, fmt.Errorf("downloading %s assets from %s/%s: %w", src.name, src.repo, tag, err)
		}
		if download.Matched == 0 {
			return fetchResult{}, fmt.Errorf("downloading %s assets: no %s files found in release %s/%s", src.name, pattern, src.repo, tag)
		}

		result.Results = append(result.Results, fetchSourceResult{
			Source:         src.name,
			Repo:           src.repo,
			RequestedTag:   src.tag,
			ResolvedTag:    tag,
			Pattern:        pattern,
			DestinationDir: destDir,
			Files:          download.Downloaded,
			SkippedFiles:   download.Skipped,
			MatchedAssets:  download.Matched,
		})
		result.AllFiles = append(result.AllFiles, download.Downloaded...)
	}

	return result, nil
}

// addFetchFlags adds dynamic source flags to the fetch command from config.
func addFetchFlags(cmd *cli.Command, configPath string) {
	cfg, err := config.Load(configPath)
	if err != nil {
		// Fall back to no dynamic flags if config can't be loaded
		return
	}

	for _, src := range cfg.Fetch.Sources {
		desc := fmt.Sprintf("tag for %s artifacts (%s", src.Name, src.Repo)
		if src.Prefix != "" {
			desc += fmt.Sprintf(", prefix: %s", src.Prefix)
		}
		desc += ")"

		cmd.Flags = append(cmd.Flags, &cli.StringFlag{
			Name:  src.Name,
			Usage: desc,
		})
	}
}

package postbuild

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"proto-distro/tools/distro/internal/color"
	"proto-distro/tools/distro/internal/config"
	"proto-distro/tools/distro/internal/distroinject"
	"proto-distro/tools/distro/internal/gh"
)

// InjectPackages resolves configured IPKs before shared rootfs processing.
// Release/mfg AVB and discovered SWUs are rebuilt even without packages or a VERSION update.
func InjectPackages(cfg *config.Config, configPath, machine, variant, folderPath, version string) error {
	var ipkFiles []string
	if cfg.Injection.Enabled && variant != "" {
		variantCfg, ok := cfg.Injection.Variants[variant]
		if !ok {
			return fmt.Errorf("injection variant %q not found in config", variant)
		}

		var err error
		ipkFiles, err = resolveVariantIPKs(cfg, configPath, variantCfg)
		if err != nil {
			return err
		}
	}

	switch {
	case len(ipkFiles) > 0 && version != "":
		fmt.Fprintf(os.Stderr, "Auto-injecting %d IPK package(s) into %s (%s) and setting VERSION=%s\n", len(ipkFiles), filepath.Base(folderPath), variant, version)
	case len(ipkFiles) > 0:
		fmt.Fprintf(os.Stderr, "Auto-injecting %d IPK package(s) into %s (%s)\n", len(ipkFiles), filepath.Base(folderPath), variant)
	case version != "":
		fmt.Fprintf(os.Stderr, "Updating %s os-release VERSION to %s\n", filepath.Base(folderPath), version)
	}

	if _, err := distroinject.RunWithConfig(context.Background(), distroinject.Params{
		Config:        cfg,
		ConfigPath:    configPath,
		InputPath:     folderPath,
		IPKPaths:      ipkFiles,
		Version:       version,
		Machine:       machine,
		Variant:       variant,
		SkipChecksums: true,
	}); err != nil {
		return fmt.Errorf("processing artifact rootfs: %w", err)
	}

	return nil
}

func resolveVariantIPKs(
	cfg *config.Config,
	configPath string,
	variantCfg config.InjectionVariant,
) ([]string, error) {
	targetArch := cfg.Defaults.Arch
	if targetArch == "" {
		return nil, fmt.Errorf("injection enabled but defaults.arch is empty; please set defaults.arch to armv7")
	}
	if !slices.Contains(supportedIPKArchs, targetArch) {
		return nil, fmt.Errorf("unsupported defaults.arch %q for injection; only armv7 is supported", targetArch)
	}

	artifactRoot := config.ArtifactRootDir(cfg, configPath)
	cacheDir := cfg.Injection.CacheDir
	if cacheDir == "" {
		cacheDir = "injection-cache"
	}
	cacheRoot := filepath.Join(artifactRoot, cacheDir)
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		return nil, fmt.Errorf("creating injection cache dir: %w", err)
	}

	var localIPKDir string
	if cfg.Injection.LocalIPKDir != "" {
		localIPKDir = config.ResolvePath(configPath, cfg.Injection.LocalIPKDir)
	}

	fmt.Fprintf(os.Stderr, "Resolving IPK packages for injection (%d configured):\n", len(variantCfg.Packages))

	var ipkFiles []string
	var missing []string
	for _, pkg := range variantCfg.Packages {
		required := true
		if pkg.Required != nil {
			required = *pkg.Required
		}

		found, source, err := selectIPK(cfg, configPath, localIPKDir, cacheRoot, pkg, targetArch)
		if err != nil {
			if required {
				missing = append(missing, fmt.Sprintf("%s: %v", pkg.Name, err))
				continue
			}
			fmt.Fprintf(os.Stderr, "Skipping optional package %q: %v\n", pkg.Name, err)
			continue
		}

		if found == "" {
			if required {
				distVariant := pkg.DistVariant
				if distVariant == "" {
					distVariant = "release"
				}
				ipkPrefix := deriveIPKPrefix(pkg.Name, distVariant)
				missing = append(missing, fmt.Sprintf("%s (%s): not found locally or remotely", pkg.Name, ipkPrefix))
				continue
			}
			continue
		}

		fmt.Fprintln(os.Stderr, color.Cmdf("  %s: %s (%s)", pkg.Name, filepath.Base(found), source))
		ipkFiles = append(ipkFiles, found)
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("missing %d required IPK package(s):\n  %s", len(missing), strings.Join(missing, "\n  "))
	}

	return ipkFiles, nil
}

// selectIPK resolves the IPK file for a package by checking local sources first,
// then falling back to remote sources. Returns the path, a human-readable source
// description, and any error.
func selectIPK(
	cfg *config.Config,
	configPath, localIPKDir, cacheRoot string,
	pkg config.InjectionPackage,
	targetArch string,
) (path string, source string, err error) {
	distVariant := pkg.DistVariant
	if distVariant == "" {
		distVariant = "release"
	}
	ipkPrefix := deriveIPKPrefix(pkg.Name, distVariant)

	// 1. Check local source first.
	found, err := resolveLocalIPK(configPath, localIPKDir, pkg, ipkPrefix, distVariant, targetArch)
	if err != nil {
		return "", "", err
	}
	if found != "" {
		rel, _ := filepath.Rel(localIPKDir, found)
		return found, fmt.Sprintf("local: %s", rel), nil
	}

	// If glob_override is set, this package is local-only — skip remote fallback.
	if pkg.GlobOverride != "" {
		return "", "", nil
	}

	// 2. Fall back to remote source.
	found, remote, err := resolveRemoteIPK(cfg, cacheRoot, pkg, ipkPrefix, targetArch)
	if err != nil {
		return "", "", err
	}
	if found != "" {
		return found, fmt.Sprintf("remote: %s", remote), nil
	}

	return "", "", nil
}

func resolveLocalIPK(configPath, localIPKDir string, pkg config.InjectionPackage, ipkPrefix, distVariant string, targetArch string) (string, error) {
	if pkg.GlobOverride != "" {
		globPath := config.ResolvePath(configPath, pkg.GlobOverride)
		matches, err := filepath.Glob(globPath)
		if err != nil {
			return "", fmt.Errorf("invalid glob for %s: %w", pkg.Name, err)
		}
		return pickIPKMatch(matches, pkg.Name, ipkPrefix, "local", targetArch)
	}

	if pkg.Name == "" || localIPKDir == "" {
		return "", nil
	}

	dir := filepath.Join(localIPKDir, pkg.Name, distVariant)
	matches, err := filepath.Glob(filepath.Join(dir, "*.ipk"))
	if err != nil {
		return "", fmt.Errorf("finding local IPKs for %s: %w", pkg.Name, err)
	}
	return pickIPKMatch(matches, pkg.Name, ipkPrefix, "local", targetArch)
}

func resolveRemoteIPK(cfg *config.Config, cacheRoot string, pkg config.InjectionPackage, ipkPrefix string, targetArch string) (path string, remote string, err error) {
	injSrc, err := cfg.InjectionSourceForPackage(pkg.Name)
	if err != nil {
		return "", "", err
	}
	fetchSrc, ok := cfg.FetchSourceByName(injSrc.Name)
	if !ok {
		return "", "", fmt.Errorf("unknown fetch source %q for package %q", injSrc.Name, pkg.Name)
	}

	tag := strings.TrimSpace(injSrc.Tag)
	if tag == "" {
		return "", "", fmt.Errorf("no tag configured for source %q", injSrc.Name)
	}

	resolvedTag, err := gh.ResolveTag(tag, fetchSrc.Repo, fetchSrc.Prefix)
	if err != nil {
		return "", "", fmt.Errorf("resolving tag for %s: %w", injSrc.Name, err)
	}

	remote = fmt.Sprintf("%s/%s", fetchSrc.Repo, resolvedTag)

	destDir := filepath.Join(cacheRoot, fmt.Sprintf("%s-%s", injSrc.Name, resolvedTag))
	download, err := gh.DownloadAssets(fetchSrc.Repo, resolvedTag, "*.ipk", destDir)
	if err != nil {
		return "", "", fmt.Errorf("downloading IPKs for %s: %w", injSrc.Name, err)
	}
	if download.Matched == 0 {
		return "", "", fmt.Errorf("release %s has no *.ipk assets; looking for package %q", remote, ipkPrefix)
	}

	matches, err := filepath.Glob(filepath.Join(destDir, "*.ipk"))
	if err != nil {
		return "", "", err
	}
	found, err := pickIPKMatch(matches, pkg.Name, ipkPrefix, "remote", targetArch)
	if err != nil {
		return "", "", err
	}
	return found, remote, nil
}

func pickIPKMatch(matches []string, pkgName, ipkPrefix, source string, targetArch string) (string, error) {
	var filtered []string
	for _, m := range matches {
		base := filepath.Base(m)
		if matchesIPK(base, ipkPrefix, targetArch) {
			filtered = append(filtered, m)
		}
	}

	if len(filtered) == 0 {
		return "", nil
	}
	if len(filtered) > 1 {
		names := make([]string, len(filtered))
		for i, f := range filtered {
			names[i] = filepath.Base(f)
		}
		return "", fmt.Errorf("%s: multiple %s IPKs matched for %s: %s", pkgName, source, ipkPrefix, strings.Join(names, ", "))
	}
	return filtered[0], nil
}

// supportedIPKArchs is the list of supported IPK architecture names.
var supportedIPKArchs = []string{
	"armv7",
}

// ipkRestRe matches the portion of an IPK filename after "{prefix}_":
// an optional arch field (single token) followed by a version and ".ipk".
//
//	"armv7_1.2.4.ipk" → arch="armv7"
//	"1.2.4.ipk"       → arch="" (arch-less package)
//
// Filenames with extra underscore-delimited fields (e.g. "dev_armv7_1.2.4.ipk")
// will not match, preventing a release prefix from matching variant files.
var ipkRestRe = regexp.MustCompile(`^(?:([^_]+)_)?([^_]+)\.ipk$`)

func matchesIPK(filename, ipkPrefix, targetArch string) bool {
	rest, found := strings.CutPrefix(filename, ipkPrefix+"_")
	if !found {
		return false
	}

	m := ipkRestRe.FindStringSubmatch(rest)
	if m == nil {
		return false
	}

	ipkArch := m[1] // empty string for arch-less packages
	if ipkArch == "" || ipkArch == "all" {
		return true
	}
	if !slices.Contains(supportedIPKArchs, ipkArch) {
		return false
	}
	return ipkArch == targetArch
}

func deriveIPKPrefix(name, distVariant string) string {
	if distVariant == "" || distVariant == "release" {
		return name
	}
	return name + "_" + distVariant
}

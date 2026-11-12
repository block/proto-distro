package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Defaults   Defaults           `yaml:"defaults"`
	Docker     Docker             `yaml:"docker"`
	Signing    Signing            `yaml:"signing"`
	AVB        map[string]AVBKeys `yaml:"avb"`
	Version    Version            `yaml:"version"`
	Fetch      Fetch              `yaml:"fetch"`
	Injection  Injection          `yaml:"injection"`
	Artifacts  Artifacts          `yaml:"artifacts"`
	BuildStats BuildStats         `yaml:"build_stats"`
}

type Defaults struct {
	Machine string `yaml:"machine"`
	Distro  string `yaml:"distro"`
	Variant string `yaml:"variant"`
	EnvFile string `yaml:"env_file"`
	Arch    string `yaml:"arch"`
}

type Docker struct {
	Builder  BuilderDocker  `yaml:"builder"`
	Injector InjectorDocker `yaml:"injector"`
}

type BuilderDocker struct {
	Image              string   `yaml:"image"`
	Hostname           string   `yaml:"hostname"`
	ContainerDistroDir string   `yaml:"container_distro_dir"`
	ShellInit          string   `yaml:"shell_init"`
	BuildVolume        string   `yaml:"build_volume"`
	BuildDir           string   `yaml:"build_dir"`
	EnvPassthrough     []string `yaml:"env_passthrough"`
}

type InjectorDocker struct {
	Image string `yaml:"image"`
}

type Signing struct {
	KeyDir     string `yaml:"key_dir"`
	PrivateKey string `yaml:"private_key"`
	Passfile   string `yaml:"passfile"`
}

// AVBKeys identifies the independent rootfs signing keys for one machine.
type AVBKeys struct {
	PrivateKey   string `yaml:"private_key"`
	PublicKey    string `yaml:"public_key"`
	AVBPublicKey string `yaml:"avb_public_key"`
}

type Version struct {
	TagPrefix string `yaml:"tag_prefix"`
}

type FetchSource struct {
	Name    string `yaml:"name"`
	Repo    string `yaml:"repo"`
	Prefix  string `yaml:"prefix"`
	Pattern string `yaml:"pattern"`
}

type Fetch struct {
	Sources []FetchSource `yaml:"sources"`
}

type Injection struct {
	Enabled     bool                        `yaml:"enabled"`
	LocalIPKDir string                      `yaml:"local_ipk_dir"`
	CacheDir    string                      `yaml:"cache_dir"`
	Sources     []InjectionSource           `yaml:"sources"`
	Variants    map[string]InjectionVariant `yaml:"variants"`
}

type InjectionSource struct {
	Name     string   `yaml:"name"`
	Tag      string   `yaml:"tag"`
	Packages []string `yaml:"packages"`
}

type InjectionVariant struct {
	Packages []InjectionPackage `yaml:"packages"`
}

type InjectionPackage struct {
	Name         string `yaml:"name"`
	DistVariant  string `yaml:"dist_variant"`
	GlobOverride string `yaml:"glob_override"`
	Required     *bool  `yaml:"required"`
}

type Artifacts struct {
	RootDir        string                     `yaml:"root_dir"`
	ImagesBasePath string                     `yaml:"images_base_path"`
	Deploy         []string                   `yaml:"deploy"`
	Files          []string                   `yaml:"files"`
	LocalFiles     []string                   `yaml:"local_files"`
	Variants       map[string]ArtifactVariant `yaml:"variants"`
}

type ArtifactVariant struct {
	Files []string `yaml:"files"`
}

type BuildStats struct {
	Paths []string `yaml:"paths"`
}

// Load reads and parses a distro.yaml config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file %s: %w", path, err)
	}

	return &cfg, nil
}

func (c *Config) FetchSourceByName(name string) (FetchSource, bool) {
	for _, src := range c.Fetch.Sources {
		if src.Name == name {
			return src, true
		}
	}
	return FetchSource{}, false
}

func (c *Config) InjectionSourceByName(name string) (InjectionSource, bool) {
	for _, src := range c.Injection.Sources {
		if src.Name == name {
			return src, true
		}
	}
	return InjectionSource{}, false
}

func (c *Config) InjectionSourceForPackage(pkgName string) (InjectionSource, error) {
	var found *InjectionSource
	for i := range c.Injection.Sources {
		src := &c.Injection.Sources[i]
		for _, mapped := range src.Packages {
			if mapped != pkgName {
				continue
			}
			if found != nil {
				return InjectionSource{}, fmt.Errorf("package %q mapped to multiple injection sources: %q and %q", pkgName, found.Name, src.Name)
			}
			found = src
		}
	}
	if found == nil {
		return InjectionSource{}, fmt.Errorf("package %q not mapped to any injection source", pkgName)
	}
	return *found, nil
}

// TargetName constructs the full Yocto target name from a variant (e.g. "release" → "miner-image-release").
func TargetName(variant string) string {
	return "miner-image-" + variant
}

// ExpandVars replaces {machine}, {distro}, {target}, and {version} placeholders in a string.
// Parameters: machine, distro, target (Yocto image name), version (git-derived).
func ExpandVars(s, machine, distro, target, version string) string {
	s = strings.ReplaceAll(s, "{machine}", machine)
	s = strings.ReplaceAll(s, "{distro}", distro)
	s = strings.ReplaceAll(s, "{target}", target)
	s = strings.ReplaceAll(s, "{version}", version)
	return s
}

// ResolvePath resolves a path from the config file. If the path is absolute it
// is returned as-is; otherwise it is joined to the directory containing configPath.
func ResolvePath(configPath, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(filepath.Dir(configPath), p)
}

// SigningKeys holds resolved signing key paths.
type SigningKeys struct {
	KeyDir     string
	PrivateKey string
	Passfile   string
}

// ResolveSigningKeys resolves signing key paths from config relative to configPath.
// Any non-empty override takes precedence over the config value.
func ResolveSigningKeys(configPath string, signing Signing, privateKeyOverride, passfileOverride string) SigningKeys {
	var keys SigningKeys
	if signing.KeyDir != "" {
		keys.KeyDir = ResolvePath(configPath, signing.KeyDir)
		if signing.PrivateKey != "" {
			keys.PrivateKey = filepath.Join(keys.KeyDir, signing.PrivateKey)
		}
		if signing.Passfile != "" {
			keys.Passfile = filepath.Join(keys.KeyDir, signing.Passfile)
		}
	}
	if privateKeyOverride != "" {
		keys.PrivateKey = privateKeyOverride
	}
	if passfileOverride != "" {
		keys.Passfile = passfileOverride
	}
	return keys
}

// ArtifactRootDir resolves the configured artifact root directory relative to the config file.
// If artifacts.root_dir is omitted, it defaults to "artifacts".
func ArtifactRootDir(cfg *Config, configPath string) string {
	rootDir := cfg.Artifacts.RootDir
	if rootDir == "" {
		rootDir = "artifacts"
	}
	return ResolvePath(configPath, rootDir)
}

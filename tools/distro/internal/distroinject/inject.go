package distroinject

import (
	"context"

	"proto-distro/tools/distro/internal/config"
)

// Params configures an injection operation with config-level awareness.
type Params struct {
	Config        *config.Config
	ConfigPath    string
	InputPath     string
	IPKPaths      []string
	Version       string
	Machine       string
	Variant       string
	SkipChecksums bool
	// Optional overrides for signing keys. If empty, resolved from config.
	PrivateKeyOverride string
	PassfileOverride   string
	Verbose            bool
}

// RunWithConfig resolves separate SWU credentials and passes artifact policy to Run.
func RunWithConfig(ctx context.Context, params Params) (string, error) {
	cfg := params.Config

	// Resolve signing keys from config, with overrides taking precedence
	keys := config.ResolveSigningKeys(params.ConfigPath, cfg.Signing, params.PrivateKeyOverride, params.PassfileOverride)

	return Run(ctx, Options{
		ContainerImage: cfg.Docker.Injector.Image,
		Machine:        params.Machine,
		Variant:        params.Variant,
		AVB:            cfg.AVB,
		ConfigPath:     params.ConfigPath,
		InputPath:      params.InputPath,
		IPKPaths:       params.IPKPaths,
		Version:        params.Version,
		PrivateKey:     keys.PrivateKey,
		Passfile:       keys.Passfile,
		KeyDir:         keys.KeyDir,
		Verbose:        params.Verbose,
		SkipChecksums:  params.SkipChecksums,
	})
}

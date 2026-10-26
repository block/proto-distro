package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"gopkg.in/yaml.v3"
)

const DefaultConfigPath = "/etc/proto-swupdate.yaml"

type Config struct {
	// Hardware
	HWCompatibility string `yaml:"hw_compatibility"`
	OSReleaseFile   string `yaml:"os_release_file"`

	// Partitions
	RootfsAEnvVar   string   `yaml:"rootfs_a_env_var"`
	RootfsBEnvVar   string   `yaml:"rootfs_b_env_var"`
	MetadataDevices []string `yaml:"metadata_devices"`

	// SWUpdate
	SWUpdateBinary string `yaml:"swupdate_binary"`
	SWUpdateCfg    string `yaml:"swupdate_cfg"`
	SWUpdateKey    string `yaml:"swupdate_key"`

	// U-Boot
	UstateVar      string `yaml:"ustate_var"`
	UstateUpdating string `yaml:"ustate_updating"`
	UstateIdle     string `yaml:"ustate_idle"`
	BootcountAddr  uint32 `yaml:"bootcount_addr"`

	// TAMP register (FWU trial boot info)
	TAMPFWUInfoAddr uint32 `yaml:"tamp_fwu_info_addr"`
}

func Default() Config {
	return Config{
		HWCompatibility: "1.0",
		OSReleaseFile:   "/etc/os-release",

		RootfsAEnvVar:   "a_partnum",
		RootfsBEnvVar:   "b_partnum",
		MetadataDevices: []string{"/dev/disk/by-partlabel/metadata1", "/dev/disk/by-partlabel/metadata2"},

		SWUpdateBinary: "/usr/bin/swupdate",
		SWUpdateCfg:    "/etc/swupdate.cfg",
		SWUpdateKey:    "/etc/swupdate/public_key.pem",

		UstateVar:      "ustate",
		UstateUpdating: "1",
		UstateIdle:     "0",
		BootcountAddr:  0xDCFFFFF0,

		TAMPFWUInfoAddr: 0x5C00A128,
	}
}

// Load reads DefaultConfigPath and overlays it onto Default(). A missing file
// is fine — defaults stand. Any other read or parse error fails closed: this
// tool writes u-boot env and /dev/mem registers, so silently misconfigured
// defaults are dangerous.
func Load() (Config, error) {
	cfg := Default()

	data, err := os.ReadFile(DefaultConfigPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read %s: %w", DefaultConfigPath, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", DefaultConfigPath, err)
	}
	return cfg, nil
}

// This file stores device targets in the working directory's .go_fastboot/target.json.
package device

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	configDirName   = ".go_fastboot"
	targetFileName  = "target.json"
	configDirPerms  = 0700
	targetFilePerms = 0600
)

// Cache manages persistent storage of device target information
type Cache struct {
	configDir string
}

// NewCache binds a cache to the current working directory.
func NewCache() (*Cache, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting working directory: %w", err)
	}
	return &Cache{configDir: filepath.Join(dir, configDirName)}, nil
}

// ensureConfigDir creates the config directory if it doesn't exist
func (c *Cache) ensureConfigDir() error {
	return os.MkdirAll(c.configDir, configDirPerms)
}

// targetPath returns the full path to the target file
func (c *Cache) targetPath() string {
	return filepath.Join(c.configDir, targetFileName)
}

// SaveTarget persists the current target device
func (c *Cache) SaveTarget(d *Device) error {
	if err := c.ensureConfigDir(); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling target: %w", err)
	}

	if err := os.WriteFile(c.targetPath(), data, targetFilePerms); err != nil {
		return fmt.Errorf("writing target file: %w", err)
	}

	return nil
}

// LoadTarget loads the current target device from cache
func (c *Cache) LoadTarget() (*Device, error) {
	data, err := os.ReadFile(c.targetPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No target set
		}
		return nil, fmt.Errorf("reading target file: %w", err)
	}

	var d Device
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("parsing target file: %w", err)
	}

	return &d, nil
}

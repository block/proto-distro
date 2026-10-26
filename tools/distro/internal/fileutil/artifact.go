package fileutil

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ResolveArtifact resolves top-level aliases before output names are derived.
func ResolveArtifact(name string) (string, error) {
	absolute, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

// SafeMemberName normalizes an archive name, rejecting unsafe paths.
func SafeMemberName(name string) (string, error) {
	if strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("unsafe archive member: %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", fmt.Errorf("unsafe archive member: %q", name)
		}
	}
	normalized := path.Clean(name)
	if normalized == "." || !fs.ValidPath(normalized) {
		return "", fmt.Errorf("unsafe archive member: %q", name)
	}
	return normalized, nil
}

// ResolveDestination resolves an existing directory parent without requiring an absent output.
func ResolveDestination(name string) (string, error) {
	absolute, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	parent, err := ResolveArtifact(filepath.Dir(absolute))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(parent)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("destination parent is not a directory: %s", parent)
	}
	return filepath.Join(parent, filepath.Base(absolute)), nil
}

// Destination resolves an existing parent and rejects existing outputs, including aliases.
func Destination(name string) (string, error) {
	absolute, err := ResolveDestination(name)
	if err != nil {
		return "", err
	}
	if err := RequireAbsent(absolute); err != nil {
		return "", err
	}
	return absolute, nil
}

// RequireAbsent also rejects dangling destination symlinks.
func RequireAbsent(name string) error {
	_, err := os.Lstat(name)
	if err == nil {
		return fmt.Errorf("destination already exists: %s", name)
	}
	if !os.IsNotExist(err) {
		return err
	}
	return nil
}

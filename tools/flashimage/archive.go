package flashimage

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// ExtractZIP extracts into a fresh, caller-owned directory. On failure, the
// caller must remove any partial output. onFile receives absolute paths in
// archive order, only after each regular file has been successfully closed.
func ExtractZIP(archivePath, destinationDir string, onFile func(string)) (err error) {
	if archivePath == "" || destinationDir == "" {
		return fmt.Errorf("ZIP archive and destination paths must not be empty")
	}
	destinationDir, err = filepath.Abs(destinationDir)
	if err != nil {
		return err
	}
	info, err := os.Stat(destinationDir)
	if err != nil {
		return fmt.Errorf("inspect ZIP destination: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("ZIP destination %q is not a directory", destinationDir)
	}
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open image ZIP: %w", err)
	}
	defer func() { err = errors.Join(err, archive.Close()) }()

	type destination struct {
		name     string
		dir      bool
		explicit bool
	}
	seen := make(map[string]destination)
	for _, entry := range archive.File {
		name := entry.Name
		// ZIP paths are slash-separated. Reject Windows separators/drives too,
		// rather than letting the host OS reinterpret them differently.
		if name == "" || strings.ContainsAny(name, "\\:\x00") || !filepath.IsLocal(name) {
			return fmt.Errorf("invalid ZIP entry path %q", name)
		}
		for _, component := range strings.Split(name, "/") {
			if component == ".." {
				return fmt.Errorf("ZIP traversal path %q", name)
			}
		}
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 || (!mode.IsRegular() && !mode.IsDir()) {
			return fmt.Errorf("unsupported ZIP entry type for %q", name)
		}
		clean := filepath.Clean(name)
		if clean == "." {
			return fmt.Errorf("invalid ZIP entry path %q", name)
		}
		components := strings.Split(clean, string(filepath.Separator))
		relative := ""
		for i, component := range components {
			relative = filepath.Join(relative, component)
			last := i == len(components)-1
			isDir := !last || mode.IsDir()
			// Choose one rune per Unicode fold cycle, not merely lowercase:
			// lowercase alone misses aliases such as sigma and final sigma.
			key := strings.Map(func(r rune) rune {
				canonical := r
				for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
					if next < canonical {
						canonical = next
					}
				}
				return canonical
			}, relative)
			previous, exists := seen[key]
			if exists && (previous.name != relative || previous.dir != isDir || (last && previous.explicit)) {
				return fmt.Errorf("conflicting ZIP destination %q", name)
			}
			seen[key] = destination{relative, isDir, previous.explicit || last}
			if isDir && !exists {
				path := filepath.Join(destinationDir, relative)
				if err := os.Mkdir(path, 0700); err != nil {
					return fmt.Errorf("create ZIP directory %q: %w", relative, err)
				}
			}
		}
		if mode.IsDir() {
			continue
		}
		path := filepath.Join(destinationDir, clean)
		if err := extractFile(entry, path); err != nil {
			return fmt.Errorf("extract ZIP file %q: %w", name, err)
		}
		if onFile != nil {
			onFile(path)
		}
	}
	return nil
}

func extractFile(entry *zip.File, destination string) error {
	source, err := entry.Open()
	if err != nil {
		return err
	}
	// O_EXCL also rejects aliases on case-insensitive filesystems.
	target, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.Join(err, source.Close())
	}
	_, copyErr := io.Copy(target, source)
	return errors.Join(copyErr, source.Close(), target.Close())
}

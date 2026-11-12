package fileutil

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"proto-distro/tools/distro/internal/color"
)

// CopyFile copies a file from src to dst, preserving permissions.
func CopyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	written, err := io.Copy(dstFile, srcFile)
	if err != nil {
		return err
	}

	if written != srcInfo.Size() {
		return fmt.Errorf("incomplete copy: %d/%d bytes", written, srcInfo.Size())
	}

	return os.Chmod(dst, srcInfo.Mode())
}

// CollectIPKFiles collects .ipk files from the given paths (files or directories)
// into destDir and returns the list of destination paths.
func CollectIPKFiles(packages []string, destDir string) ([]string, error) {
	var collected []string

	for _, pkg := range packages {
		absPath, err := filepath.Abs(pkg)
		if err != nil {
			return nil, err
		}

		info, err := os.Stat(absPath)
		if err != nil {
			fmt.Println(color.Warnf("Warning: Skipping %s: %v", pkg, err))
			continue
		}

		if info.IsDir() {
			entries, err := os.ReadDir(absPath)
			if err != nil {
				fmt.Println(color.Warnf("Warning: Skipping directory %s: %v", pkg, err))
				continue
			}
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".ipk") {
					src := filepath.Join(absPath, entry.Name())
					dst := filepath.Join(destDir, entry.Name())
					if err := CopyFile(src, dst); err != nil {
						fmt.Println(color.Warnf("Warning: Skipping %s: %v", entry.Name(), err))
						continue
					}
					collected = append(collected, dst)
				}
			}
		} else if strings.HasSuffix(absPath, ".ipk") {
			dst := filepath.Join(destDir, filepath.Base(absPath))
			if err := CopyFile(absPath, dst); err != nil {
				fmt.Println(color.Warnf("Warning: Skipping %s: %v", pkg, err))
				continue
			}
			collected = append(collected, dst)
		} else {
			fmt.Println(color.Warnf("Warning: Skipping %s (not an IPK file or directory)", pkg))
		}
	}

	return collected, nil
}

// ExtractZip extracts a zip archive to destDir, guarding against zip-slip attacks.
func ExtractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		target := filepath.Join(destDir, f.Name)
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("invalid zip entry path: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		if err := extractZipFile(f, target); err != nil {
			return err
		}
	}
	return nil
}

func extractZipFile(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	w, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
	if err != nil {
		return err
	}
	defer w.Close()

	_, err = io.Copy(w, rc)
	return err
}

// CopyDir recursively copies a directory tree, preserving file permissions.
func CopyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return CopyFile(path, target)
	})
}

// CreateZip creates a standard ZIP archive using DEFLATE at best compression.
func CreateZip(srcDir, zipPath string) error {
	f, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer f.Close()

	compressor, err := flate.NewWriter(io.Discard, flate.BestCompression)
	if err != nil {
		return err
	}
	w := zip.NewWriter(f)
	w.RegisterCompressor(zip.Deflate, func(dst io.Writer) (io.WriteCloser, error) {
		compressor.Reset(dst)
		return compressor, nil
	})

	err = filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		if info.IsDir() {
			_, err := w.Create(rel + "/")
			return err
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = rel
		header.Method = zip.Deflate

		writer, err := w.CreateHeader(header)
		if err != nil {
			return err
		}

		src, err := os.Open(path)
		if err != nil {
			return err
		}

		_, err = io.Copy(writer, src)
		closeErr := src.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
	closeErr := w.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return f.Close()
}

// WriteChecksums computes SHA256 for all files in dir and writes them to the
// given filename (e.g. "sha256.txt" or "sha.txt") inside dir. The checksum file
// itself is excluded from the listing. Each line has the format "<hash>  <relpath>\n".
func WriteChecksums(dir, filename string) error {
	checksumFile := filepath.Join(dir, filename)
	f, err := os.Create(checksumFile)
	if err != nil {
		return fmt.Errorf("creating %s: %w", filename, err)
	}
	defer f.Close()

	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if d.Name() == filename {
			return nil
		}
		hash, err := SHA256File(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		fmt.Fprintf(f, "%s  %s\n", hash, rel)
		return nil
	})
	if err != nil {
		return fmt.Errorf("computing checksums: %w", err)
	}
	return nil
}

// ipkFilenameRe parses an IPK filename: name_arch_version.ipk
// The last underscore-delimited field (before .ipk) is always the version.
// The second-to-last field is the architecture. Everything before that is the name.
// Examples:
//   - mcdd_armv7_9.8.2.ipk           → name=mcdd, version=9.8.2
//   - mcdd_dev_armv7_9.8.2.ipk       → name=mcdd_dev, version=9.8.2
//   - mcdd_armv7_8f02bab595-dirty.ipk → name=mcdd, version=8f02bab595-dirty
var ipkFilenameRe = regexp.MustCompile(`^(.+)_([^_]+)_([^_]+)\.ipk$`)

// ParseIPKFilename extracts the package name and version from an IPK filename.
// Returns empty strings if the filename does not match.
func ParseIPKFilename(filename string) (name, version string) {
	m := ipkFilenameRe.FindStringSubmatch(filepath.Base(filename))
	if m == nil {
		return "", ""
	}
	return m[1], m[3]
}

const maxIPKControlFileSize = 1 << 20

// ReadIPKControl reads the Package and Version fields from an IPK's control file.
func ReadIPKControl(ipkPath string) (name, version string, err error) {
	f, err := os.Open(ipkPath)
	if err != nil {
		return "", "", err
	}
	defer f.Close()

	var magic [len(arGlobalHeader)]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return "", "", fmt.Errorf("reading %s header: %w", ipkPath, err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", "", err
	}

	if string(magic[:]) == arGlobalHeader {
		name, version, err := readArIPKControl(f)
		if err != nil {
			return "", "", fmt.Errorf("reading %s as ar: %w", ipkPath, err)
		}
		if name == "" {
			return "", version, fmt.Errorf("Package field not found in %s", ipkPath)
		}
		return name, version, nil
	}

	name, version, err = readGzipTarIPKControl(f)
	if err != nil {
		return "", "", fmt.Errorf("reading %s as gzip tar: %w", ipkPath, err)
	}
	if name == "" {
		return "", version, fmt.Errorf("Package field not found in %s", ipkPath)
	}
	return name, version, nil
}

func readGzipTarIPKControl(r io.Reader) (name, version string, err error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return "", "", err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return "", "", fmt.Errorf("control.tar.gz not found")
		}
		if err != nil {
			return "", "", err
		}
		if hdr.FileInfo().IsDir() || cleanTarMemberName(hdr.Name) != "control.tar.gz" {
			continue
		}

		return readControlTarGz(tr)
	}
}

const arGlobalHeader = "!<arch>\n"

func readArIPKControl(r io.Reader) (name, version string, err error) {
	header := make([]byte, len(arGlobalHeader))
	if _, err := io.ReadFull(r, header); err != nil {
		return "", "", err
	}
	if string(header) != arGlobalHeader {
		return "", "", fmt.Errorf("invalid ar global header")
	}

	for {
		hdr := make([]byte, 60)
		if _, err := io.ReadFull(r, hdr); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return "", "", fmt.Errorf("control.tar.gz not found")
			}
			return "", "", err
		}
		if !bytes.Equal(hdr[58:60], []byte("`\n")) {
			return "", "", fmt.Errorf("invalid ar member header")
		}

		memberName := cleanArMemberName(string(hdr[0:16]))
		size, err := strconv.ParseInt(strings.TrimSpace(string(hdr[48:58])), 10, 64)
		if err != nil {
			return "", "", fmt.Errorf("invalid ar member size for %q: %w", memberName, err)
		}
		if size < 0 {
			return "", "", fmt.Errorf("invalid negative ar member size for %q", memberName)
		}

		lr := &io.LimitedReader{R: r, N: size}
		if memberName == "control.tar.gz" {
			return readControlTarGz(lr)
		}
		if _, err := io.Copy(io.Discard, lr); err != nil {
			return "", "", err
		}
		if size%2 != 0 {
			if _, err := io.CopyN(io.Discard, r, 1); err != nil {
				return "", "", err
			}
		}
	}
}

func readControlTarGz(r io.Reader) (name, version string, err error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return "", "", err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return "", "", fmt.Errorf("control file not found")
		}
		if err != nil {
			return "", "", err
		}
		if hdr.FileInfo().IsDir() || cleanTarMemberName(hdr.Name) != "control" {
			continue
		}

		if hdr.Size > maxIPKControlFileSize {
			return "", "", fmt.Errorf("control file too large: %d bytes exceeds %d", hdr.Size, maxIPKControlFileSize)
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxIPKControlFileSize+1))
		if err != nil {
			return "", "", err
		}
		if len(data) > maxIPKControlFileSize {
			return "", "", fmt.Errorf("control file too large: exceeds %d bytes", maxIPKControlFileSize)
		}
		name, version := parseControlFields(string(data))
		return name, version, nil
	}
}

func parseControlFields(control string) (name, version string) {
	for _, line := range strings.Split(control, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch key {
		case "Package":
			name = strings.TrimSpace(value)
		case "Version":
			version = strings.TrimSpace(value)
		}
	}
	return name, version
}

func cleanTarMemberName(name string) string {
	return strings.TrimPrefix(filepath.ToSlash(name), "./")
}

func cleanArMemberName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimSuffix(name, "/")
	return strings.TrimPrefix(filepath.ToSlash(name), "./")
}

// ApplicationManifestPath returns the application manifest path for an artifact directory.
func ApplicationManifestPath(dir string) string {
	return filepath.Join(dir, "application_manifest.txt")
}

// UpdateApplicationManifest appends or updates entries in application_manifest.txt
// for the given IPK files. Each entry is written as "<name> <version>".
// It returns the manifest path and whether the file changed.
func UpdateApplicationManifest(dir string, ipkPaths []string) (string, bool, error) {
	manifestPath := ApplicationManifestPath(dir)
	if len(ipkPaths) == 0 {
		return manifestPath, false, nil
	}

	// Parse name/version from each IPK.
	type entry struct{ name, version string }
	var entries []entry
	for _, p := range ipkPaths {
		name, version := ParseIPKFilename(filepath.Base(p))
		if name == "" {
			continue
		}
		entries = append(entries, entry{name, version})
	}
	if len(entries) == 0 {
		return manifestPath, false, nil
	}

	// Read existing manifest lines and index by package name.
	existing := make(map[string]bool)
	var lines []string
	if data, err := os.ReadFile(manifestPath); err == nil {
		for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			if line == "" {
				continue
			}
			lines = append(lines, line)
			parts := strings.Fields(line)
			if len(parts) >= 1 {
				existing[parts[0]] = true
			}
		}
	}

	// Update existing entries or append new ones.
	changed := false
	for _, e := range entries {
		newLine := fmt.Sprintf("%s %s", e.name, e.version)
		if existing[e.name] {
			for i, line := range lines {
				if parts := strings.Fields(line); len(parts) >= 1 && parts[0] == e.name {
					if line != newLine {
						lines[i] = newLine
						changed = true
					}
					break
				}
			}
		} else {
			lines = append(lines, newLine)
			existing[e.name] = true
			changed = true
		}
	}
	if !changed {
		return manifestPath, false, nil
	}

	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(manifestPath, []byte(content), 0644); err != nil {
		return manifestPath, false, err
	}
	return manifestPath, true, nil
}

// SHA256File computes the SHA256 hash of a file and returns it as a hex string.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

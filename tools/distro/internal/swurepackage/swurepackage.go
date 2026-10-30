// Package swurepackage rebuilds signed SWUs while preserving seed membership.
package swurepackage

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"proto-distro/tools/distro/internal/fileutil"
)

// Options configures an SWU repackage operation.
type Options struct {
	OldSWU          string // Path to the existing .swu file
	NewRootfs       string // Plain ext4 paired with mandatory AVB body and tail
	NewFIP          string // Replacement FIP when the seed contains a FIP
	AVBBody         string // Trusted body paired with plain ext4 and tail
	AVBTail         string // Trusted final sector paired with plain ext4 and body
	SoftwareVersion string // Explicit rootfs software version; empty preserves the seed version
	PrivateKey      string // RSA private key path (empty = use KeyDir default)
	Passfile        string // Key passphrase file path (empty = use KeyDir default)
	KeyDir          string // Directory containing signing keys (resolved path)
	Output          string // Output .swu path (empty = <old>_repackaged.swu)
}

// Run recreates one signed SWU, replacing only explicitly supplied components.
func Run(opts Options) error { return recreate(opts) }

func stageRootfs(opts Options, directory, name string) ([]imagePayload, error) {
	path := filepath.Join(directory, name)
	if err := gzipFile(opts.NewRootfs, path); err != nil {
		return nil, fmt.Errorf("compressing rootfs: %w", err)
	}
	hash, err := fileutil.SHA256File(path)
	if err != nil {
		return nil, err
	}
	metadata, err := copyAVBMetadata(opts.NewRootfs, opts.AVBBody, opts.AVBTail, directory, strings.TrimSuffix(name, ".gz"))
	if err != nil {
		return nil, fmt.Errorf("copying AVB metadata: %w", err)
	}
	return append([]imagePayload{{name: name, hash: hash, compressed: true}}, metadata...), nil
}

// listCPIO returns validated normalized archive member names.
func listCPIO(archive string) ([]string, error) {
	f, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	listing := exec.Command("cpio", "-it")
	listing.Stdin = f
	output, err := listing.Output()
	if err != nil {
		return nil, fmt.Errorf("listing archive: %w", err)
	}
	seen := make(map[string]bool)
	var names []string
	archiveNames := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	for _, raw := range archiveNames {
		name, err := fileutil.SafeMemberName(raw)
		if err != nil {
			return nil, err
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate archive member %s", name)
		}
		seen[name] = true
		names = append(names, name)
	}
	if !seen["sw-description"] {
		return nil, fmt.Errorf("SWU archive is missing sw-description")
	}
	// GNU and BSD cpio verbose listings start with mode and link count.
	// Check metadata before extraction: even a signature symlink could make
	// OpenSSL write outside the workspace. Hardlinks are not payload files.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	metadata := exec.Command("cpio", "-itv")
	metadata.Env = append(os.Environ(), "LC_ALL=C")
	metadata.Stdin = f
	verbose, err := metadata.Output()
	if err != nil {
		return nil, fmt.Errorf("listing archive metadata: %w", err)
	}
	entries := strings.Split(strings.TrimSuffix(string(verbose), "\n"), "\n")
	if len(entries) != len(names) {
		return nil, fmt.Errorf("archive member and metadata listings disagree")
	}
	for i, entry := range entries {
		fields := strings.Fields(entry)
		if len(fields) < 2 || len(fields[0]) != 10 {
			return nil, fmt.Errorf("invalid archive metadata for %q", names[i])
		}
		mode := fields[0][0]
		links, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil || (mode != '-' && mode != 'd') || (mode == '-' && links != 1) {
			return nil, fmt.Errorf("archive member must be a regular file without hardlinks or a directory: %q", names[i])
		}
	}
	return names, nil
}

func extractCPIO(archive, destDir string) ([]string, error) {
	names, err := listCPIO(archive)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// Recreated archive members use creation time, not timestamps from the seed.
	cmd := exec.Command("cpio", "-id")
	cmd.Dir = destDir
	cmd.Stdin = f
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return names, cmd.Run()
}

// copyPayload creates a new archive member without following or replacing one.
func copyPayload(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func gzipFile(src, dst string) error {
	cmd := exec.Command("gzip", "-c", src)
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0666)
	if err != nil {
		return err
	}
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func signFile(input, output, privateKey, passfile string) error {
	cmd := exec.Command("openssl", "dgst", "-sha256", "-sign", privateKey,
		"-passin", "file:"+passfile,
		"-out", output,
		input)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// createCPIO reports ownership of output even when packing fails.
func createCPIO(sourceDir, output string, payloadOrder []string) (bool, error) {
	// Keep manifest/signature/payload order, then walk unrelated members without following symlinks.
	var fileList []string
	fileList = append(fileList, "sw-description", "sw-description.sig")
	fileList = append(fileList, payloadOrder...)
	seen := make(map[string]bool, len(fileList))
	for _, name := range fileList {
		seen[name] = true
	}
	err := filepath.WalkDir(sourceDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == sourceDir {
			return nil
		}
		name, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		if !seen[name] {
			fileList = append(fileList, name)
		}
		return nil
	})
	if err != nil {
		return false, err
	}

	// Platform-specific cpio flags
	var cpioFlags string
	if runtime.GOOS == "linux" {
		cpioFlags = "-ov -H crc --reproducible"
	} else {
		cpioFlags = "-ov --format newc"
	}

	out, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0666)
	if err != nil {
		return false, err
	}
	defer out.Close()

	// Use sh -c to handle the flag splitting properly
	input := strings.Join(fileList, "\n")
	cmd := exec.Command("sh", "-c", fmt.Sprintf("cpio %s", cpioFlags))
	cmd.Dir = sourceDir
	cmd.Stdin = strings.NewReader(input)
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	return true, cmd.Run()
}

// prepare resolves defaults without extracting, hashing, signing, or creating files.
func prepare(opts Options) (Options, error) {
	if _, err := regularFile(opts.NewRootfs); err != nil {
		return opts, err
	}
	if strings.HasSuffix(strings.TrimSuffix(opts.NewRootfs, ".gz"), ".avb") {
		return opts, fmt.Errorf("full .avb rootfs sources are unsupported; supply plain ext4, AVBBody, and AVBTail")
	}
	if opts.NewRootfs == "" || opts.AVBBody == "" || opts.AVBTail == "" {
		return opts, fmt.Errorf("plain NewRootfs, AVBBody, and AVBTail are required")
	}
	if strings.HasSuffix(opts.NewRootfs, ".gz") {
		return opts, fmt.Errorf("AVB metadata requires an uncompressed rootfs")
	}
	raw, err := os.Open(opts.NewRootfs)
	if err != nil {
		return opts, err
	}
	var magic [2]byte
	_, readErr := io.ReadFull(raw, magic[:])
	closeErr := raw.Close()
	if readErr != nil {
		return opts, readErr
	}
	if closeErr != nil {
		return opts, closeErr
	}
	if magic == [2]byte{0x1f, 0x8b} {
		return opts, fmt.Errorf("AVB metadata requires an uncompressed rootfs")
	}
	if _, err := inspectAVBMetadata(opts.NewRootfs, opts.AVBBody, opts.AVBTail, filepath.Base(opts.NewRootfs)); err != nil {
		return opts, err
	}
	if opts.NewFIP != "" {
		if _, err := regularFile(opts.NewFIP); err != nil {
			return opts, err
		}
	}
	if opts.PrivateKey == "" && opts.KeyDir != "" {
		opts.PrivateKey = filepath.Join(opts.KeyDir, "priv.pem")
	}
	if opts.Passfile == "" && opts.KeyDir != "" {
		opts.Passfile = filepath.Join(opts.KeyDir, "passout")
	}
	if opts.PrivateKey == "" || opts.Passfile == "" {
		return opts, fmt.Errorf("signing key paths not provided and no KeyDir configured; use --swu-key-file/--swu-key-pass or configure signing.key_dir in distro.yaml")
	}
	for _, name := range []string{opts.OldSWU, opts.PrivateKey, opts.Passfile} {
		info, err := os.Stat(name)
		if err != nil {
			return opts, err
		}
		if !info.Mode().IsRegular() {
			return opts, fmt.Errorf("not a regular input file: %s", name)
		}
		f, err := os.Open(name)
		if err != nil {
			return opts, err
		}
		if err := f.Close(); err != nil {
			return opts, err
		}
	}
	if opts.Output == "" {
		opts.Output = strings.TrimSuffix(opts.OldSWU, ".swu") + "_repackaged.swu"
	}
	output, err := filepath.Abs(opts.Output)
	opts.Output = output
	return opts, err
}

// Package swu has minimal helpers for inspecting .swu (CPIO newc) archives
// without shelling out to an external cpio binary.
package swu

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

const (
	// newc (070701) and crc (070702) share the same 110-byte header layout;
	// only the magic and an optional checksum field distinguish them. Both
	// are produced by GNU cpio depending on platform/flags, so accept either.
	cpioNewcMagic  = "070701"
	cpioCRCMagic   = "070702"
	cpioHeaderSize = 110

	// maxNameSize caps the pathname length we will allocate per CPIO entry.
	// PATH_MAX-style sanity bound; protects against a malformed archive that
	// claims a multi-GB pathname.
	maxNameSize = 4096
)

// ErrNotFound is returned by FindFile when no entry matches the pattern.
// Callers can distinguish "absent group" from "malformed archive" via
// errors.Is(err, ErrNotFound).
var ErrNotFound = errors.New("entry not found")

// Has reports whether the archive at path contains an entry matching pattern.
// It returns (false, nil) only when the archive parses cleanly but no entry
// matches; malformed archives or I/O errors propagate so callers can fail
// closed.
func Has(path, pattern string) (bool, error) {
	if _, err := FindFile(path, pattern); err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Entry describes one file found inside a CPIO newc archive.
type Entry struct {
	Name       string
	Size       int64
	DataOffset int64 // absolute byte offset in the archive where file content begins
}

// FindFile scans path (a newc CPIO / SWU archive) and returns the first
// entry whose name matches pattern (filepath.Match glob).
func FindFile(path, pattern string) (Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return Entry{}, err
	}
	defer f.Close()

	var hdr [cpioHeaderSize]byte
	for {
		if _, err := io.ReadFull(f, hdr[:]); err != nil {
			return Entry{}, fmt.Errorf("read cpio header: %w", err)
		}
		magic := string(hdr[0:6])
		if magic != cpioNewcMagic && magic != cpioCRCMagic {
			return Entry{}, fmt.Errorf("not a newc/crc cpio archive (magic=%q)", magic)
		}
		size, err := parseHex(hdr[54:62])
		if err != nil {
			return Entry{}, fmt.Errorf("parse filesize: %w", err)
		}
		namesize, err := parseHex(hdr[94:102])
		if err != nil {
			return Entry{}, fmt.Errorf("parse namesize: %w", err)
		}
		if namesize == 0 || namesize > maxNameSize {
			return Entry{}, fmt.Errorf("invalid namesize %d in cpio header", namesize)
		}
		nameLen := int(namesize)

		name := make([]byte, nameLen)
		if _, err := io.ReadFull(f, name); err != nil {
			return Entry{}, fmt.Errorf("read name: %w", err)
		}
		if pad := (4 - (cpioHeaderSize+nameLen)%4) % 4; pad > 0 {
			if _, err := f.Seek(int64(pad), io.SeekCurrent); err != nil {
				return Entry{}, err
			}
		}
		if name[nameLen-1] == 0 {
			name = name[:nameLen-1]
		}
		if string(name) == "TRAILER!!!" {
			return Entry{}, fmt.Errorf("%w: pattern %q in %s", ErrNotFound, pattern, path)
		}

		dataOffset, err := f.Seek(0, io.SeekCurrent)
		if err != nil {
			return Entry{}, err
		}

		match, err := filepath.Match(pattern, string(name))
		if err != nil {
			return Entry{}, fmt.Errorf("bad pattern %q: %w", pattern, err)
		}
		if match {
			return Entry{
				Name:       string(name),
				Size:       int64(size),
				DataOffset: dataOffset,
			}, nil
		}

		skip := int64(size)
		if pad := (4 - int(size)%4) % 4; pad > 0 {
			skip += int64(pad)
		}
		if _, err := f.Seek(skip, io.SeekCurrent); err != nil {
			return Entry{}, err
		}
	}
}

func parseHex(b []byte) (uint32, error) {
	v, err := strconv.ParseUint(string(b), 16, 32)
	if err != nil {
		return 0, err
	}
	return uint32(v), nil
}

package swurepackage

import (
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"proto-distro/tools/distro/internal/fileutil"
)

func verify(archive string, components Components, expected map[string]string, expectedFIPHash string) error {
	directory, err := os.MkdirTemp("", "swu-verify-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	names, err := extractCPIO(archive, directory)
	if err != nil {
		return err
	}
	if len(names) < 2 || names[0] != "sw-description" || names[1] != "sw-description.sig" {
		return fmt.Errorf("SWU must start with sw-description and sw-description.sig")
	}
	actual, err := discoverComponents(names)
	if err != nil || actual != components {
		return fmt.Errorf("SWU component membership differs from expected")
	}
	for _, name := range []string{"sw-description", "sw-description.sig"} {
		if _, err := regularFile(filepath.Join(directory, name)); err != nil {
			return err
		}
	}
	hashes := make(map[string]string)
	offsets := make(map[string]int64)
	if components.Rootfs != "" && expected != nil {
		rootfs := strings.TrimSuffix(components.Rootfs, ".gz")
		for _, name := range []string{components.Rootfs, rootfs + ".avb.body", rootfs + ".avb.tail"} {
			if _, err := regularFile(filepath.Join(directory, name)); err != nil {
				return err
			}
		}
		for i, name := range names {
			if name == components.Rootfs && (i+2 >= len(names) || names[i+1] != rootfs+".avb.body" || names[i+2] != rootfs+".avb.tail") {
				return fmt.Errorf("SWU rootfs/body/tail must be adjacent and ordered")
			}
		}
		compressed, err := os.Open(filepath.Join(directory, components.Rootfs))
		if err != nil {
			return err
		}
		defer compressed.Close()
		compressedHash, rawHash := sha256.New(), sha256.New()
		reader, err := gzip.NewReader(io.TeeReader(compressed, compressedHash))
		if err != nil {
			return fmt.Errorf("rootfs gzip: %w", err)
		}
		defer reader.Close()
		rawSize, err := io.Copy(rawHash, reader)
		if err != nil {
			return fmt.Errorf("rootfs gzip: %w", err)
		}
		if fmt.Sprintf("%x", rawHash.Sum(nil)) != expected[rootfs] {
			return fmt.Errorf("SWU rootfs differs from replacement: %s", rootfs)
		}
		bodyInfo, err := regularFile(filepath.Join(directory, rootfs+".avb.body"))
		if err != nil {
			return err
		}
		tailInfo, err := regularFile(filepath.Join(directory, rootfs+".avb.tail"))
		if err != nil {
			return err
		}
		if !ValidAVBGeometry(uint64(rawSize), uint64(bodyInfo.Size()), uint64(tailInfo.Size())) {
			return fmt.Errorf("invalid SWU AVB geometry")
		}
		hashes[components.Rootfs] = fmt.Sprintf("%x", compressedHash.Sum(nil))
		offsets[components.Rootfs] = 0
		offsets[rootfs+".avb.body"] = rawSize
		offsets[rootfs+".avb.tail"] = avbPartitionSize - 512
		for _, suffix := range []string{".avb.body", ".avb.tail"} {
			name := rootfs + suffix
			hash, err := fileutil.SHA256File(filepath.Join(directory, name))
			if err != nil {
				return err
			}
			if hash != expected[name] {
				return fmt.Errorf("SWU AVB payload differs from replacement: %s", name)
			}
			hashes[name] = hash
		}
	}
	if components.FIP != "" {
		path := filepath.Join(directory, components.FIP)
		if _, err := regularFile(path); err != nil {
			return err
		}
		hash, err := fileutil.SHA256File(path)
		if err != nil {
			return err
		}
		if expectedFIPHash != "" && hash != expectedFIPHash {
			return fmt.Errorf("SWU FIP differs from replacement")
		}
		hashes[components.FIP] = hash
	} else if expectedFIPHash != "" {
		return fmt.Errorf("SWU is missing replacement FIP")
	}
	description, err := os.ReadFile(filepath.Join(directory, "sw-description"))
	if err != nil {
		return err
	}
	source := string(description)
	if components.Rootfs != "" && expected != nil {
		_, owned, err := rewriteDescription(source, components.Rootfs, []imagePayload{{name: components.Rootfs}})
		if err != nil {
			return err
		}
		if !owned {
			return fmt.Errorf("SWU manifest is missing split AVB records")
		}
	}
	tokens, err := configTokens(source)
	if err != nil {
		return err
	}
	if len(tokens) < 4 || tokens[0].text != "software" || (tokens[1].text != "=" && tokens[1].text != ":") || tokens[2].text != "{" {
		return fmt.Errorf("SWU manifest requires a software group")
	}
	end := tokens[2].mate + 1
	if end < len(tokens) && tokens[end].text == ";" {
		end++
	}
	if end != len(tokens) {
		return fmt.Errorf("unexpected content after software group")
	}
	fipReferences := 0
	for i, token := range tokens {
		if token.text != "{" {
			continue
		}
		record, err := recordFields(tokens, i)
		if err != nil {
			return err
		}
		if _, ok := record.fields["filename"]; !ok {
			continue
		}
		name, err := verifyManifestPayload(tokens, record, directory, hashes)
		if err != nil {
			return err
		}
		if name == components.FIP {
			fipReferences++
		}
		if offset, ok := offsets[name]; ok {
			value, err := fieldString(tokens, record, "offset")
			if err != nil || value != strconv.FormatInt(offset, 10) {
				return fmt.Errorf("incorrect SWU offset for %s", name)
			}
			typ, err := fieldString(tokens, record, "type")
			if err != nil || typ != "raw" {
				return fmt.Errorf("SWU payload %s requires raw type", name)
			}
			if name == components.Rootfs {
				compression, err := fieldString(tokens, record, "compressed")
				if err != nil || compression != "zlib" {
					return fmt.Errorf("SWU rootfs requires zlib compression")
				}
			} else if _, ok := record.fields["compressed"]; ok {
				return fmt.Errorf("SWU AVB payload %s must be uncompressed", name)
			}
		}
	}
	if components.FIP != "" && fipReferences == 0 {
		return fmt.Errorf("SWU manifest does not reference replacement FIP")
	}
	return nil
}

func verifyManifestPayload(tokens []configToken, record imageRecord, directory string, hashes map[string]string) (string, error) {
	name, err := fieldString(tokens, record, "filename")
	if err != nil {
		return "", err
	}
	clean, err := fileutil.SafeMemberName(name)
	if err != nil || clean != name {
		return "", fmt.Errorf("unsafe manifest filename %q", name)
	}
	want, err := fieldString(tokens, record, "sha256")
	if err != nil {
		return "", err
	}
	hash, ok := hashes[name]
	if !ok {
		path := filepath.Join(directory, name)
		if _, err := regularFile(path); err != nil {
			return "", err
		}
		hash, err = fileutil.SHA256File(path)
		if err != nil {
			return "", err
		}
		if hashes != nil {
			hashes[name] = hash
		}
	}
	if !strings.EqualFold(want, hash) {
		return "", fmt.Errorf("SWU manifest hash mismatch for %s", name)
	}
	return name, nil
}

package distroinject

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"proto-distro/tools/distro/internal/config"
	"proto-distro/tools/distro/internal/docker"
)

// avbKeys determines policy from the inner rootfs, never its enclosing archive.
func avbKeys(rootfs string, opts Options) (*config.AVBKeys, error) {
	name := filepath.Base(rootfs)
	hasOutput := false
	for _, suffix := range []string{".avb.body", ".avb.tail", ".avb", ".avb.footer"} {
		path := rootfs + suffix
		output, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("checking AVB output: %w", err)
		}
		hasOutput = true
		if !output.Mode().IsRegular() {
			return nil, fmt.Errorf("AVB output must be a regular file: %s", path)
		}
		if stat, ok := output.Sys().(*syscall.Stat_t); ok && stat.Nlink > 1 {
			return nil, fmt.Errorf("AVB output has hard-link aliases: %s", path)
		}
	}

	variant := ""
	for _, candidate := range []string{"release", "mfg"} {
		if strings.HasPrefix(name, "miner-image-"+candidate+"-") {
			variant = candidate
		}
	}
	machine := opts.Machine
	if opts.Variant != "" || machine != "" {
		if opts.Variant == "" || machine == "" {
			return nil, fmt.Errorf("artifact identity requires both variant and machine")
		}
		if variant != "" && !strings.HasPrefix(name, "miner-image-"+opts.Variant+"-"+machine+"-") {
			return nil, fmt.Errorf("rootfs %s contradicts variant %q machine %q", name, opts.Variant, machine)
		}
		variant = opts.Variant
	} else if variant == "release" || variant == "mfg" {
		matches := 0
		for candidate := range opts.AVB {
			if candidate != "" && strings.HasPrefix(name, "miner-image-"+variant+"-"+candidate+"-") {
				machine = candidate
				matches++
			}
		}
		if matches != 1 {
			return nil, fmt.Errorf("%s rootfs %s must match exactly one configured AVB machine (matched %d); use a canonical miner-image-%s-<machine>- filename", variant, name, matches, variant)
		}
	}
	if variant != "release" && variant != "mfg" {
		if hasOutput {
			return nil, fmt.Errorf("signed rootfs %s is not identified as release or mfg; use a canonical miner-image-<variant>-<machine>- filename", name)
		}
		return nil, nil
	}
	keys, ok := opts.AVB[machine]
	if !ok {
		return nil, fmt.Errorf("AVB keys missing for machine %q", machine)
	}
	for _, field := range []struct {
		name string
		path *string
	}{
		{"private_key", &keys.PrivateKey}, {"public_key", &keys.PublicKey}, {"avb_public_key", &keys.AVBPublicKey},
	} {
		if *field.path == "" {
			return nil, fmt.Errorf("avb.%s.%s is required", machine, field.name)
		}
		path, err := filepath.Abs(config.ResolvePath(opts.ConfigPath, *field.path))
		if err == nil {
			path, err = filepath.EvalSymlinks(path)
		}
		if err != nil {
			return nil, fmt.Errorf("avb.%s.%s: %w", machine, field.name, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("avb.%s.%s: %w", machine, field.name, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("avb.%s.%s must be a regular file", machine, field.name)
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("avb.%s.%s: %w", machine, field.name, err)
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
		*field.path = path
	}
	return &keys, nil
}

// createAVB invokes the installed CLI only after the plain filesystem is final.
func createAVB(ctx context.Context, rootfs, image string, keys config.AVBKeys) error {
	stage, err := os.MkdirTemp(filepath.Dir(rootfs), ".distro-avb-*")
	if err != nil {
		return fmt.Errorf("creating AVB staging directory: %w", err)
	}
	defer os.RemoveAll(stage)
	stagedRootfs := filepath.Join(stage, filepath.Base(rootfs))
	if err := os.Symlink("../"+filepath.Base(rootfs), stagedRootfs); err != nil {
		return fmt.Errorf("staging AVB input: %w", err)
	}
	mount := func(src, dst string, readonly bool) string {
		var b strings.Builder
		w := csv.NewWriter(&b)
		fields := []string{"type=bind", "src=" + src, "dst=" + dst}
		if readonly {
			fields = append(fields, "readonly")
		}
		_ = w.Write(fields)
		w.Flush()
		return strings.TrimSuffix(b.String(), "\n")
	}
	args := []string{"run", "--rm", "--network", "none", "--entrypoint", "create_avb_image",
		"--mount", mount(filepath.Dir(rootfs), "/images", false),
		"--mount", mount(keys.PrivateKey, "/keys/private.pem", true),
		"--mount", mount(keys.PublicKey, "/keys/public.pem", true),
		"--mount", mount(keys.AVBPublicKey, "/keys/public.avbpubkey", true),
		image, "--rootfs-image", "/images/" + filepath.Base(stage) + "/" + filepath.Base(rootfs),
		"--privkey", "/keys/private.pem", "--pubkey", "/keys/public.pem",
		"--avbpubkey", "/keys/public.avbpubkey",
	}
	if err := docker.Run(ctx, args, docker.RunOptions{Stdout: os.Stdout, Stderr: os.Stderr}); err != nil {
		return fmt.Errorf("creating AVB for %s: %w", rootfs, err)
	}
	sizes := make([]int64, 0, 3)
	for _, path := range []string{rootfs, stagedRootfs + ".avb.body", stagedRootfs + ".avb.tail"} {
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("checking AVB file %s: %w; rebuild the injector with just docker-build-injector", path, err)
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("AVB file must be a nonempty regular file: %s; rebuild the injector with just docker-build-injector", path)
		}
		sizes = append(sizes, info.Size())
	}
	// U-Boot rootfs_part_size is 0x200000 512-byte sectors (1 GiB), defined in
	// sources/meta-miner/recipes-bsp/u-boot/files/0002-add-default-boot-partition-layout-env-variables.patch.
	// create_avb_image reserves the final sector for its footer-containing tail;
	// raw/body sizes must be sector-aligned and end at or before this byte offset.
	const tailOffset = int64(1073741824 - 512)
	if sizes[0]%512 != 0 || sizes[1]%512 != 0 || sizes[2] != 512 || sizes[0] > tailOffset || sizes[1] > tailOffset-sizes[0] {
		return fmt.Errorf("invalid AVB file geometry; rebuild the injector with just docker-build-injector")
	}
	for _, suffix := range []string{".avb", ".avb.footer"} {
		if _, err := os.Lstat(stagedRootfs + suffix); !os.IsNotExist(err) {
			return fmt.Errorf("unexpected legacy AVB output or inaccessible path %s: %v; rebuild the injector with just docker-build-injector", stagedRootfs+suffix, err)
		}
	}
	// Validate both files before replacing either. Each rename is atomic, not the pair.
	for _, suffix := range []string{".avb.body", ".avb.tail"} {
		if err := os.Rename(stagedRootfs+suffix, rootfs+suffix); err != nil {
			return fmt.Errorf("publishing AVB metadata: %w", err)
		}
	}
	return nil
}

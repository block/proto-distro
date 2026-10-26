package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"proto-distro/tools/flashimage"
)

// All reads remain bounded and cancellable; large images are never buffered.
type contextImage struct {
	ctx    context.Context
	source io.ReaderAt
}

func (r contextImage) ReadAt(data []byte, offset int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.ReadAt(data, offset)
}

func preparePayloads(ctx context.Context, plan flashPlan, paths map[string]string, verity bool) (items []payload, cleanup func() error, result error) {
	files := make(map[string]*os.File)
	cleanup = func() error {
		var result error
		for path, file := range files {
			result = errors.Join(result, file.Close())
			delete(files, path)
		}
		return result
	}
	defer func() {
		if result != nil {
			result = errors.Join(result, cleanup())
		}
	}()
	open := func(path string) (*os.File, int64, error) {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		if path == "" {
			return nil, 0, errors.New("missing required artifact")
		}
		file := files[path]
		if file == nil {
			// Check before opening so a FIFO cannot block preflight.
			info, err := os.Stat(path)
			if err != nil {
				return nil, 0, err
			}
			if !info.Mode().IsRegular() {
				return nil, 0, fmt.Errorf("artifact %s is not a regular file", path)
			}
			file, err = os.Open(path)
			if err != nil {
				return nil, 0, err
			}
			files[path] = file
		}
		info, err := file.Stat()
		if err != nil {
			return nil, 0, err
		}
		if !info.Mode().IsRegular() || info.Size() <= 0 {
			return nil, 0, fmt.Errorf("artifact %s must be a nonempty regular file", path)
		}
		return file, info.Size(), nil
	}
	for _, p := range plan.partitions {
		if !p.writes() {
			continue
		}
		path := paths[p.ArtifactKey]
		file, size, err := open(path)
		if err != nil {
			return nil, cleanup, fmt.Errorf("%s: %w", p.Name, err)
		}
		total, err := paddedSize(size)
		if err != nil {
			return nil, cleanup, fmt.Errorf("%s: %w", path, err)
		}
		ranges := []extent{{offset: 0, length: total, source: contextImage{ctx, paddedImage{file, size, total}}, fileName: filepath.Base(file.Name()), fileSize: size}}
		if verity && p.AVB {
			if p.capacity <= 0 {
				return nil, cleanup, fmt.Errorf("AVB %s requires a known fixed partition capacity", p.Name)
			}
			var metadata [2]*os.File
			var lengths [2]int64
			for i, suffix := range [...]string{"body", "tail"} {
				metadataPath := paths["avb_"+suffix]
				if metadataPath != path+".avb."+suffix {
					return nil, cleanup, fmt.Errorf("%s requires its same-name .avb.%s", path, suffix)
				}
				metadata[i], lengths[i], err = open(metadataPath)
				if err != nil {
					return nil, cleanup, err
				}
			}
			bodyOffset, tailOffset, err := flashimage.ValidateAVBSizes(p.capacity, size, lengths[0], lengths[1])
			if err != nil {
				return nil, cleanup, fmt.Errorf("%s: %w", p.Name, err)
			}
			for i, offset := range [...]int64{bodyOffset, tailOffset} {
				ranges = append(ranges, extent{offset: offset, length: lengths[i], source: contextImage{ctx, metadata[i]}, fileName: filepath.Base(metadata[i].Name()), fileSize: lengths[i]})
			}
			total = p.capacity
		}
		item := payload{part: p, size: total, extents: ranges}
		if err := validatePayload(item); err != nil {
			return nil, cleanup, err
		}
		items = append(items, item)
	}
	return items, cleanup, nil
}

func (item payload) logPrepared() {
	var written int64
	for _, x := range item.extents {
		written += x.length
	}
	logInfo("Prepared %s: %d write bytes across %d write ranges", item.part.Name, written, len(item.extents))
	for _, x := range item.extents {
		logInfo("  - %q (%d bytes)", x.fileName, x.fileSize)
	}
}

func flashImage(ctx context.Context, profile flashimage.Profile, imagePath string, opts flashOptions) (result error) {
	// Options are checked before extraction, USB access, or any possible write.
	plan, err := buildFlashPlan(profile, opts)
	if err != nil {
		return err
	}
	dir, cleanup, err := prepareImage(imagePath)
	if err != nil {
		return err
	}
	defer cleanup()
	session, stage, err := openSession(ctx)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, session.Close()) }()
	logInfo("Detected %s on USB bus %d port %v", stage, session.identity.bus, session.identity.path)
	paths, err := getImageFiles(profile, dir, requiredArtifacts(plan, stage, opts.verity), opts.mmcDev, opts.signed)
	if err != nil {
		return err
	}
	items, closeImages, err := preparePayloads(ctx, plan, paths, opts.verity)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, closeImages()) }()
	if err := validateBootstrapImages(stage, paths); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, item := range items {
		item.logPrepared()
	}
	if err := bootstrap(ctx, session, stage, paths); err != nil {
		return err
	}
	return (&stm32progClient{session: session, skipVerify: opts.skipVerify}).program(ctx, plan, items)
}

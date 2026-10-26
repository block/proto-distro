package fastboot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/bits"
	"strconv"
	"strings"
	"time"

	"proto-distro/tools/go_fastboot/internal/fastboot/udp"
)

const (
	nativeDefaultTimeout    = time.Minute
	nativeFlashTimeout      = 3 * time.Minute
	nativeResponsiveTimeout = 5 * time.Second
	nativeWaitSleepDelay    = 2 * time.Second
)

const MB = 1 << 20

// FlashResult contains information about a flash operation.
type FlashResult struct {
	Partition string
	ImagePath string
	Duration  time.Duration
	Error     error
}

// NativeClient provides a pure Go fastboot client over UDP.
type NativeClient struct {
	ip        string
	port      int
	timeout   time.Duration
	transport *udp.Transport
	protocol  *Protocol
}

// NewNativeClient creates a new native fastboot client for the given IP.
func NewNativeClient(ip string) *NativeClient {
	return &NativeClient{
		ip:      ip,
		port:    udp.DefaultPort,
		timeout: nativeDefaultTimeout,
	}
}

// WithTimeout sets a custom timeout for commands.
func (c *NativeClient) WithTimeout(timeout time.Duration) *NativeClient {
	c.timeout = timeout
	return c
}

// WithPort sets a custom port (default 5554).
func (c *NativeClient) WithPort(port int) *NativeClient {
	c.port = port
	return c
}

// connect establishes connection if not already connected.
func (c *NativeClient) connect(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.transport != nil && c.transport.IsConnected() {
		return nil
	}

	c.transport = udp.NewTransport(c.ip).WithPort(c.port)
	if err := c.transport.Connect(ctx); err != nil {
		c.transport = nil
		return err
	}

	c.protocol = NewProtocol(c.transport)
	return nil
}

// disconnect closes the connection.
func (c *NativeClient) disconnect() {
	if c.transport != nil {
		if err := c.transport.Close(); err != nil {
			slog.Debug("failed to close transport", "error", err, "ip", c.ip)
		}
		c.transport = nil
		c.protocol = nil
	}
}

// CheckResponsive checks if the fastboot server is responding.
func (c *NativeClient) CheckResponsive(ctx context.Context) (err error) {
	ctx, cancel := context.WithTimeout(ctx, min(c.timeout, nativeResponsiveTimeout))
	defer cancel()
	defer func() { err = errors.Join(err, ctx.Err()) }()

	if err := c.connect(ctx); err != nil {
		return err
	}
	defer c.disconnect()

	_, err = c.protocol.GetVar(ctx, "product")
	return err
}

// GetVar retrieves a fastboot variable.
func (c *NativeClient) GetVar(ctx context.Context, name string) (value string, err error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	defer func() { err = errors.Join(err, ctx.Err()) }()

	if err := c.connect(ctx); err != nil {
		return "", err
	}
	defer c.disconnect()

	return c.protocol.GetVar(ctx, name)
}

// GetProduct returns the product name.
func (c *NativeClient) GetProduct(ctx context.Context) (string, error) {
	return c.GetVar(ctx, "product")
}

// GetVersion returns the bootloader version.
func (c *NativeClient) GetVersion(ctx context.Context) (string, error) {
	return c.GetVar(ctx, "version")
}

// GetSerialNo returns the serial number.
func (c *NativeClient) GetSerialNo(ctx context.Context) (string, error) {
	return c.GetVar(ctx, "serialno")
}

// Reboot reboots the device (exits fastboot mode).
func (c *NativeClient) Reboot(ctx context.Context) (err error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	defer func() { err = errors.Join(err, ctx.Err()) }()

	if err := c.connect(ctx); err != nil {
		return err
	}
	defer c.disconnect()

	if err := ctx.Err(); err != nil {
		return err
	}
	return c.protocol.Reboot(ctx)
}

// Erase erases a partition.
func (c *NativeClient) Erase(ctx context.Context, partition string) (err error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	defer func() { err = errors.Join(err, ctx.Err()) }()

	if err := c.connect(ctx); err != nil {
		return err
	}
	defer c.disconnect()

	if err := ctx.Err(); err != nil {
		return err
	}
	return c.protocol.Erase(ctx, partition)
}

// Flash flashes an image using one bounded payload buffer and one connection.
func (c *NativeClient) Flash(ctx context.Context, partition, imagePath string, timeout time.Duration, progress func(string)) (result *FlashResult, err error) {
	if timeout == 0 {
		timeout = nativeFlashTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	result = &FlashResult{Partition: partition, ImagePath: imagePath}
	defer func() {
		err = errors.Join(err, ctx.Err())
		result.Error = err
		result.Duration = time.Since(start)
	}()
	defer c.disconnect()
	limit, err := c.getMaxDownloadSize(ctx)
	if err != nil {
		return result, err
	}
	image, splits, rawSize, err := planFlashImage(ctx, imagePath, limit)
	if err != nil {
		return result, err
	}
	defer image.Close()
	if rawSize != 0 {
		return c.flashRawDirect(ctx, partition, image, rawSize, progress, start, result)
	}
	return c.flashParts(ctx, partition, image, splits, progress, start, result)
}

// getMaxDownloadSize leaves its connection open for the caller's transfer.
func (c *NativeClient) getMaxDownloadSize(ctx context.Context) (size int64, err error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	defer func() { err = errors.Join(err, ctx.Err()) }()
	if err := c.connect(ctx); err != nil {
		return 0, fmt.Errorf("connecting: %w", err)
	}
	value, err := c.protocol.GetVar(ctx, "max-download-size")
	if err != nil {
		return 0, fmt.Errorf("querying max-download-size: %w", err)
	}
	advertised, err := parseHexSize(value)
	if err != nil {
		return 0, fmt.Errorf("invalid max-download-size %q: %w", value, err)
	}
	return effectiveDownloadSize(advertised)
}

// ValidateImages checks all source metadata before any destructive operation.
func (c *NativeClient) ValidateImages(ctx context.Context, paths []string) (err error) {
	defer func() { err = errors.Join(err, ctx.Err()) }()
	defer c.disconnect()
	limit, err := c.getMaxDownloadSize(ctx)
	if err != nil {
		return err
	}
	for _, path := range paths {
		image, _, _, err := planFlashImage(ctx, path, limit)
		if err != nil {
			return fmt.Errorf("validating %s: %w", path, err)
		}
		if err := image.Close(); err != nil {
			return fmt.Errorf("closing %s: %w", path, err)
		}
	}
	return ctx.Err()
}

// planFlashImage returns metadata only. rawSize is nonzero for direct transfers.
func planFlashImage(ctx context.Context, path string, limit int64) (*SparseImage, []SplitInfo, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, 0, err
	}
	isSparse, err := IsSparseImage(path)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("checking image type: %w", err)
	}
	var image *SparseImage
	if isSparse {
		image, err = OpenSparseImage(path)
	} else {
		image, err = openRawImage(path)
	}
	if err != nil {
		return nil, nil, 0, err
	}
	fail := func(err error) (*SparseImage, []SplitInfo, int64, error) {
		image.Close()
		return nil, nil, 0, err
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if !isSparse {
		info, err := image.file.Stat()
		if err != nil {
			return fail(err)
		}
		if info.Size() <= limit {
			return image, nil, info.Size(), nil
		}
	}
	splits, err := image.CalculateSplits(limit)
	if err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	return image, splits, 0, nil
}

func (c *NativeClient) flashRawDirect(ctx context.Context, partition string, image *SparseImage, size int64, progress func(string), start time.Time, result *FlashResult) (*FlashResult, error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	data := make([]byte, int(size))
	if _, err := image.file.ReadAt(data, 0); err != nil {
		return result, fmt.Errorf("reading image: %w", err)
	}
	if progress != nil {
		progress("Downloading image to device...")
	}
	lastPct := -1
	err := c.protocol.Download(ctx, data, func(sent, total int) {
		if progress != nil {
			pct := nativeProgressPercent(int64(sent), int64(total))
			if pct/10 != lastPct/10 {
				lastPct = pct
				progress(fmt.Sprintf("\rSending... %d%%", pct))
			}
		}
	})
	if err != nil {
		return result, fmt.Errorf("download: %w", err)
	}
	if progress != nil {
		progress("Flashing partition...")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := c.protocol.Flash(ctx, partition); err != nil {
		return result, fmt.Errorf("flash: %w", err)
	}
	if progress != nil {
		progress(fmt.Sprintf("Flash complete (%.1fs)", time.Since(start).Seconds()))
	}
	return result, nil
}

// flashParts builds and consumes one part before reusing its payload buffer.
func (c *NativeClient) flashParts(ctx context.Context, partition string, image *SparseImage, splits []SplitInfo, progress func(string), start time.Time, result *FlashResult) (*FlashResult, error) {
	var totalBytes, maxWireSize int64
	for _, split := range splits {
		if split.WireSize <= 0 || split.WireSize > math.MaxInt64-totalBytes {
			return result, fmt.Errorf("invalid cumulative sparse wire size")
		}
		totalBytes += split.WireSize
		maxWireSize = max(maxWireSize, split.WireSize)
	}
	if totalBytes == 0 {
		return result, fmt.Errorf("empty sparse transfer")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	buffer := make([]byte, int(maxWireSize))
	if progress != nil {
		progress(fmt.Sprintf("Beginning sparse image flashing (%d parts)", len(splits)))
	}
	var bytesSent int64
	for i, split := range splits {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		data, err := image.BuildSplitImage(split, buffer)
		if err != nil {
			return result, fmt.Errorf("building split %d: %w", i+1, err)
		}
		lastPct := -1
		err = c.protocol.Download(ctx, data, func(sent, _ int) {
			if progress != nil {
				pct := nativeProgressPercent(bytesSent+int64(sent), totalBytes)
				if pct != lastPct {
					lastPct = pct
					progress(fmt.Sprintf("\rProgress (%d%%) - Downloading part %d/%d", pct, i+1, len(splits)))
				}
			}
		})
		if err != nil {
			return result, fmt.Errorf("download part %d: %w", i+1, err)
		}
		bytesSent += int64(len(data))
		if progress != nil {
			progress(fmt.Sprintf("\rProgress (%d%%) - Flashing part %d/%d", nativeProgressPercent(bytesSent, totalBytes), i+1, len(splits)))
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := c.protocol.Flash(ctx, partition); err != nil {
			return result, fmt.Errorf("flash part %d: %w", i+1, err)
		}
	}
	if progress != nil {
		progress(fmt.Sprintf("Flash complete (%.1fs)", time.Since(start).Seconds()))
	}
	return result, nil
}

func nativeProgressPercent(sent, total int64) int {
	hi, lo := bits.Mul64(uint64(sent), 100)
	percent, _ := bits.Div64(hi, lo, uint64(total))
	return int(percent)
}

// parseHexSize accepts only a positive, complete hexadecimal uint64.
func parseHexSize(value string) (uint64, error) {
	if strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0X") {
		value = value[2:]
	}
	size, err := strconv.ParseUint(value, 16, 64)
	if err != nil {
		return 0, err
	}
	if size == 0 {
		return 0, fmt.Errorf("size must be positive")
	}
	return size, nil
}

func effectiveDownloadSize(advertised uint64) (int64, error) {
	if advertised == 0 {
		return 0, fmt.Errorf("max-download-size must be positive")
	}
	// ponytail: cap host payload memory at 256 MiB; streaming transport is needed to raise it.
	return int64(min(advertised, uint64(256*MB), uint64(math.MaxUint32))), nil
}

// WaitForDevice waits for the fastboot device to become available.
func (c *NativeClient) WaitForDevice(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for ctx.Err() == nil {
		if err := c.CheckResponsive(ctx); err == nil {
			return nil
		}
		timer := time.NewTimer(nativeWaitSleepDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	return fmt.Errorf("waiting for fastboot device at %s: %w", c.ip, ctx.Err())
}

// CheckFastbootReachable checks if a fastboot device is reachable at the given IP.
func CheckFastbootReachable(ctx context.Context, ip string, timeout time.Duration) bool {
	client := NewNativeClient(ip).WithTimeout(timeout)
	return client.CheckResponsive(ctx) == nil
}

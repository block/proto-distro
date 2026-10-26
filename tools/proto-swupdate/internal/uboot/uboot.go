package uboot

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// GetEnv reads a U-Boot environment variable via fw_printenv.
func GetEnv(name string) (string, error) {
	out, err := exec.Command("fw_printenv", "-n", name).Output()
	if err != nil {
		return "", fmt.Errorf("fw_printenv %s: %w", name, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// SetEnvs sets several U-Boot environment variables in one environment write
// via `fw_setenv -s -`, so a power cut cannot leave only some of them set.
func SetEnvs(vars map[string]string) error {
	names := make([]string, 0, len(vars))
	for name, value := range vars {
		if name == "" || strings.ContainsAny(name, "=\n") || strings.Contains(value, "\n") {
			return fmt.Errorf("invalid U-Boot variable %q=%q", name, value)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	var script strings.Builder
	for _, name := range names {
		fmt.Fprintf(&script, "%s=%s\n", name, vars[name])
	}
	cmd := exec.Command("fw_setenv", "-s", "-")
	cmd.Stdin = strings.NewReader(script.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("fw_setenv -s %q: %w: %s", script.String(), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// devmemMapBytes returns the page-aligned byte slice covering addr's 4-byte
// register, plus the offset into that slice where the register lives. The
// caller must syscall.Munmap the returned slice. The mapping is sized to
// guarantee offset+4 <= len(data), and the address is checked for 4-byte
// alignment so reads/writes via encoding/binary are well-defined on ARM.
func devmemMapBytes(f *os.File, addr uint32, prot int) ([]byte, int, error) {
	if addr%4 != 0 {
		return nil, 0, fmt.Errorf("address 0x%08x not 4-byte aligned", addr)
	}
	pageSize := uint32(os.Getpagesize())
	pageBase := addr & ^(pageSize - 1)
	offset := int(addr - pageBase)
	mapLen := int(pageSize)
	// If the register would straddle the page boundary, extend the mapping.
	if offset+4 > mapLen {
		mapLen += int(pageSize)
	}
	data, err := syscall.Mmap(int(f.Fd()), int64(pageBase), mapLen, prot, syscall.MAP_SHARED)
	if err != nil {
		return nil, 0, fmt.Errorf("mmap 0x%08x: %w", addr, err)
	}
	if offset+4 > len(data) {
		syscall.Munmap(data)
		return nil, 0, fmt.Errorf("mapping too small for register at 0x%08x", addr)
	}
	return data, offset, nil
}

// ReadDevmem reads a 32-bit value from a physical address via /dev/mem.
func ReadDevmem(addr uint32) (uint32, error) {
	f, err := os.OpenFile("/dev/mem", os.O_RDONLY|syscall.O_SYNC, 0)
	if err != nil {
		return 0, fmt.Errorf("open /dev/mem: %w", err)
	}
	defer f.Close()

	data, offset, err := devmemMapBytes(f, addr, syscall.PROT_READ)
	if err != nil {
		return 0, err
	}
	defer syscall.Munmap(data)

	return binary.LittleEndian.Uint32(data[offset : offset+4]), nil
}

// WriteDevmem writes a 32-bit value to a physical address via /dev/mem.
func WriteDevmem(addr, value uint32) error {
	f, err := os.OpenFile("/dev/mem", os.O_RDWR|syscall.O_SYNC, 0)
	if err != nil {
		return fmt.Errorf("open /dev/mem: %w", err)
	}
	defer f.Close()

	data, offset, err := devmemMapBytes(f, addr, syscall.PROT_READ|syscall.PROT_WRITE)
	if err != nil {
		return err
	}
	defer syscall.Munmap(data)

	binary.LittleEndian.PutUint32(data[offset:offset+4], value)
	return nil
}

// ClearBootcount writes 0 to the bootcount address.
func ClearBootcount(addr uint32) error {
	return WriteDevmem(addr, 0)
}

// ReadFWUBootInfo reads the TAMP backup register and extracts boot bank and trial count.
func ReadFWUBootInfo(addr uint32) (bootBank uint32, trialCount uint32, err error) {
	val, err := ReadDevmem(addr)
	if err != nil {
		return 0, 0, err
	}
	bootBank = val & 0xF
	trialCount = (val >> 4) & 0xF
	return bootBank, trialCount, nil
}

// ActivePartition returns the partition number of the currently mounted root filesystem.
func ActivePartition() (string, error) {
	out, err := exec.Command("findmnt", "-n", "-o", "MAJ:MIN", "/").Output()
	if err != nil {
		return "", fmt.Errorf("findmnt: %w", err)
	}
	device := strings.TrimSpace(string(out))
	if !deviceNumberPattern.MatchString(device) {
		return "", fmt.Errorf("findmnt returned invalid device number %q", device)
	}
	return partitionNumber(filepath.Join("/sys/dev/block", device))
}

var deviceNumberPattern = regexp.MustCompile(`^[0-9]+:[0-9]+$`)

// partitionNumber resolves a partition or a one-layer dm-verity mapping with
// one shared data/tree partition. Other device stacks are not supported.
func partitionNumber(devicePath string) (string, error) {
	path := filepath.Join(devicePath, "partition")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		slavesPath := filepath.Join(devicePath, "slaves")
		slaves, readErr := os.ReadDir(slavesPath)
		if readErr != nil {
			return "", fmt.Errorf("read slaves for %s: %w", devicePath, readErr)
		}
		if len(slaves) != 1 {
			return "", fmt.Errorf("%s has %d slaves; require exactly one partition", devicePath, len(slaves))
		}
		path = filepath.Join(slavesPath, slaves[0].Name(), "partition")
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return "", fmt.Errorf("read partition %s: %w", path, err)
	}
	value := strings.TrimSpace(string(data))
	number, err := strconv.ParseUint(value, 10, 64)
	if err != nil || number == 0 || value[0] < '0' || value[0] > '9' {
		return "", fmt.Errorf("invalid partition number %q in %s", value, path)
	}
	return strconv.FormatUint(number, 10), nil
}

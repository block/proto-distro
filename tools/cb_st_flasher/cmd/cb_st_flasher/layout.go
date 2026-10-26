package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"proto-distro/tools/flashimage"
)

type partition struct {
	flashimage.Partition
	device, operation string
	capacity          int64 // Zero means no fixed capacity is known.
}

type flashPlan struct{ partitions []partition }

type flashOptions struct {
	mmcDev     int
	mode       flashMode
	partitions []string
	eraseAll   bool
	signed     bool
	verity     bool
	skipVerify bool
}

func buildFlashPlan(profile flashimage.Profile, opts flashOptions) (flashPlan, error) {
	if err := profile.Validate(); err != nil {
		return flashPlan{}, err
	}
	if opts.mmcDev != 0 && opts.mmcDev != 1 {
		return flashPlan{}, fmt.Errorf("unsupported MMC device %d: choose 0 or 1", opts.mmcDev)
	}
	mode := opts.mode
	if mode == "" {
		mode = flashModeFull
	}
	switch mode {
	case flashModeFull, flashModeRootfsOnly, flashModeNoRootfs:
	default:
		return flashPlan{}, fmt.Errorf("unsupported flash mode %q", mode)
	}
	partitions, err := normalizePartitions(profile, opts.partitions)
	if err != nil {
		return flashPlan{}, err
	}

	device := "mmc" + strconv.Itoa(opts.mmcDev)
	if opts.eraseAll {
		plan := flashPlan{partitions: make([]partition, profile.ErasePartitionCount())}
		for i := range plan.partitions {
			row, _ := profile.ErasePartition(i)
			plan.partitions[i] = partition{Partition: flashimage.Partition{Name: row.Name, Area: row.Area, Offset: row.Offset, Phase: row.Phase, DFUType: row.DFUType, DefaultOperation: row.DefaultOperation}, device: device, operation: row.DefaultOperation}
		}
		return plan, nil
	}

	plan := flashPlan{partitions: make([]partition, profile.PartitionCount())}
	for i := range plan.partitions {
		row, _ := profile.Partition(i)
		capacity, _ := profile.FixedCapacity(row.Name)
		plan.partitions[i] = partition{Partition: row, device: device, operation: row.DefaultOperation, capacity: capacity}
	}

	// Explicit selections replace the default full selection, but augment modes.
	if mode != flashModeFull || len(partitions) > 0 {
		selected := make(map[string]bool, len(partitions)+6)
		for _, p := range plan.partitions {
			if (mode == flashModeRootfsOnly && p.RootfsOnly) || (mode == flashModeNoRootfs && p.NoRootfs) {
				selected[p.Name] = true
			}
		}
		for _, name := range partitions {
			selected[name] = true
			row, _ := profile.Lookup(name)
			if row.SelectAlso != "" {
				selected[row.SelectAlso] = true
			}
		}
		for i := range plan.partitions {
			p := &plan.partitions[i]
			if !selected[p.Name] {
				p.operation = "-"
			} else if p.ArtifactKey != "" {
				p.operation = "P"
			}
		}
	}

	return plan, nil
}

// requiredArtifacts includes only writes and the detected stage's RAM loaders.
// Keep board order stable, including when two partitions share one artifact.
func requiredArtifacts(plan flashPlan, stage bootStage, verity bool) []string {
	keys := make([]string, 0, len(plan.partitions)+2)
	seen := make(map[string]bool, len(plan.partitions)+2)
	add := func(key string) {
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	for _, p := range plan.partitions {
		if p.writes() {
			add(p.ArtifactKey)
			if verity && p.AVB {
				add("avb_body")
				add("avb_tail")
			}
		}
	}
	if stage == stageROM {
		add("tfa_usb")
	}
	if stage == stageROM || stage == stageTFA {
		add("fip_optee")
	}
	return keys
}
func (p partition) writes() bool { return p.operation == "P" }
func (p partition) erases() bool { return p.operation == "PDE" || p.operation == "PED" }
func (plan flashPlan) wireLayout() ([]byte, error) {
	var text strings.Builder
	seen := make(map[uint8]bool)
	for _, p := range plan.partitions {
		if p.Phase == 0 || p.Phase > 0xf0 || seen[p.Phase] {
			return nil, fmt.Errorf("invalid or duplicate layout phase %02x", p.Phase)
		}
		seen[p.Phase] = true
		if len(p.Name) == 0 || len(p.Name) > 16 || strings.ContainsAny(p.Name, "\t\r\n /\x00") {
			return nil, fmt.Errorf("invalid partition name %q", p.Name)
		}
		switch p.DFUType {
		case "Binary", "FIP", "System", "FileSystem", "RawImage":
		default:
			return nil, fmt.Errorf("unsupported partition kind %q", p.DFUType)
		}
		if p.device != "mmc0" && p.device != "mmc1" {
			return nil, fmt.Errorf("unsupported device %q", p.device)
		}
		switch p.operation {
		case "P", "PE", "PDE", "PED", "-":
		default:
			return nil, fmt.Errorf("unsupported operation %q", p.operation)
		}
		if p.writes() && p.ArtifactKey == "" {
			return nil, fmt.Errorf("phase %02x lacks a payload", p.Phase)
		}
		var offset string
		switch p.Area {
		case flashimage.AreaBoot1:
			offset = "boot1"
		case flashimage.AreaBoot2:
			offset = "boot2"
		case flashimage.AreaUser:
			if p.Offset < 0 || p.Offset%flashimage.SectorSize != 0 {
				return nil, fmt.Errorf("invalid MMC offset %d", p.Offset)
			}
			offset = fmt.Sprintf("0x%08X", p.Offset)
			if p.Offset == 0 {
				offset = "0x0"
			}
		default:
			return nil, fmt.Errorf("invalid MMC address area %d", p.Area)
		}
		fmt.Fprintf(&text, "%s\t0x%02X\t%s\t%s\t%s\t%s\n", p.operation, p.Phase, p.Name, p.DFUType, p.device, offset)
	}
	if text.Len() == 0 || text.Len() >= 256*1024 {
		return nil, errors.New("FlashLayout must be nonempty and less than 256 KiB")
	}
	return []byte(text.String()), nil
}

func (plan flashPlan) nextPhase(after uint8) uint8 {
	next := uint8(0xfe)
	for _, p := range plan.partitions {
		if p.Phase > after && p.writes() && p.Phase < next {
			next = p.Phase
		}
	}
	return next
}

func (plan flashPlan) expectedAlternates() map[uint8]usbPhaseExpectation {
	result := make(map[uint8]usbPhaseExpectation, len(plan.partitions))
	for _, p := range plan.partitions {
		permission := byte('a')
		if p.writes() {
			permission = 'e'
		}
		result[p.Phase] = usbPhaseExpectation{name: p.Name, permissions: permission}
	}
	return result
}

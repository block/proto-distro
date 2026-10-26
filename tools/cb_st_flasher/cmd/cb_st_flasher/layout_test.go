package main

import (
	"bytes"
	"encoding/csv"
	"proto-distro/tools/flashimage"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestBuildFlashPlanSelection(t *testing.T) {
	tests := []struct {
		name     string
		opts     flashOptions
		writes   []string
		erases   []string
		reserved []string
	}{
		{
			name:     "default full preserves reserved slots",
			opts:     flashOptions{mode: flashModeFull},
			writes:   []string{"fsbl1", "fsbl2", "metadata1", "metadata2", "fip-a", "bootfs", "rootfs_a", "data"},
			erases:   []string{"u-boot-env"},
			reserved: []string{"fip-b", "rootfs_b", "recovery"},
		},
		{
			name:   "rootfs-only also clears env",
			opts:   flashOptions{mode: flashModeRootfsOnly},
			writes: []string{"rootfs_a"}, erases: []string{"u-boot-env"},
		},
		{
			name:   "explicit selections replace default full",
			opts:   flashOptions{mode: flashModeFull, partitions: []string{"bootfs", "rootfs_b"}},
			writes: []string{"bootfs", "rootfs_b"},
		},
		{
			name:   "metadata1 updates both copies",
			opts:   flashOptions{partitions: []string{"metadata1"}},
			writes: []string{"metadata1", "metadata2"},
		},
		{
			name:   "metadata2 can be selected alone",
			opts:   flashOptions{partitions: []string{"metadata2"}},
			writes: []string{"metadata2"},
		},
		{
			name:   "no-rootfs preserves env and data",
			opts:   flashOptions{mode: flashModeNoRootfs, verity: true},
			writes: []string{"fsbl1", "fsbl2", "metadata1", "metadata2", "fip-a", "bootfs"},
		},
		{
			name:   "explicit selections augment no-rootfs",
			opts:   flashOptions{mode: flashModeNoRootfs, partitions: []string{"rootfs_a", "u-boot-env"}},
			writes: []string{"fsbl1", "fsbl2", "metadata1", "metadata2", "fip-a", "bootfs", "rootfs_a"},
			erases: []string{"u-boot-env"},
		},
		{
			name:   "explicit selections augment rootfs-only",
			opts:   flashOptions{mode: flashModeRootfsOnly, partitions: []string{"recovery"}},
			writes: []string{"rootfs_a", "recovery"}, erases: []string{"u-boot-env"},
		},
		{
			name:   "alias and duplicates select rootfs once without erasing env",
			opts:   flashOptions{partitions: []string{"rootfs", "rootfs_a", "rootfs"}},
			writes: []string{"rootfs_a"},
		},
		{
			name:     "explicit fip-b remains reserved without a payload",
			opts:     flashOptions{partitions: []string{"fip-b"}},
			reserved: []string{"fip-b"},
		},
		{
			name:   "erase-all overrides selections and mode",
			opts:   flashOptions{eraseAll: true, mode: flashModeRootfsOnly, partitions: []string{"metadata1"}, verity: true},
			erases: []string{"emmcboot0", "emmcboot1", "emmcall"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := buildFlashPlan(flashimage.C3(), tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			wantCount := 12
			if tt.opts.eraseAll {
				wantCount = 3
			}
			if len(plan.partitions) != wantCount {
				t.Fatalf("partition geometry lost: got %d rows, want %d", len(plan.partitions), wantCount)
			}
			var writes, erases, reserved []string
			for _, p := range plan.partitions {
				switch {
				case p.writes():
					writes = append(writes, p.Name)
				case p.erases():
					erases = append(erases, p.Name)
				case p.operation == "PE":
					reserved = append(reserved, p.Name)
				case p.operation != "-":
					t.Fatalf("unexpected operation for %s: %s", p.Name, p.operation)
				}
				if p.Name == "fip-b" && p.ArtifactKey != "" {
					t.Fatal("reserved fip-b must not acquire a payload")
				}
			}
			if !slices.Equal(writes, tt.writes) || !slices.Equal(erases, tt.erases) || !slices.Equal(reserved, tt.reserved) {
				t.Fatalf("writes/erases/reserved = %v / %v / %v; want %v / %v / %v", writes, erases, reserved, tt.writes, tt.erases, tt.reserved)
			}
		})
	}
}

func TestBuildFlashPlanRejectsInvalidOptions(t *testing.T) {
	for name, opts := range map[string]flashOptions{
		"negative MMC":        {mmcDev: -1},
		"unsupported MMC":     {mmcDev: 2},
		"unknown mode":        {mode: "invalid"},
		"unknown partition":   {partitions: []string{"missing"}},
		"body without rootfs": {verity: true, partitions: []string{"avb_body"}},
		"tail without rootfs": {verity: true, partitions: []string{"avb_tail"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := buildFlashPlan(flashimage.C3(), opts); err == nil {
				t.Fatal("invalid options accepted")
			}
		})
	}
}

func TestRequiredArtifactsByStage(t *testing.T) {
	for _, tt := range []struct {
		name  string
		opts  flashOptions
		stage bootStage
		want  []string
	}{
		{"paired ROM", flashOptions{verity: true, mode: flashModeRootfsOnly}, stageROM, []string{"rootfs_a", "avb_body", "avb_tail", "tfa_usb", "fip_optee"}},
		{"paired deduplication", flashOptions{verity: true, partitions: []string{"rootfs_a", "rootfs_b", "recovery"}}, stageUBoot, []string{"rootfs_a", "avb_body", "avb_tail", "rootfs_b", "recovery"}},
		{"verity nonrootfs", flashOptions{verity: true, partitions: []string{"bootfs"}}, stageUBoot, []string{"bootfs"}},
		{"verity erase", flashOptions{verity: true, eraseAll: true}, stageUBoot, nil},
		{"direct rootfs", flashOptions{mode: flashModeRootfsOnly}, stageUBoot, []string{"rootfs_a"}},
		{"ROM rootfs", flashOptions{mode: flashModeRootfsOnly}, stageROM, []string{"rootfs_a", "tfa_usb", "fip_optee"}},
		{"TFA rootfs", flashOptions{mode: flashModeRootfsOnly}, stageTFA, []string{"rootfs_a", "fip_optee"}},
		{"full deduplication", flashOptions{}, stageROM, []string{"tfa", "tfa_metadata", "fip_optee", "bootfs", "rootfs_a", "datafs", "tfa_usb"}},
		{"board order", flashOptions{partitions: []string{"recovery", "rootfs_b", "metadata1"}}, stageUBoot, []string{"tfa_metadata", "rootfs_b", "recovery"}},
		{"reserved and erase-only", flashOptions{partitions: []string{"fip-b", "u-boot-env"}}, stageUBoot, nil},
		{"direct erase-all", flashOptions{eraseAll: true}, stageUBoot, nil},
		{"ROM erase-all", flashOptions{eraseAll: true}, stageROM, []string{"tfa_usb", "fip_optee"}},
		{"TFA erase-all", flashOptions{eraseAll: true}, stageTFA, []string{"fip_optee"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := buildFlashPlan(flashimage.C3(), tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			if got := requiredArtifacts(plan, tt.stage, tt.opts.verity); !slices.Equal(got, tt.want) {
				t.Fatalf("required artifacts = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBoardLayoutCapacities(t *testing.T) {
	plan, err := buildFlashPlan(flashimage.C3(), flashOptions{partitions: []string{"rootfs_b"}})
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]int64{
		"metadata1": 0x80000, "metadata2": 0x80000,
		"fip-a": 0x400000, "fip-b": 0x400000,
		"u-boot-env": 0x80000, "bootfs": 0x80000,
		"rootfs_a": 1 << 30, "rootfs_b": 1 << 30, "recovery": 1 << 30,
	}
	for _, p := range plan.partitions {
		want, exact := known[p.Name]
		if got, ok := p.capacity, p.capacity > 0; ok != exact || got != want {
			t.Errorf("%s capacity = %d, %v; want %d, %v", p.Name, got, ok, want, exact)
		}
	}
}

func TestWireLayoutFraming(t *testing.T) {
	for _, mmc := range []int{0, 1} {
		plan, err := buildFlashPlan(flashimage.C3(), flashOptions{mmcDev: mmc, partitions: []string{"rootfs"}})
		if err != nil {
			t.Fatal(err)
		}
		wire, err := plan.wireLayout()
		if err != nil {
			t.Fatal(err)
		}
		if len(wire) >= 256*1024 || wire[len(wire)-1] != '\n' {
			t.Fatal("layout exceeds target buffer or lacks the final row terminator")
		}
		reader := csv.NewReader(bytes.NewReader(wire))
		reader.Comma = '\t'
		reader.FieldsPerRecord = 6
		rows, err := reader.ReadAll()
		if err != nil {
			t.Fatalf("target requires six-column layout without host filenames: %v", err)
		}
		if len(rows) != 12 {
			t.Fatalf("expected all 12 storage rows, got %d", len(rows))
		}
		wantOffsets := []string{"boot1", "boot2", "0x00080000", "0x00100000", "0x00180000", "0x00580000", "0x00980000", "0x00A00000", "0x00A80000", "0x40A80000", "0x80A80000", "0xC0A80000"}
		for i, row := range rows {
			if row[5] != wantOffsets[i] {
				t.Fatalf("offset %s, want %s", row[5], wantOffsets[i])
			}
			phase, err := strconv.ParseUint(row[1], 0, 8)
			if err != nil || phase != uint64(i+4) || row[4] != "mmc"+strconv.Itoa(mmc) {
				t.Fatalf("invalid storage phase/device: %v", row)
			}
			if row[2] == "rootfs_a" {
				if row[0] != "P" || row[3] != "FileSystem" || row[5] != "0x00A80000" {
					t.Fatalf("rootfs wire geometry invalid: %v", row)
				}
			} else if row[0] != "-" {
				t.Fatalf("unselected storage row not preserved: %v", row)
			}
		}
	}
}

func TestNormalizePartitions(t *testing.T) {
	partitions, err := normalizePartitions(flashimage.C3(), []string{"rootfs", "bootfs", "rootfs_a", "bootfs"})
	if err != nil {
		t.Fatalf("normalizePartitions() error = %v", err)
	}
	if joined := strings.Join(partitions, ","); joined != "rootfs_a,bootfs" {
		t.Fatalf("normalizePartitions() = %q, want rootfs_a,bootfs", joined)
	}
}

func TestProfileChoicesAndValidation(t *testing.T) {
	profile := flashimage.C3()
	choices := partitionChoices(profile)
	if len(choices) != 13 {
		t.Fatalf("choices: %v", choices)
	}
	for _, choice := range choices {
		if _, err := normalizePartitions(profile, []string{choice}); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"boot", "tfa", "fip", "datafs", "emmcboot0", "emmcboot1", "emmcall"} {
		if _, err := normalizePartitions(profile, []string{name}); err == nil {
			t.Fatalf("non-cb name accepted: %s", name)
		}
	}
	if _, err := buildFlashPlan(flashimage.Profile{}, flashOptions{}); err == nil {
		t.Fatal("invalid profile accepted")
	}
	plan, err := buildFlashPlan(profile, flashOptions{mmcDev: 1, eraseAll: true})
	if err != nil {
		t.Fatal(err)
	}
	wire, err := plan.wireLayout()
	want := "PED\t0x04\temmcboot0\tBinary\tmmc1\tboot1\nPED\t0x05\temmcboot1\tBinary\tmmc1\tboot2\nPED\t0x10\temmcall\tRawImage\tmmc1\t0x0\n"
	if err != nil || string(wire) != want {
		t.Fatalf("erase layout %q: %v", wire, err)
	}
	for _, row := range plan.partitions {
		if row.capacity != 0 {
			t.Fatal("erase row acquired capacity")
		}
	}
}

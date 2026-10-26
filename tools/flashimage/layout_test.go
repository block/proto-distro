package flashimage

import (
	"reflect"
	"testing"
)

func TestC3Layout(t *testing.T) {
	profile := C3()
	if err := profile.Validate(); err != nil {
		t.Fatal(err)
	}
	want := []struct {
		name     string
		area     AddressArea
		offset   int64
		capacity int64
	}{
		{"fsbl1", AreaBoot1, 0, 0},
		{"fsbl2", AreaBoot2, 0, 0},
		{"metadata1", AreaUser, 0x00080000, 512 * 1024},
		{"metadata2", AreaUser, 0x00100000, 512 * 1024},
		{"fip-a", AreaUser, 0x00180000, 4 * 1024 * 1024},
		{"fip-b", AreaUser, 0x00580000, 4 * 1024 * 1024},
		{"u-boot-env", AreaUser, 0x00980000, 512 * 1024},
		{"bootfs", AreaUser, 0x00A00000, 512 * 1024},
		{"rootfs_a", AreaUser, 0x00A80000, 1024 * 1024 * 1024},
		{"rootfs_b", AreaUser, 0x40A80000, 1024 * 1024 * 1024},
		{"recovery", AreaUser, 0x80A80000, 1024 * 1024 * 1024},
		{"data", AreaUser, 0xC0A80000, 0},
	}
	if profile.PartitionCount() != len(want) {
		t.Fatalf("partition count = %d", profile.PartitionCount())
	}
	var rootfsOnly, noRootfs, avb, environment, artifacts []string
	for i, expected := range want {
		row, ok := profile.Partition(i)
		if !ok || row.Name != expected.name || row.Area != expected.area || row.Offset != expected.offset || row.Phase != uint8(4+i) {
			t.Fatalf("partition %d = %+v, want %+v", i, row, expected)
		}
		lookedUp, found := profile.Lookup(row.Name)
		if !found || lookedUp != row {
			t.Fatalf("canonical lookup %q = %+v, %v", row.Name, lookedUp, found)
		}
		capacity, fixed := profile.FixedCapacity(row.Name)
		if capacity != expected.capacity || fixed != (expected.capacity != 0) {
			t.Errorf("%s capacity = %d, %v; want %d", row.Name, capacity, fixed, expected.capacity)
		}
		if row.RootfsOnly {
			rootfsOnly = append(rootfsOnly, row.Name)
		}
		if row.NoRootfs {
			noRootfs = append(noRootfs, row.Name)
		}
		if row.AVB {
			avb = append(avb, row.Name)
			if row.SourceArtifactKey != "rootfs_a" {
				t.Errorf("%s source = %q", row.Name, row.SourceArtifactKey)
			}
			body, tail, err := ValidateAVBSizes(capacity, 61952, 5632, 512)
			if err != nil || body != 61952 || tail != 1073741312 {
				t.Errorf("%s AVB offsets = %d, %d, %v", row.Name, body, tail, err)
			}
		}
		if row.Environment {
			environment = append(environment, row.Name)
		}
		if row.ArtifactKey != "" {
			artifacts = append(artifacts, row.ArtifactKey)
		}
	}
	for _, check := range []struct {
		name string
		got  []string
		want []string
	}{
		{"rootfs-only", rootfsOnly, []string{"u-boot-env", "rootfs_a"}},
		{"no-rootfs", noRootfs, []string{"fsbl1", "fsbl2", "metadata1", "metadata2", "fip-a", "bootfs"}},
		{"AVB", avb, []string{"rootfs_a", "rootfs_b", "recovery"}},
		{"environment", environment, []string{"u-boot-env"}},
		{"artifacts", artifacts, []string{"tfa", "tfa", "tfa_metadata", "tfa_metadata", "fip_optee", "bootfs", "rootfs_a", "rootfs_b", "recovery", "datafs"}},
	} {
		if !reflect.DeepEqual(check.got, check.want) {
			t.Errorf("%s = %v, want %v", check.name, check.got, check.want)
		}
	}
	for _, name := range []string{"fip-b", "rootfs_b", "recovery"} {
		row, _ := profile.Lookup(name)
		if row.DefaultOperation != "PE" {
			t.Errorf("%s default = %q, want reserved PE", name, row.DefaultOperation)
		}
	}
	first, _ := profile.Lookup("metadata1")
	second, _ := profile.Lookup("metadata2")
	if first.SelectAlso != second.Name || second.SelectAlso != "" {
		t.Fatalf("metadata selection must be one-way: %+v, %+v", first, second)
	}
	alias, ok := profile.Resolve("rootfs")
	canonical, _ := profile.Lookup("rootfs_a")
	if !ok || alias != canonical {
		t.Fatalf("rootfs alias = %+v, %v", alias, ok)
	}
	if capacity, fixed := profile.FixedCapacity("rootfs"); !fixed || capacity != 1<<30 {
		t.Fatalf("rootfs alias capacity = %d, %v", capacity, fixed)
	}
	if _, found := profile.Lookup("rootfs"); found {
		t.Fatal("canonical lookup accepted alias")
	}
}

func TestProfileFastbootRules(t *testing.T) {
	profile := C3()
	want := []struct {
		key, destination, physical string
		defaultMember              bool
		rank                       int
	}{
		{"rootfs", "rootfs_a", "rootfs_a", true, 2},
		{"bootfs", "bootfs", "bootfs", true, 3},
		{"datafs", "data", "data", true, 1},
		{"tfa", "mmc1boot1", "fsbl1", true, 7},
		{"fip", "fip-a", "fip-a", true, 6},
		{"boot", "boot", "", false, 4},
		{"recovery", "recovery", "recovery", false, 5},
	}
	if profile.FastbootRuleCount() != len(want) {
		t.Fatalf("fastboot rule count = %d", profile.FastbootRuleCount())
	}
	for i, expected := range want {
		rule, ok := profile.FastbootRule(expected.key)
		if !ok || rule.Destination() != expected.destination || rule.Partition != expected.physical || rule.Default != expected.defaultMember || rule.Rank != expected.rank {
			t.Errorf("rule %q = %+v, %v", expected.key, rule, ok)
		}
		indexed, found := profile.FastbootRuleAt(i)
		if !found || indexed != rule {
			t.Errorf("indexed rule %d = %+v, %v", i, indexed, found)
		}
	}
	for _, name := range []string{"rootfs_a", "rootfs_b", "data", "fip-a", "fsbl1", "mmc1boot1", "userfs", "dtb", "ROOTFS", "", "arbitrary"} {
		if rule, ok := profile.FastbootRule(name); ok || rule != (FastbootRule{}) {
			t.Errorf("non-key %q acquired rule %+v", name, rule)
		}
	}
	if _, found := profile.Lookup("boot"); found {
		t.Fatal("fastboot-only boot acquired physical geometry")
	}
}

func TestProfileEraseRows(t *testing.T) {
	profile := C3()
	want := []ErasePartition{
		{Name: "emmcboot0", Area: AreaBoot1, Phase: 4, DFUType: "Binary", DefaultOperation: "PED"},
		{Name: "emmcboot1", Area: AreaBoot2, Phase: 5, DFUType: "Binary", DefaultOperation: "PED"},
		{Name: "emmcall", Area: AreaUser, Phase: 16, DFUType: "RawImage", DefaultOperation: "PED"},
	}
	if profile.ErasePartitionCount() != len(want) {
		t.Fatalf("erase row count = %d", profile.ErasePartitionCount())
	}
	for i, expected := range want {
		row, ok := profile.ErasePartition(i)
		if !ok || row != expected {
			t.Errorf("erase row %d = %+v, %v", i, row, ok)
		}
		if _, selectable := profile.Resolve(row.Name); selectable {
			t.Errorf("erase row %q is selectable", row.Name)
		}
		if _, fixed := profile.FixedCapacity(row.Name); fixed {
			t.Errorf("erase row %q has inferred capacity", row.Name)
		}
	}
}

func TestProfileAlternateGeometry(t *testing.T) {
	data := c3
	for i := 9; i < len(data.partitions); i++ {
		data.partitions[i].Offset = data.partitions[8].Offset + int64(i-8)*128*1024
	}
	profile := Profile{data: &data}
	if err := profile.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"rootfs_a", "rootfs_b", "recovery"} {
		capacity, fixed := profile.FixedCapacity(name)
		if !fixed || capacity != 128*1024 {
			t.Fatalf("%s alternate capacity = %d, %v", name, capacity, fixed)
		}
		body, tail, err := ValidateAVBSizes(capacity, 61952, 5632, 512)
		if err != nil || body != 61952 || tail != 130560 {
			t.Errorf("%s alternate AVB offsets = %d, %d, %v", name, body, tail, err)
		}
	}
	// Interleaved hardware areas cannot become capacity boundaries.
	data = c3
	boot := data.partitions[1]
	data.partitions[1] = data.partitions[2]
	data.partitions[2] = boot
	if err := profile.Validate(); err != nil {
		t.Fatal(err)
	}
	if capacity, ok := profile.FixedCapacity("metadata1"); !ok || capacity != 512*1024 {
		t.Fatalf("interleaved boot area changed user capacity: %d, %v", capacity, ok)
	}
}

func TestProfileValueIsolationAndMissingGeometry(t *testing.T) {
	profile := C3()
	row, _ := profile.Partition(8)
	original := row
	row.Name, row.Offset, row.AVB = "changed", 512, false
	again, _ := profile.Partition(8)
	if row == original || again != original {
		t.Fatal("returned partition is not an independent value")
	}
	row, _ = profile.Lookup("rootfs_a")
	row.Alias = "changed"
	alias, _ := profile.Resolve("rootfs")
	if alias != original {
		t.Fatal("lookup mutation changed alias lookup")
	}
	erase, _ := profile.ErasePartition(0)
	erase.Name = "changed"
	eraseAgain, _ := profile.ErasePartition(0)
	if eraseAgain == erase {
		t.Fatal("returned erase row changed profile")
	}
	rule, _ := profile.FastbootRule("rootfs")
	rule.Partition = "changed"
	ruleAgain, _ := profile.FastbootRuleAt(0)
	if ruleAgain == rule || ruleAgain.Destination() != "rootfs_a" {
		t.Fatal("returned fastboot rule changed profile")
	}
	for _, name := range []string{"fsbl1", "fsbl2", "data"} {
		if _, found := profile.Lookup(name); !found {
			t.Errorf("known partition %q missing", name)
		}
		if size, fixed := profile.FixedCapacity(name); fixed || size != 0 {
			t.Errorf("non-fixed %q capacity = %d, %v", name, size, fixed)
		}
	}
	for _, name := range []string{"", "missing", "boot", "tfa", "fip", "datafs", "emmcboot0"} {
		if row, found := profile.Resolve(name); found || row != (Partition{}) {
			t.Errorf("nonphysical name %q resolved to %+v", name, row)
		}
		if size, fixed := profile.FixedCapacity(name); fixed || size != 0 {
			t.Errorf("missing %q capacity = %d, %v", name, size, fixed)
		}
	}
	for _, index := range []int{-1, profile.PartitionCount()} {
		if row, found := profile.Partition(index); found || row != (Partition{}) {
			t.Errorf("invalid partition index %d returned %+v", index, row)
		}
	}
	for _, index := range []int{-1, profile.ErasePartitionCount()} {
		if row, found := profile.ErasePartition(index); found || row != (ErasePartition{}) {
			t.Errorf("invalid erase index %d returned %+v", index, row)
		}
	}
	for _, index := range []int{-1, profile.FastbootRuleCount()} {
		if row, found := profile.FastbootRuleAt(index); found || row != (FastbootRule{}) {
			t.Errorf("invalid rule index %d returned %+v", index, row)
		}
	}
	var zero Profile
	if zero.Validate() == nil || zero.PartitionCount() != 0 || zero.ErasePartitionCount() != 0 || zero.FastbootRuleCount() != 0 {
		t.Fatal("zero profile must be empty and invalid")
	}
	if _, found := zero.Lookup("rootfs_a"); found {
		t.Fatal("zero profile lookup succeeded")
	}
}

func TestProfileRejectsMalformedFacts(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*profileData)
	}{
		{"empty name", func(p *profileData) { p.partitions[0].Name = "" }},
		{"duplicate name", func(p *profileData) { p.partitions[1].Name = p.partitions[0].Name }},
		{"duplicate phase", func(p *profileData) { p.partitions[1].Phase = p.partitions[0].Phase }},
		{"empty phase", func(p *profileData) { p.partitions[0].Phase = 0 }},
		{"empty DFU type", func(p *profileData) { p.partitions[0].DFUType = "" }},
		{"duplicate alias", func(p *profileData) { p.partitions[9].Alias = "rootfs" }},
		{"alias earlier name", func(p *profileData) { p.partitions[8].Alias = "fsbl1" }},
		{"alias later name", func(p *profileData) { p.partitions[0].Alias = "rootfs_a" }},
		{"self alias", func(p *profileData) { p.partitions[8].Alias = "rootfs_a" }},
		{"invalid area", func(p *profileData) { p.partitions[0].Area = 99 }},
		{"negative offset", func(p *profileData) { p.partitions[2].Offset = -512 }},
		{"unaligned offset", func(p *profileData) { p.partitions[8].Offset++ }},
		{"reversed offsets", func(p *profileData) { p.partitions[9].Offset = p.partitions[8].Offset - 512 }},
		{"equal offsets", func(p *profileData) { p.partitions[9].Offset = p.partitions[8].Offset }},
		{"missing upper bound", func(p *profileData) { p.partitions[11].Capacity = CapacityBounded }},
		{"invalid capacity kind", func(p *profileData) { p.partitions[2].Capacity = 99 }},
		{"bounded boot area", func(p *profileData) { p.partitions[0].Capacity = CapacityBounded }},
		{"growing boot area", func(p *profileData) { p.partitions[0].Capacity = CapacityGrowToEnd }},
		{"grow overlaps next", func(p *profileData) { p.partitions[7].Capacity = CapacityGrowToEnd }},
		{"unknown AVB", func(p *profileData) { p.partitions[8].Capacity = CapacityUnknown }},
		{"grow AVB", func(p *profileData) { p.partitions[11].AVB = true }},
		{"boot AVB", func(p *profileData) { p.partitions[0].AVB = true }},
		{"missing source key", func(p *profileData) { p.partitions[8].SourceArtifactKey = "" }},
		{"unknown source key", func(p *profileData) { p.partitions[9].SourceArtifactKey = "missing" }},
		{"orphan source key", func(p *profileData) { p.partitions[5].SourceArtifactKey = "rootfs_a" }},
		{"write without artifact", func(p *profileData) { p.partitions[5].DefaultOperation = "P" }},
		{"AVB without artifact", func(p *profileData) { p.partitions[5].AVB = true }},
		{"invalid operation", func(p *profileData) { p.partitions[0].DefaultOperation = "bad" }},
		{"unknown selection reference", func(p *profileData) { p.partitions[2].SelectAlso = "missing" }},
		{"alias selection reference", func(p *profileData) { p.partitions[2].SelectAlso = "rootfs" }},
		{"self selection reference", func(p *profileData) { p.partitions[2].SelectAlso = "metadata1" }},
		{"missing environment", func(p *profileData) { p.partitions[6].Environment = false }},
		{"duplicate environment", func(p *profileData) { p.partitions[7].Environment = true }},
		{"erase empty name", func(p *profileData) { p.erase[0].Name = "" }},
		{"erase duplicate name", func(p *profileData) { p.erase[1].Name = p.erase[0].Name }},
		{"erase duplicate phase", func(p *profileData) { p.erase[1].Phase = p.erase[0].Phase }},
		{"erase physical collision", func(p *profileData) { p.erase[0].Name = "fsbl1" }},
		{"erase alias collision", func(p *profileData) { p.erase[0].Name = "rootfs" }},
		{"erase invalid offset", func(p *profileData) { p.erase[0].Offset = 1 }},
		{"erase invalid area", func(p *profileData) { p.erase[0].Area = 99 }},
		{"erase invalid operation", func(p *profileData) { p.erase[0].DefaultOperation = "P" }},
		{"fastboot missing reference", func(p *profileData) { p.fastboot[0].Partition = "missing" }},
		{"fastboot alias reference", func(p *profileData) { p.fastboot[0].Partition = "rootfs" }},
		{"fastboot erase reference", func(p *profileData) { p.fastboot[0].Partition = "emmcboot0" }},
		{"fastboot duplicate key", func(p *profileData) { p.fastboot[1].Key = "rootfs" }},
		{"fastboot duplicate rank", func(p *profileData) { p.fastboot[1].Rank = 2 }},
		{"fastboot invalid rank", func(p *profileData) { p.fastboot[0].Rank = 0 }},
		{"fastboot empty key", func(p *profileData) { p.fastboot[0].Key = "" }},
		{"fastboot no destination", func(p *profileData) { p.fastboot[5].DestinationOverride = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := c3
			tc.mutate(&data)
			if err := (Profile{data: &data}).Validate(); err == nil {
				t.Fatal("malformed profile accepted")
			}
		})
	}
}

func TestProfileLookupDoesNotAllocate(t *testing.T) {
	profile := C3()
	if allocations := testing.AllocsPerRun(100, func() {
		profile.Lookup("rootfs_a")
		profile.Resolve("rootfs")
		profile.FixedCapacity("rootfs_a")
		profile.FastbootRule("rootfs")
	}); allocations != 0 {
		t.Fatalf("lookups allocate: %g", allocations)
	}
}

package flashimage

import "fmt"

// AddressArea identifies separate eMMC address spaces, not an MMC device number.
type AddressArea uint8

const (
	AreaUser AddressArea = iota
	AreaBoot1
	AreaBoot2
)

// CapacityKind distinguishes a next-boundary capacity from unknown device geometry.
type CapacityKind uint8

const (
	CapacityUnknown CapacityKind = iota
	CapacityBounded
	CapacityGrowToEnd
)

// Partition contains only values. Changing a returned partition cannot change its profile.
type Partition struct {
	Name              string
	Area              AddressArea
	Offset            int64
	Capacity          CapacityKind
	Phase             uint8
	DFUType           string
	DefaultOperation  string
	ArtifactKey       string
	SourceArtifactKey string
	Alias             string
	AVB               bool
	RootfsOnly        bool
	NoRootfs          bool
	Environment       bool
	SelectAlso        string
}

// ErasePartition describes an erase-all operation, not a selectable partition.
type ErasePartition struct {
	Name             string
	Area             AddressArea
	Offset           int64
	Phase            uint8
	DFUType          string
	DefaultOperation string
}

// FastbootRule uses exact image-selection keys; canonical destinations are not aliases for keys.
type FastbootRule struct {
	Key                 string
	Partition           string
	DestinationOverride string
	Default             bool
	Rank                int
}

func (r FastbootRule) Destination() string {
	if r.DestinationOverride != "" {
		return r.DestinationOverride
	}
	return r.Partition
}

type profileData struct {
	partitions [12]Partition
	erase      [3]ErasePartition
	fastboot   [7]FastbootRule
}

// Profile is a read-only handle. Its zero value has no rows and fails Validate.
type Profile struct{ data *profileData }

// Host flashing geometry only: firmware producers and bootloaders must also support a device.
var c3 = profileData{
	partitions: [12]Partition{
		{Name: "fsbl1", Area: AreaBoot1, Capacity: CapacityUnknown, Phase: 0x04, DFUType: "Binary", DefaultOperation: "P", ArtifactKey: "tfa", SourceArtifactKey: "tfa", NoRootfs: true},
		{Name: "fsbl2", Area: AreaBoot2, Capacity: CapacityUnknown, Phase: 0x05, DFUType: "Binary", DefaultOperation: "P", ArtifactKey: "tfa", SourceArtifactKey: "tfa", NoRootfs: true},
		{Name: "metadata1", Area: AreaUser, Offset: 0x00080000, Capacity: CapacityBounded, Phase: 0x06, DFUType: "Binary", DefaultOperation: "P", ArtifactKey: "tfa_metadata", SourceArtifactKey: "tfa_metadata", NoRootfs: true, SelectAlso: "metadata2"},
		{Name: "metadata2", Area: AreaUser, Offset: 0x00100000, Capacity: CapacityBounded, Phase: 0x07, DFUType: "Binary", DefaultOperation: "P", ArtifactKey: "tfa_metadata", SourceArtifactKey: "tfa_metadata", NoRootfs: true},
		{Name: "fip-a", Area: AreaUser, Offset: 0x00180000, Capacity: CapacityBounded, Phase: 0x08, DFUType: "FIP", DefaultOperation: "P", ArtifactKey: "fip_optee", SourceArtifactKey: "fip_optee", NoRootfs: true},
		{Name: "fip-b", Area: AreaUser, Offset: 0x00580000, Capacity: CapacityBounded, Phase: 0x09, DFUType: "FIP", DefaultOperation: "PE"},
		{Name: "u-boot-env", Area: AreaUser, Offset: 0x00980000, Capacity: CapacityBounded, Phase: 0x0a, DFUType: "Binary", DefaultOperation: "PDE", RootfsOnly: true, Environment: true},
		{Name: "bootfs", Area: AreaUser, Offset: 0x00A00000, Capacity: CapacityBounded, Phase: 0x0b, DFUType: "System", DefaultOperation: "P", ArtifactKey: "bootfs", SourceArtifactKey: "bootfs", NoRootfs: true},
		{Name: "rootfs_a", Area: AreaUser, Offset: 0x00A80000, Capacity: CapacityBounded, Phase: 0x0c, DFUType: "FileSystem", DefaultOperation: "P", ArtifactKey: "rootfs_a", SourceArtifactKey: "rootfs_a", Alias: "rootfs", AVB: true, RootfsOnly: true},
		{Name: "rootfs_b", Area: AreaUser, Offset: 0x40A80000, Capacity: CapacityBounded, Phase: 0x0d, DFUType: "FileSystem", DefaultOperation: "PE", ArtifactKey: "rootfs_b", SourceArtifactKey: "rootfs_a", AVB: true},
		{Name: "recovery", Area: AreaUser, Offset: 0x80A80000, Capacity: CapacityBounded, Phase: 0x0e, DFUType: "FileSystem", DefaultOperation: "PE", ArtifactKey: "recovery", SourceArtifactKey: "rootfs_a", AVB: true},
		{Name: "data", Area: AreaUser, Offset: 0xC0A80000, Capacity: CapacityGrowToEnd, Phase: 0x0f, DFUType: "FileSystem", DefaultOperation: "P", ArtifactKey: "datafs", SourceArtifactKey: "datafs"},
	},
	erase: [3]ErasePartition{
		{Name: "emmcboot0", Area: AreaBoot1, Phase: 0x04, DFUType: "Binary", DefaultOperation: "PED"},
		{Name: "emmcboot1", Area: AreaBoot2, Phase: 0x05, DFUType: "Binary", DefaultOperation: "PED"},
		{Name: "emmcall", Area: AreaUser, Phase: 0x10, DFUType: "RawImage", DefaultOperation: "PED"},
	},
	fastboot: [7]FastbootRule{
		{Key: "rootfs", Partition: "rootfs_a", Default: true, Rank: 2},
		{Key: "bootfs", Partition: "bootfs", Default: true, Rank: 3},
		{Key: "datafs", Partition: "data", Default: true, Rank: 1},
		{Key: "tfa", Partition: "fsbl1", DestinationOverride: "mmc1boot1", Default: true, Rank: 7},
		{Key: "fip", Partition: "fip-a", Default: true, Rank: 6},
		{Key: "boot", DestinationOverride: "boot", Rank: 4},
		{Key: "recovery", Partition: "recovery", Rank: 5},
	},
}

// C3 returns the host flashing profile shared by all C3 board revisions.
func C3() Profile { return Profile{data: &c3} }

func (p Profile) PartitionCount() int {
	if p.data == nil {
		return 0
	}
	return len(p.data.partitions)
}

func (p Profile) Partition(index int) (Partition, bool) {
	if index < 0 || index >= p.PartitionCount() {
		return Partition{}, false
	}
	return p.data.partitions[index], true
}

// Lookup accepts only canonical physical names. Use Resolve for cb aliases.
func (p Profile) Lookup(name string) (Partition, bool) {
	for i := range p.PartitionCount() {
		row := p.data.partitions[i]
		if row.Name == name {
			return row, true
		}
	}
	return Partition{}, false
}

func (p Profile) Resolve(name string) (Partition, bool) {
	if name != "" {
		for i := range p.PartitionCount() {
			row := p.data.partitions[i]
			if row.Name == name || row.Alias == name {
				return row, true
			}
		}
	}
	return Partition{}, false
}

// FixedCapacity derives capacity from the next user-area boundary, never another area.
// A false result means missing or non-fixed geometry; Resolve distinguishes those cases.
func (p Profile) FixedCapacity(name string) (int64, bool) {
	row, ok := p.Resolve(name)
	if !ok || row.Capacity != CapacityBounded || row.Area != AreaUser || row.Offset < 0 || row.Offset%SectorSize != 0 {
		return 0, false
	}
	for i := range p.PartitionCount() {
		if p.data.partitions[i].Name != row.Name {
			continue
		}
		for j := i + 1; j < p.PartitionCount(); j++ {
			next := p.data.partitions[j]
			if next.Area != AreaUser {
				continue
			}
			if next.Offset <= row.Offset || next.Offset%SectorSize != 0 {
				return 0, false
			}
			return next.Offset - row.Offset, true
		}
		break
	}
	return 0, false
}

func (p Profile) ErasePartitionCount() int {
	if p.data == nil {
		return 0
	}
	return len(p.data.erase)
}

func (p Profile) ErasePartition(index int) (ErasePartition, bool) {
	if index < 0 || index >= p.ErasePartitionCount() {
		return ErasePartition{}, false
	}
	return p.data.erase[index], true
}

func (p Profile) FastbootRuleCount() int {
	if p.data == nil {
		return 0
	}
	return len(p.data.fastboot)
}

func (p Profile) FastbootRuleAt(index int) (FastbootRule, bool) {
	if index < 0 || index >= p.FastbootRuleCount() {
		return FastbootRule{}, false
	}
	return p.data.fastboot[index], true
}

func (p Profile) FastbootRule(key string) (FastbootRule, bool) {
	for i := range p.FastbootRuleCount() {
		rule := p.data.fastboot[i]
		if rule.Key == key {
			return rule, true
		}
	}
	return FastbootRule{}, false
}

// Validate checks host facts before either caller performs device operations.
// Protocol-specific limits remain the caller's responsibility.
func (p Profile) Validate() error {
	if p.data == nil {
		return fmt.Errorf("empty partition profile")
	}
	lastUserOffset := int64(-1)
	environments := 0
	for i, row := range p.data.partitions {
		if row.Name == "" || row.Phase == 0 || row.DFUType == "" {
			return fmt.Errorf("partition %d has an empty identity or DFU definition", i)
		}
		if row.Area > AreaBoot2 || row.Offset < 0 || row.Offset%SectorSize != 0 {
			return fmt.Errorf("partition %q has invalid address geometry", row.Name)
		}
		if row.Area == AreaUser {
			if row.Offset <= lastUserOffset {
				return fmt.Errorf("partition %q has a non-increasing user offset", row.Name)
			}
			lastUserOffset = row.Offset
		}
		if row.Capacity > CapacityGrowToEnd || (row.Area != AreaUser && row.Capacity != CapacityUnknown) {
			return fmt.Errorf("partition %q has invalid capacity classification", row.Name)
		}
		if row.Capacity == CapacityBounded {
			if _, ok := p.FixedCapacity(row.Name); !ok {
				return fmt.Errorf("partition %q has no valid next boundary", row.Name)
			}
		}
		if row.Capacity == CapacityGrowToEnd {
			for _, next := range p.data.partitions[i+1:] {
				if next.Area == row.Area {
					return fmt.Errorf("partition %q grows across another partition", row.Name)
				}
			}
		}
		if row.Alias == row.Name {
			return fmt.Errorf("partition %q aliases itself", row.Name)
		}
		for _, other := range p.data.partitions[:i] {
			if row.Name == other.Name || row.Phase == other.Phase || row.Name == other.Alias ||
				(row.Alias != "" && (row.Alias == other.Name || row.Alias == other.Alias)) {
				return fmt.Errorf("partition %q has a duplicate identity, alias, or phase", row.Name)
			}
		}
		switch row.DefaultOperation {
		case "P":
			if row.ArtifactKey == "" {
				return fmt.Errorf("partition %q writes without an artifact", row.Name)
			}
		case "PE", "PDE", "PED", "-":
		default:
			return fmt.Errorf("partition %q has invalid default operation", row.Name)
		}
		if (row.ArtifactKey == "") != (row.SourceArtifactKey == "") {
			return fmt.Errorf("partition %q has incomplete artifact keys", row.Name)
		}
		if row.SourceArtifactKey != "" {
			found := false
			for _, source := range p.data.partitions {
				if source.ArtifactKey == row.SourceArtifactKey && source.SourceArtifactKey == source.ArtifactKey {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("partition %q references an unknown source artifact", row.Name)
			}
		}
		if row.SelectAlso != "" {
			if _, ok := p.Lookup(row.SelectAlso); !ok || row.SelectAlso == row.Name {
				return fmt.Errorf("partition %q has invalid select-also reference", row.Name)
			}
		}
		if row.AVB {
			if _, ok := p.FixedCapacity(row.Name); !ok || row.ArtifactKey == "" {
				return fmt.Errorf("AVB partition %q needs bounded geometry and an artifact", row.Name)
			}
		}
		if row.Environment {
			environments++
		}
	}
	if environments != 1 {
		return fmt.Errorf("partition profile must have exactly one environment partition")
	}
	for i, row := range p.data.erase {
		if row.Name == "" || row.Phase == 0 || row.DFUType == "" || row.DefaultOperation != "PED" ||
			row.Area > AreaBoot2 || row.Offset < 0 || row.Offset%SectorSize != 0 {
			return fmt.Errorf("invalid erase-all row %d", i)
		}
		if _, ok := p.Resolve(row.Name); ok {
			return fmt.Errorf("erase-all row %q conflicts with a physical partition", row.Name)
		}
		for _, other := range p.data.erase[:i] {
			if row.Name == other.Name || row.Phase == other.Phase {
				return fmt.Errorf("erase-all row %q has a duplicate name or phase", row.Name)
			}
		}
	}
	for i, rule := range p.data.fastboot {
		if rule.Key == "" || rule.Destination() == "" || rule.Rank <= 0 {
			return fmt.Errorf("invalid fastboot rule %d", i)
		}
		if rule.Partition != "" {
			if _, ok := p.Lookup(rule.Partition); !ok {
				return fmt.Errorf("fastboot rule %q references an unknown physical partition", rule.Key)
			}
		}
		for _, other := range p.data.fastboot[:i] {
			if rule.Key == other.Key || rule.Rank == other.Rank {
				return fmt.Errorf("fastboot rule %q has a duplicate key or rank", rule.Key)
			}
		}
	}
	return nil
}

# go_fastboot

A command-line tool for flashing and managing mining rigs/devices over the network.

Maintained in proto-distro, imported from [miner-firmware](https://github.com/block/miner-firmware/tree/b702dc4082139f40861949b264d192214ee240d0/tools/go_fastboot) at commit `b702dc4082139f40861949b264d192214ee240d0`. The public Go module is `proto-distro/tools/go_fastboot`. Fastboot uses native UDP only; no external fastboot executable is needed.

## Installation

**Recommended: Using Hermit**

The tool is automatically available when you activate the Hermit environment (from the repository root):

```bash
source bin/activate-hermit
go_fastboot --help
```

**Released binary or local build**

Existing `distro-tools` bundles include a standalone `go_fastboot` for Darwin/Linux amd64/arm64. Extract the matching architecture archive and run `./go_fastboot --help`. No Go installation or CGO library is needed for this binary. Source use invokes `bin/scripts/go_fastboot`, which delegates to the tool justfile with the Hermit Go toolchain. `just flash` remains the separate USB `cb_st_flasher` workflow.

For a local build:

```bash
cd tools/go_fastboot
just build                               # Builds to dist/<os>-<arch>/go_fastboot
# Or
go build -o go_fastboot ./cmd/go_fastboot
```

## Quick Start

```bash
go_fastboot discover                      # Find devices on network
go_fastboot target 192.168.1.100          # Set target device
go_fastboot status                        # Check device status
go_fastboot enter                         # Enter fastboot mode
go_fastboot enter --fastboot-ip 192.168.1.100  # Request a specific fastboot IP
go_fastboot flash firmware.zip            # Flash firmware
```

## Commands

| Command | Description |
|---------|-------------|
| `target <ip>` | Set target device (persisted to `.go_fastboot/target.json` in the current working directory) |
| `target` | Show current target (auto-relocates if IP changed) |
| `discover` | Scan network for devices |
| `status` | Show device mode, version, and service status |
| `enter` | Reboot from Linux into fastboot mode |
| `exit` | Reboot from fastboot back to Linux |
| `reboot` | Reboot device (works in any mode) |
| `flash` | Flash firmware image to device |

Each working directory has its own saved target. The old home-directory cache is neither read nor migrated; run `go_fastboot target <ip>` in each working directory. Explicit `--target` / `GO_FASTBOOT_TARGET` overrides are transient and never replace that saved target or its metadata.

### Network settings and cancellation

`enter` accepts optional `--fastboot-ip`, `--fastboot-netmask`, and `--fastboot-gateway` values. Each nonempty value must be canonical dotted-quad IPv4. The netmask must be contiguous; `0.0.0.0` and `255.255.255.255` are valid. Hostnames, IPv6, CIDRs, ports, whitespace, and shell syntax are rejected before device access. No subnet-membership or routing constraints are imposed.

Empty positional slots are preserved: a gateway-only request sends `reboot --fastboot '' '' '192.0.2.1'`. With all settings empty, the command remains `reboot --fastboot`.

Native UDP reboot requires a final `OKAY`; a disappearing device or lost acknowledgement is an error. SSH reboot tolerates a missing exit status only after the server accepts the command. Rejected commands, nonzero exits, exit signals, and cancellation remain errors. Failed SSH commands retain stderr diagnostics, including sudo password requirements. `exit` reports Linux success only after a fresh SSH banner check, including after relocation.

Caller contexts bound discovery, probes, SSH handshakes/commands, transfers, and reboot waits. Each native image transfer timeout covers source planning, capacity queries, and every part; it does not restart per part. Canceled erase operations stop flashing. SSH commands have a 10-second maximum; a shorter caller deadline still wins. Fastboot entry sends the normal reboot command without stopping application services explicitly; systemd handles service shutdown.


## Flash Command

```bash
go_fastboot flash firmware.zip                       # Default partitions; auto-reboot
go_fastboot flash firmware-dir/                      # Default partitions; auto-reboot
go_fastboot flash --no-reboot rootfs.ext4              # Auto-detect image destination
go_fastboot flash -p bootfs image.bin                 # Explicit destination; no auto-reboot
go_fastboot flash -p rootfs firmware.zip              # Exact archive-selection key
go_fastboot flash -p rootfs firmware-dir/             # Exact directory-selection key
go_fastboot flash --no-reboot firmware.zip            # Suppress automatic reboot
go_fastboot flash --verity firmware.zip               # Require AVB companions for selected AVB partitions
go_fastboot flash -t 10m firmware.zip                 # Custom transfer timeout
```

**Flag placement:** Put command flags before the image argument. The inherited CLI parser stops parsing flags at the first positional argument; trailing flags are not applied.

**Reboot behavior:** Without explicit `-p`, a successful flash automatically reboots unless `--no-reboot` is set, including an auto-detected single image. Explicit partition selection suppresses automatic reboot. An environment erase is attempted only for an unspecified partition selection containing more than one image. Flash failures stop without reboot.

**Partition selection:** The [shared host profile](../flashimage/layout.go) owns exact image keys, destinations, defaults, and safe ordering. Directory and ZIP selection uses exact keys, such as `rootfs`, not the destination `rootfs_a`. A direct image accepts canonical destinations such as `rootfs_a`, `rootfs_b`, or `recovery`. Unknown destinations remain pass-through and are not speculative AVB targets. `tfa` still targets `mmc1boot1`; there is no `uboot` alias.

Ambiguous fallback filenames such as `kernel.dtb` require an explicit `--partition`; keyword matching never chooses a destination nondeterministically.

### Native transfer bounds

Every native flash requires a valid positive hexadecimal `max-download-size`; missing, invalid, zero, or failed queries are fatal. The effective payload limit is the smaller of the advertised value and 256 MiB. This is a host memory ceiling, not an assumed device capacity.

Before any environment erase or transfer, native preflight parses and plans every selected effective image, including prepared AVB images. Sparse inputs must have consistent headers, chunks, payload lengths, and logical block counts. Header extensions are supported. Invalid later images fail before earlier writes. This is not transactional flashing: source files and device state can change after preflight.

Large raw images and existing sparse images use exact wire-size planning, including every header and leading/trailing skip. Oversized RAW chunks split at block boundaries. Transfers build one bounded part, download it, require flash `OKAY`, and reuse the buffer. Payload memory is O(min(device limit, 256 MiB)); metadata grows with chunks and fragments. FILL extents are not expanded. DONT_CARE spans stay untouched; literal RAW zeros remain writes. Only the final partial block of a converted raw image receives zero padding. Rewritten sparse images omit CRC chunks and clear the checksum; no checksum or authenticity verification is implied.


### Automatic AVB companions

For selected rootfs image path `R`, companions are exactly `R + ".avb.body"` and `R + ".avb.tail"`, including nested ZIP directories. Files from another directory or version are unrelated. Full `.avb` and compact `.avb.footer` files are not companions. Direct `.avb.body`/`.avb.tail` inputs are rejected, even with `--partition`.

AVB flashing is best effort by default: if either companion is absent, one warning names the missing files and the original image is flashed without either companion. Verified boot may fail; this fallback does not disable device verification or clear an existing tail. With `flash --verity` (`FlashRequest.Verity` for Go callers), any missing companion for a selected AVB partition fails before any environment erase or image transfer. Non-AVB partitions are unaffected. Present nonregular or unreadable companions, malformed complete pairs, incompatible target capacity, or invalid download limits remain fatal in either mode. Unselected rootfs metadata is not inspected. USB `cb_st_flasher --verity` remains unchanged.

For a complete pair, the flasher requires regular nonempty raw/body/tail files, rejects sparse rootfs input, and validates shared size arithmetic. Raw and body lengths must align to 512 bytes; the tail is exactly 512 bytes. The target's `partition-size:<destination>` must equal the profile's fixed capacity, and `max-download-size` must be valid positive hexadecimal. There is no geometry fallback. These checks establish placement, not AVB authenticity, signatures, keys, or rootfs trust.

One temporary mode-0600 Android sparse image contains raw bytes at zero, body bytes immediately after raw, a DONT_CARE gap, and tail bytes at `partition capacity - 512`. Logical size and RAW chunk sizes derive from the selected capacity and transfer limit. Source zeros are preserved; the gap is not zero-filled or erased. Preparation streams bounded buffers, removes partial outputs on failure, and removes successful temporary files after flashing. Native UDP transmits the prepared sparse image.

The declarative profile derives fixed capacities from the next boundary in the same user address area. Separate boot areas have unknown capacity; data grows to device end. Neither produces AVB offsets. All C3 board revisions share this profile; rootfs slots remain 1 GiB. The 512-byte sparse/sector geometry is not the hashtree or avbtool block size. Python AVB generation, artifact/SWU tooling, Yocto, and U-Boot definitions are unchanged; a host profile edit alone does not qualify new hardware or recovery behavior.

Both flashers share ZIP traversal, entry-type, collision, checksum, and exclusive-creation checks. ZIP paths stay nested; failed extraction removes the owned temporary directory. Fastboot retains the first image candidate in archive order, except that signed eMMC TF-A takes precedence. Caller-owned directories and source files are not removed.

Loopback checks exercise native raw/AVB and oversized sparse transfers, independently reconstructed bytes, preserved gap sentinels, missing-pair warnings, strict AVB rejection before writes, and directory-local target isolation. Ephemeral SSH/UDP peers cover deadlines, cancellation, acknowledgements, and sudo diagnostics. CLI SIGINT smoke proves process termination, not cooperative signal-to-context handling. These checks do not qualify physical-board support for 512-byte sparse blocks, DONT_CARE preservation, or successful signed boot. Test hardware only on an explicitly designated board.

## Global Flags

| Flag | Env Variable | Default | Description |
|------|-------------|---------|-------------|
| `-t, --target` | `GO_FASTBOOT_TARGET` | (saved) | Target device IP or hostname |
| `-u, --username` | `GO_FASTBOOT_USERNAME` | `admin` | SSH username |
| `-p, --password` | `GO_FASTBOOT_PASSWORD` | `proto` | SSH password (see below) |

### Authentication

**Passwords are not saved to disk** for security. Provide via `-p` flag or `GO_FASTBOOT_PASSWORD` environment variable.

Ordinary commands, including target metadata gathering, try the selected credentials once. Resolution is explicit flag/environment value, then saved username, then `admin` / `proto`. Passwords are never persisted.

Only post-flash SSH verification tries the existing fallback list after the selected credentials: `root` with no password, then `admin` / `proto` (duplicates are removed). Fallback-success messages report the username, never the password.

For persistent password configuration, set the environment variable in your shell profile:

```bash
# In ~/.bashrc or ~/.zshrc
export GO_FASTBOOT_PASSWORD="your-password"
```

## Requirements

- Fastboot uses native UDP; no external fastboot executable, fallback flag, or subprocess client is supported.
- Network access to target device
- SSH access for Linux mode operations
- Source builds require Go 1.25.0 or later, the minimum required by the pinned `golang.org/x/crypto`, `x/term`, and `x/sys` dependencies. Hermit retains Go 1.26.2 as the managed build toolchain.

## Integration Testing

Host tests exercise raw-image block-count limits without creating files larger than 16 TiB, which some Linux filesystems cannot represent. The fastboot-only relocation regression requires unused loopback TCP port 22 and UDP port 5554; it skips if a local service occupies either port. An occupied SSH port is detected without waiting for a banner.

An integration test script is provided to verify changes against a real device.

### Usage

```bash
# From the go_fastboot directory:
./scripts/integration_test.sh <device_ip> [options]

# Options:
#   --full          Run full test suite including enter/exit fastboot (~5 min)
#   --skip-build    Use existing go_fastboot binary instead of building
#   --verbose       Show command output during tests
#   --flash <path>  Test flashing with a firmware zip or directory (requires --full)
```

### Examples

```bash
# Quick tests only (non-disruptive, ~30 sec)
./scripts/integration_test.sh 192.168.1.100

# Full test suite with verbose output
./scripts/integration_test.sh 192.168.1.100 --full --verbose

# Test on remote machine with pre-built binary
./scripts/integration_test.sh 192.168.1.100 --skip-build --verbose

# Full test including flash operation
./scripts/integration_test.sh 192.168.1.100 --full --flash ./miner-image.zip --verbose
```

### Test Coverage

**Quick tests (non-disruptive):**
- Version and help commands
- Target set/show
- Status command (tests spinner lifecycle)
- Version query (tests SSH connection)
- Rapid status calls (tests for goroutine leaks)

**Full tests (disruptive - device will reboot):**
- Enter fastboot mode
- Status in fastboot mode
- Exit fastboot mode
- Verify Linux mode after reboot
- Repeat fastboot entry after returning to Linux

**Flash tests (with `--flash` flag):**
- Flash all partitions from zip (tests auto-reboot and SSH verification)
- Flash specific partition/image (tests no auto-reboot behavior for targeted updates)
- Verify device recovery and mode after flash

## Notes

### Device Relocation

When a device's IP changes (e.g., after DHCP lease renewal), the tool can automatically relocate it using:
1. DNS hostname resolution
2. ARP table lookup by MAC address
3. Network scanning with `arp-scan` (if installed)

Discovery scans IPv4 only, with at most 50 active probes and lazy scheduling. `/31` includes both addresses; `/32` includes its one address. The target display uses live SSH or fastboot reachability, not cached mode.

**Note:** If `arp-scan` is available, relocation may invoke `sudo -n arp-scan <normalized-subnet>`. Results must belong to the requested subnet host range. This requires sudo privileges with cached or passwordless access; it fails instead of prompting if a password is needed.

### SSH Host Key Verification

This tool uses `InsecureIgnoreHostKey` for SSH connections. This disables SSH host key verification, does not read local host-key files, and does not save device fingerprints. Only use this tool on trusted networks.

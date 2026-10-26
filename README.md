<p align="center">
  <a href="https://github.com/block/proto-distro" target="_blank" rel="noopener noreferrer">
    <img width="64" src="doc/logo.svg" alt="Proto logo">
  </a>
</p>
<h1 align="center">
  Proto Distro
</h1>
<h3 align="center">
  Base Linux distro for Proto Rig.
</h3>
<p align="center">
  The base embedded Linux distro for the Proto Rig Control Board.<br/>
</p>
<p align="center">
  <a href="https://github.com/block/proto-distro/actions/workflows/docker-publish-distro.yml">
    <img src="https://github.com/block/proto-distro/actions/workflows/docker-publish-distro.yml/badge.svg" alt="Docker publish status." />
  </a>
</p>

**Proto Distro** is the base embedded Linux distro, based on Yocto, for managing the Proto Mining Rig control board. It manages all control-board-specific hardware and security features, and provides a platform for building the final release images flashed onto the control board. The distro is designed to be extended by downstream project [Proto Rig](https://github.com/block/miner-firmware), where mining-specific features and applications are added on top of the base distro provided here.

Supported control boards are C2 (`c2-evt`, `c2-p1`) and C3 (`c3-p1`).
OTA updates use SWUpdate; the image recipes produce the update archives.
Release disk image names retain their image, machine, and version identifiers.

Application-owned behavior and retained platform interfaces are documented in
[application/platform contracts](doc/application-contracts.md). Downstream
ownership links, recipe attribution, and historical signing references remain
useful provenance; a repository mention alone is not a runtime dependency.

## Getting Started

### Prerequisites

- Docker
- [Hermit](https://cashapp.github.io/hermit/), or the equivalent local toolchain

### Initial Setup

```bash
source ./bin/activate-hermit
```

### Build an Image

```bash
just build
```

Yocto release/manufacturing builds produce `<image>.swu` without FIP and
`<image>-with-fip.swu` with FIP, embedded `proto-swupdate`, and authenticated
bootstrap hooks. These seeds need host rootfs/AVB finalization before installation.
`just build` and `just shell` first build and stage the ARMv7 updater IPK.
Yocto installs its precompiled executable through the `proto-swupdate` recipe
in release/manufacturing images only; no postbuild package injection runs.

Before low-level `distro build`, run `just build-proto-swupdate`. BitBake requires
the staged `dist/proto-swupdate/yocto/proto-swupdate.ipk`; it never downloads or
compiles a replacement updater. Staging preserves package bytes and sets mode
`0644` so a builder user with a different UID can read the input.

Build a specific machine, distro, or variant:

```bash
just build -m c3-p1 -d proto -t release
```

Open an interactive Yocto shell inside the builder container:

```bash
just shell
```

### Post-Build Workflows

Finalize an existing artifact folder or ZIP, retaining its version:

```bash
just postbuild --version 1.2.3 artifacts/miner-image-release-c3-p1-1.2.3.zip
```

Without `--version`, postbuild derives the version from `DISTRO_VERSION` or git.
If it differs from the artifact version, postbuild copies and renames the
artifacts before stamping rootfs VERSION and publishing updated latest links.

`postbuild` regenerates AVB and rebuilds every discovered SWU, preserving each
seed's components, embedded names, updater, and hooks. It then exports local
files and publishes final checksums and the ZIP. `just postbuild` rebuilds the
separate exported raw updater; it does not replace any embedded or installed
updater. Rebuild the Yocto image with `just build` to update those executables.

Shared [`distro inject`](tools/distro/README.md#distro-inject) remains available
for downstream application packaging. This repository has no `just inject` entry
point or automatic IPK injection configuration. The injector Docker image remains
required for AVB signing.

Flash the latest artifact to a connected device:

```bash
just flash
```

> **Note:** `just flash` uses native USB through libusb; STM32CubeProgrammer is no longer required. Source runs require the repository's Hermit Go/just toolchain, a native C compiler, `pkg-config`, and libusb development files. Released binaries require the libusb shared library at runtime. See [cb_st_flasher build and runtime prerequisites](tools/cb_st_flasher/README.md#build-and-runtime-prerequisites) for platform-specific setup.

For a designated device already in fastboot mode, use the peer network tool:

```bash
go_fastboot --target 192.168.1.100 flash --partition rootfs --no-reboot artifacts/miner-image-latest/
```

The Hermit wrapper is `bin/scripts/go_fastboot`; released `distro-tools` bundles include the standalone `go_fastboot` binary for Darwin/Linux amd64/arm64 alongside `distro`, `opkg-inject`, and `cb_st_flasher`. `just flash` remains the USB workflow, not a network command.

Both flashers use the [shared host profile](tools/flashimage/layout.go), ZIP safety checks, and capacity-aware AVB placement. For selected rootfs image `R`, fastboot uses exactly `R.avb.body` and `R.avb.tail`. Missing either companion warns and flashes the original image without either metadata file; malformed complete pairs fail before any erase or flash. USB flashing remains strict under `--verity`. See [go_fastboot](tools/go_fastboot/README.md) and [cb_st_flasher](tools/cb_st_flasher/README.md) for safety limits. Missing metadata does not disable verified boot.

## Local SWU Installation

```bash
proto-swupdate install image.swu                 # Both components, if present
proto-swupdate install --rootfs image.swu        # Rootfs only
proto-swupdate install --fip image.swu           # FIP only
proto-swupdate install --rootfs --fip image.swu   # Require both components
```

Explicit selectors require their payloads before any writes. Without selectors,
missing payloads and unreadable FIP TAMP retain the existing skip behavior.
An invalid decoded TAMP bank always fails. Rootfs-only does not read or modify
FIP state; FIP-only does not discover the active root partition or its variables.
Modern proto-swupdate sets `PROTO_SWUPDATE_SKIP_FIP=1` in every native child
because it owns the FIP pass; legacy bootstrap never runs there. The parent
environment is unchanged. The normal FIP postinstall hook still runs.

FIP always targets `1 - TAMP boot_bank`, not the inverse of metadata's active bank.
Repeated installation before reboot may rewrite that same inactive bank; it does
not reverse pending metadata. After booting into a matching FIP, installation
skips without writes. Concurrent updates are not supported.

`-with-fip.swu` is a manual migration/recovery artifact, not a distributed OTA image.
Never send it through Memfault/Suricatta. For legacy direct native installation,
see [bootstrap SWUs](tools/distro/README.md#legacy-fip-bootstrap).


## Rootfs Finalization

`miner-boot.service` runs `finalize-rootfs.sh` on every boot after refreshing
`/etc/os-release`, covering first boot, OTA, and direct flashing for release and
manufacturing images. The helper is installed as `/usr/bin/finalize-rootfs.sh`
with mode `0755` and can run directly. It skips dm-verity roots and roots whose
verification state cannot be determined.

For unverified roots, the helper compares the partition size from `blockdev` with
the ext4 block count and block size from `dumpe2fs`. It exits without modifying or
remounting rootfs when no complete filesystem block can be added. Otherwise,
`resize2fs` grows the filesystem to its existing partition size. Partition
boundaries are not changed. Firstboot still handles data partition growth.

If filesystem sizing fails or a release rootfs cannot be remounted read-write,
the helper skips its writable steps and returns failure. After resizing, it
attempts to restore read-only mode and preserves any nonzero `resize2fs` exit
status through that cleanup. The boot caller logs helper failures and continues
its remaining setup, including service and security configuration.

Run the rootfs regression check with:

```bash
python3 -B -m unittest discover -s sources/meta-miner/tests
```

## Repository Layout

- [`doc/application-contracts.md`](doc/application-contracts.md) — retained application/platform integration contracts
- [`distro.yaml`](distro.yaml) — main build, artifact, and signing configuration
- [`justfile`](justfile) — top-level developer entry points for build, shell, postbuild, swu-recreate, and flash
- [`sources/`](sources/) — Yocto sources and custom layers, including [`sources/meta-miner/`](sources/meta-miner/)
- [`docker/`](docker/) — Dockerfiles for the builder and injector containers
- [`tools/`](tools/) — Go CLI for Linux distro build orchestration, and various interactions with Proto Rig control board
- [`python/`](python/) — Python helpers for signing and image manipulation utilities
- [`artifacts/`](artifacts/) — generated output folders and release-ready images

## License

Licensed under the [Apache License, Version 2.0](LICENSE), except where otherwise
noted. Third-party components retain their respective licenses.

Apache-2.0 is the default for first-party code. Existing upstream licenses and
copyright notices remain in force, including the vendored Android AVB tool's MIT
license and GPL notices in kernel sources. Python distributions, tool release
bundles, and updater packages include the applicable license and notice files.


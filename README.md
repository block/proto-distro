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

**Proto Distro** is the Yocto-based Linux distro for Proto Rig control boards.
It provides board support, security features, and flashable images.
[Proto Rig](https://github.com/block/miner-firmware) adds mining applications.

Supported control boards are C2 (`c2-evt`, `c2-p1`) and C3 (`c3-p1`).
OTA updates use SWUpdate; the image recipes produce the update archives.
Release disk image names retain their image, machine, and version identifiers.

See [application/platform contracts](doc/application-contracts.md) for integration details.

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

Build outputs are saved under `artifacts/`. The build stages the updater and
finalizes images automatically.

Build a specific machine, distro, or variant:

```bash
just build -m c3-p1 -d proto -t release
```

Open an interactive Yocto shell inside the builder container:

```bash
just shell
```

### Flash an Image

Flashing overwrites selected partitions. Check the connected board or target IP first.

For USB, connect the board in STM32 programming mode:

```bash
bin/scripts/cb_st_flasher artifacts/miner-image-latest/
```

For a board already in network fastboot mode, replace the example IP:

```bash
bin/scripts/go_fastboot --target 192.168.1.100 flash artifacts/miner-image-latest/
```

Both commands also accept a build ZIP instead of a folder. With released tools,
use `cb_st_flasher` or `go_fastboot` directly, without `bin/scripts/`.

USB requires libusb. See [USB setup and options](tools/cb_st_flasher/README.md)
or [network setup and options](tools/go_fastboot/README.md).

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

The first-party publication template is [Apache License, Version 2.0](LICENSE).
Export generates root `LICENSE` and `python/LICENSE` from this template; neither
file is kept in the internal checkout. Third-party components retain their licenses.

Apache-2.0 is the default for first-party code. Existing upstream licenses and
copyright notices remain in force, including the vendored Android AVB tool's MIT
license and GPL notices in kernel sources. Python distributions, tool release
bundles, and updater packages include the applicable license and notice files.


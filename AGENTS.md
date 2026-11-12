# AGENTS.md

This file provides concise guidance for AI assistants working in this repository.

## Overview

- This is the Yocto distro for miner control boards.
- Use the Go-based `distro` CLI (via `just ...`) for build/shell/clean.
- Bitbake commands should be run from `distro shell`, not directly on host.

## Environment

- Builds do not need a GitHub token.

## Core Commands

```bash
# One-time environment setup
source bin/activate-hermit
just docker-build-builder

# Distro CLI (preferred via just wrappers)
just build
just build -m c3-p1 -d proto -t release
just shell
just clean

# Direct CLI usage
distro -c distro.yaml build
distro -c distro.yaml shell
distro -c distro.yaml clean
```

### Cross-Compile Commands

Build Rust crates for ARM target with variant support using cargo-zigbuild:
## Flashing

```bash
# Default (latest local artifact folder)
just flash

# Explicit image path
just flash <path>
just flash --mmc-dev 1 <path>
just flash --no-rootfs <path>

# Direct tool
cb_st_flasher artifacts/miner-image-latest/
cb_st_flasher <path>
```

## Path Resolution in `distro.yaml`

All file paths in `distro.yaml` are resolved **relative to the `distro.yaml` file itself** (i.e., the repository root). Absolute paths are used as-is. This applies to all path fields including:
- `defaults.env_file` (e.g., `.env`)
- `docker.builder.dockerfile`, `docker.builder.build_context`
- `docker.builder.build_dir` (Yocto build directory, e.g., `build`)
- `docker.injector.dockerfile`, `docker.injector.build_context`
- `signing.key_dir`, `signing.private_key`, `signing.passfile`
- `avb.<machine>.private_key`, `avb.<machine>.public_key`, `avb.<machine>.avb_public_key` (selected machine only; independent of SWU signing)
- `injection.local_ipk_dir` (base directory for local IPK package lookup)
- `injection.cache_dir` (subdirectory name under artifact root for injection cache)
- `artifacts.root_dir`, `artifacts.images_base_path`, `artifacts.local_files[]`
- `build_stats.paths[]`
- `injection.variants.*.packages[].glob_override`

Use `config.ResolvePath(configPath, p)` in Go code to resolve any new path field from the config.

## Important Files

- `distro.yaml`: distro CLI config (docker, sources, artifacts, mounts)
- `justfile`: command wrappers
- `config/local.conf`, `config/bblayers.conf`: Yocto config

## Quick Yocto Workflow

```bash
just shell
bitbake <recipe>
bitbake -c listtasks <recipe>
bitbake -c devshell <recipe>
bitbake -c clean <recipe>
```

Build logs:
- `build/tmp/work/*/temp/log.*`
- `build/tmp/work/*/temp/run.*`

## See Also

- [Distro CLI Architecture](tools/distro/ARCHITECTURE.md) — package map, data flows, shared patterns, and checklist for adding features

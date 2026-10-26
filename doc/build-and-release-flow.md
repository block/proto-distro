# Build and Release Flow

The Go-based `distro` CLI in `tools/distro/` orchestrates Dockerized Yocto builds, artifact export, IPK injection, SWU repackaging, checksum generation, and release asset fetching.

## Build steps
1. `just build` — builds and stages the precompiled ARMv7 `proto-swupdate` IPK, then runs a Dockerized Yocto build for the selected machine, distro, and variant. Release/manufacturing images install the updater through Yocto; minimal/base images do not.
2. Yocto creates rootfs-only and with-FIP SWU seeds. Postbuild exports them with the plain rootfs, generates AVB, and rebuilds every discovered SWU while preserving seed membership, embedded names, updater, and hooks. It exports local files before writing final checksums and publishing ZIP output. No automatic IPK injection is configured here; shared CLI injection remains available to downstream projects.
3. `just shell` also stages the updater before opening BitBake. Direct `distro build` requires `just build-proto-swupdate` first.
4. `just postbuild` finalizes existing artifacts and refreshes the separate exported raw updater. It preserves embedded and installed updaters. Rebuild the Yocto image to update those executables.
5. Builds are cached in Docker volumes and the Yocto sstate cache, so subsequent builds are faster.

The builder forwards version/build metadata, `TARGET`, and optional
`MEMFAULT_PROJECT_KEY` into BitBake. Rebuild the local builder image with
`just docker-build-builder` after changing its environment allowlist.


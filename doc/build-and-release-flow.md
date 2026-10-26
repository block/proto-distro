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

## Rebuild security-tool images

The shared Python wheel ships `fsbl_signer`, `create_avb_image`,
`verify_avb_image`, and `avbtool`, with only `click` and `pycryptodome` as runtime
Python dependencies. Builder PATH exposes only `fsbl_signer`; injector PATH
exposes only the three AVB commands. Both isolated environments still install
both retained modules and all four console scripts from the shared wheel.

Existing local images are reused; changing sources does not update installed
tools. Rebuild both images after Python packaging or tool changes, then build
from the repository root:

```bash
source ./bin/activate-hermit
just docker-build-builder
just docker-build-injector
just build -m c3-p1 -d proto -t release
```

The builder entrypoint regenerates build configuration and host-tool links on
each invocation. Independently managed build directories that bypass it must
remove `cot_signer` from existing `conf/local.conf` `HOSTTOOLS`, retaining
`fsbl_signer`, or regenerate configuration from the updated template.

The manual `regenerate-config-certs.sh` workflow requires the unshipped
`cot_signer` and does not work with either trimmed image. It needs a separate
legacy environment outside this distribution's support. Automated native
`cert_create` and Yocto OP-TEE signing remain unchanged. See
[`Python tool documentation`](../python/README.md) for standalone installation,
container use, and the manual certificate caveat.


## Tool license material

First-party tools use Apache-2.0. Upstream dependency notices are maintained in
[`tools/THIRD_PARTY_NOTICES`](../tools/THIRD_PARTY_NOTICES) for host tools and
[`tools/proto-swupdate/THIRD_PARTY_NOTICES`](../tools/proto-swupdate/THIRD_PARTY_NOTICES)
for the updater. Review these files when dependencies, libusb, supported targets,
or the Go toolchain change. Preserve upstream copyright and license text;
the root Apache license does not replace dependency terms.

GoReleaser includes root `LICENSE` in default archives. The host config adds
maintained notices without dropping default license/readme patterns. CI copies
the same checked-in notices into each architecture's staging directory.
Every published host ZIP contains per-architecture tarballs with `LICENSE`
and `THIRD_PARTY_NOTICES`. Host bundles do not include libusb.

Updater packaging keeps the existing raw `proto-swupdate` and IPK names.
The additional updater tarball contains the executable, `LICENSE`, and
`THIRD_PARTY_NOTICES`; distribute that archive rather than the bare executable
when recipients need a self-contained license bundle. The IPK installs both
license files under `/usr/share/licenses/proto-swupdate/`. Its `Apache-2.0`
metadata describes first-party code, not a change to dependency terms.

Build the updater package from repository root:

```bash
source bin/activate-hermit
just build-proto-swupdate
```

No notice-generation script or Python step is required for native packaging.
Python wheel/sdist license contents and metadata are checked by the commands in
[`python/README.md`](../python/README.md#build-and-verify).
These notices do not establish release approval or satisfy corresponding-source
obligations for other distributed components.

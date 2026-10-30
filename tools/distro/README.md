# Distro Tools

Go module containing CLI tools for building, injecting, and releasing distro images.

## Commands

**Global Flags:**
- `--config`, `-c` — Path to config file (default: `distro.yaml`)

### `distro build`

Build the distro image via Docker. Orchestrates a Docker container to run bitbake.

```bash
distro build                                      # Build with defaults from distro.yaml
distro build -m c3-p1 -d proto -t release         # Specify machine/distro/variant
distro --config path/to/distro.yaml build         # Use custom config file
distro build --postbuild-inject 1.2.3             # Stamp os-release VERSION during post-build injection
```

**Flow:**
1. Loads config from `distro.yaml` and environment from `.env`
2. Checks Docker availability and uses the configured builder image, pulling it if missing
3. On macOS: ensures Docker build volume exists
4. Detects git version and build datetime
5. Runs `docker run` with volume mounts and env vars, executing bitbake in container
6. On macOS: syncs build outputs from Docker build volume to host (the `build/` dir is a named volume on macOS for I/O performance)
7. Copies source artifacts, optionally injects packages/VERSION, signs release/manufacturing rootfs and rebuilds discovered SWUs, then publishes ZIP/latest output

`distro build` now exits non-zero if post-build artifact export fails.

Build, postbuild, and injection overwrite source artifacts without creating
`.original` recovery copies. Folder updates are not transactional; keep a separate
copy before processing if rollback is required. ZIP inputs are replaced only
after processing succeeds. `distro postbuild --output-dir DIR` preserves its input
and writes a separate artifact under the selected parent directory.

In proto-distro, prefer root `just build` or `just shell`: both first build and
stage the precompiled updater IPK consumed by Yocto. Direct `distro build` needs
`just build-proto-swupdate` beforehand. Release/mfg install the updater through
Yocto; the root config disables automatic injection by omitting `injection`.
Shared package injection and explicit VERSION mutation remain supported for
downstream callers. The injector image remains required for AVB signing.

Local export includes shared `artifacts.files` plus
`artifacts.variants.<variant>.files`, using the same filename placeholders.
`artifacts.local_files` resolve relative to `distro.yaml` and copy by basename
during artifact finalization, after rootfs processing and before refreshing
checksums and publishing ZIP/latest output for every variant. This also applies
to `distro postbuild` on an existing folder; injection does not copy these files.
The config includes the C3 Linux ARMv7 `proto-swupdate` executable. All selected
sources are required; a copy failure prevents ZIP/latest publication.
Yocto exports plain rootfs, bootfs/datafs, SWU, and other configured files.
After package installation, postinstall scripts, VERSION updates, unmounting,
and filesystem checks, the shared distro pipeline generates
`{target}-{machine}-{version}.rootfs.ext4.avb.body` and
`{target}-{machine}-{version}.rootfs.ext4.avb.tail` beside the retained plain ext4.
These are ordinary binaries: body contains the hashtree, VBMeta, and alignment
padding; tail is the final 512-byte partition sector. No new full `.avb` image or
compact `.avb.footer` is generated; existing legacy files remain untouched.
The on-device partition remains 1 GiB.
Release and manufacturing signing run even with injection disabled and no VERSION
update. Both require machine AVB keys.

Release and manufacturing SWUs contain three ordered rootfs payloads: compressed plain ext4 at offset 0,
uncompressed AVB body at the raw ext4 file length, and a 512-byte tail at offset
1073741312. The rootfs member remains `*.rootfs.ext4.gz` for updater compatibility.
Each payload has its own hash and a signed offset in `sw-description`. Body and
tail are copied unchanged from the published files. SWU assembly checks regular,
nonempty files, matching basenames, sector alignment, and nonoverlapping ranges;
it does not parse AVB or verify AVB signatures. The signer supplies trusted,
verified metadata. Unused partition padding is not streamed or erased.
Yocto's existing image task creates `<image>.swu` without FIP and
`<image>-with-fip.swu` with deployed FIP, embedded `proto-swupdate`, and
authenticated bootstrap hooks. Both reuse `sw-description.in` for rootfs policy;
`sw-with-fip-description.in` supplies only the companion's FIP section. The recipe
adds companion rootfs hooks before the class's native variable and hash expansion.
The image recipe exclusively owns
the hook sources. Its companion uses the sstate-deployed updater and the
canonical compressed rootfs without recompression. Both archives are seeds:
host finalization adds the signed-rootfs AVB sidecars before installation.
The root artifact configuration explicitly exports both seeds.

Build/postbuild/inject rebuild every recursively discovered `.swu` at its
original relative path, including arbitrary names and nested same-basename
archives. No archive is synthesized. Each seed keeps its component membership,
updater, scripts, modes, and unrelated members. Replaced payloads use the supplied
basenames: rootfs adds `.gz`, and its sidecars add `.avb.body`/`.avb.tail`; FIP uses
its basename, which must match `fip-*.bin`. Only replaced entries' filenames,
hashes, required rootfs/AVB geometry, software version on rootfs replacement,
and signatures change.
Every staged result passes generic manifest hash and rootfs/AVB validation before
any source archive is replaced. A later failure leaves all source SWUs untouched;
each final rename is atomic, not the folder-wide publication.

Only FIP-bearing seed sets require exactly one nonempty regular `fip/fip-*.bin`
beside the outer rootfs. Missing, ambiguous, or symlinked inputs fail before
mutation. Rootfs-only sets require neither FIP nor a raw updater. Rebuilding
preserves embedded updater/hooks byte-for-byte; `artifacts.local_files` exports
a separate raw updater and never supplies embedded replacement bytes.

After successful changed processing, standalone folder/ZIP injection writes
`sha.txt` once over the full artifact root. No-work operations leave existing
checksums and ZIPs unchanged. Postbuild suppresses that intermediate write:
`FinalizeArtifactOutput` exports all local files, then writes final checksums once
even with no local exports, before ZIP/latest publication. Failed local export
leaves existing checksums unchanged. Existing `.avb`/`.avb.footer` siblings are
never deleted. CI rebuilds the injector only for PR changes to its image inputs;
other builds pull the published image. CI uploads plain ext4 with matching
body/tail files for `cb_st_flasher --verity`.

AVB or SWU failure prevents checksum/ZIP/latest finalization and retains legacy
siblings. Standalone ZIP inputs remain unchanged. Folder processing overwrites
files in place without recovery copies or rollback: plain rootfs or metadata
may already have changed. Both staged metadata files
are validated before either replaces its destination; each rename is atomic,
but the pair is not atomic. Missing or invalid outputs and stale signers emitting
full or compact artifacts fail without replacing the prior body/tail files;
rebuild with `just docker-build-injector` and retry.

Native signed split-SWU installation is verified against the pinned SWUpdate
implementation using file-backed A/B slots and a file-backed U-Boot environment,
including hash/signature failures without a slot commit. This is not hardware
boot, power-loss, or deployed-version qualification. The updated proto-swupdate
resolves a direct root partition or one single-backed device-mapper layer through
sysfs. **Do not use an old updater for rootfs OTA on a dm-verity boot.**
For the first migration from an old updater, run the corrected updater from RAM
or explicitly select the verified inactive slot with native SWUpdate. An incoming
package cannot fix the already-running old updater before it selects its target.
Separate bootfs updates and production bootloader/key replacement remain
separate operations; development defaults do not establish production trust
compatibility.

**Flags:**
- `--machine`, `-m` — Target machine (default from config)
- `--distro`, `-d` — Distro name (default from config)
- `--variant`, `-t` — Image variant: release, mfg (default from config)
- `--postbuild-inject` — Run package injection during post-build and stamp `/usr/lib/os-release` `VERSION`
- `--output-dir`, `-o` — Parent directory for the versioned artifact folder, ZIP, and latest links (default: `artifacts.root_dir`)

Explicit output paths resolve relative to the working directory. Existing
versioned folders or ZIPs are rejected rather than overwritten. This changes
only exported artifacts, not the Yocto workspace, Docker volume, injection
cache, or signing/local-file paths. Without the option, build behavior is unchanged.
Build export checks nested directory symlink targets before creating output,
rejecting source overlap and directory cycles while allowing safe aliases.

### `distro shell`

Open an interactive shell inside the build container with the bitbake environment sourced.

```bash
distro shell                  # Shell with defaults
distro shell -m c3-p1         # Shell for specific machine
```

**Flags:**
- `--machine`, `-m` — Target machine (default from config)
- `--distro`, `-d` — Distro name (default from config)

### `distro clean`

Clean build artifacts via bitbake.

```bash
distro clean                          # Clean default target
distro clean -t release               # Clean specific target
```

**Flags:**
- `--variant`, `-t` — Image variant to clean (default from config)

### `opkg-inject`

Inject IPK packages into an ext4 rootfs image using Docker.

```bash
opkg-inject -o OUTPUT IMAGE PACKAGES...
opkg-inject -o output.ext4 rootfs.ext4 package.ipk
opkg-inject -o output.ext4 rootfs.ext4 ./packages/
```

### `distro inject`

Inject packages into a distro ZIP or folder, optionally update VERSION, and regenerate release/mfg AVB and every discovered SWU.

```bash
distro inject INPUT_PATH [IPK_FILES_OR_DIRS...]
distro inject --version 1.2.3 miner-image-release.zip ./packages/
distro inject artifact-folder/  # Release/manufacturing AVB/SWU regeneration without rootfs mutation
distro inject -o dist artifact-folder/ ./packages/
distro inject --output-dir dist miner-image-release.zip ./packages/
```

Standalone policy follows the inner `miner-image-<release|mfg>-<machine>-` rootfs
basename and requires exactly one configured AVB machine. Outer ZIP/folder names
and `defaults.variant` do not select keys. Existing mfg sidecars are valid inputs.
Generic unsigned images retain injection support only without SWUs.
Build/postbuild metadata is explicit and must agree with canonical rootfs names.
Injected package names and versions are recorded in `application_manifest.txt`.
Version overrides trim surrounding whitespace before rootfs mutation and SWU
rewriting. A whitespace-only override leaves both existing versions unchanged.

`--output-dir DIR` (`-o DIR`) selects an output parent relative to the working
directory and preserves the input. Folder input produces `DIR/<folder-name>/`;
ZIP input produces `DIR/<archive-name>.zip`. Internal filenames are unchanged,
and injection does not publish latest links. Existing destinations and output
inside the source folder are rejected, including symlink aliases. Failed folder
processing may leave a partial copy. Explicit folder destinations are reserved
exclusively; explicit ZIP publication fails if another writer creates the
destination. Omit the option to retain the existing in-place behavior.

#### Package selection and offline installation

`injection.variants.<variant>.packages` selects what enters an image.
`injection.sources[].packages` only maps package names to remote sources;
listing a package there does not install it. `fetch.sources` supplies each
source's repository and tag prefix. Proto-distro's root `distro.yaml` has no
injection configuration; the updater is installed by its Yocto recipe.

Resolution follows [the injector selection code](internal/postbuild/injection.go):

- `dist_variant` defaults to `release`, independently of the image variant.
  Release filenames use `<name>`, other variants use `<name>_<dist_variant>`.
- Local packages win over remote packages. Normal local lookup is
  `<local_ipk_dir>/<name>/<dist_variant>/*.ipk`; `local_ipk_dir` resolves relative
  to the config file. `glob_override` also resolves relative to config and makes
  the package local-only, with no remote fallback.
- Filenames must match `<prefix>_<arch>_<version>.ipk` or
  `<prefix>_<version>.ipk`, without extra underscore fields after the prefix.
  Only target `armv7` is supported; matching `armv7`, `all`, and architecture-less
  filenames are accepted. Multiple matching files fail rather than choosing
  a newest version. Required packages default to true; missing required
  packages fail resolution.
- Remote lookup resolves the configured source tag, downloads `*.ipk` into its
  source/tag cache, then applies the same filename selection. `latest` resolves
  dynamically; local precedence and mutable local files also affect the result.
  Pin tags and retain the selected package bytes for reproducible inputs.

The container injector passes all selected IPKs to one `opkg install` invocation.
Opkg resolves dependencies between these local packages regardless of filename
order; no filename prefixes or manually ordered installs are needed. A failed
installation stops injection before deferred postinstalls and status updates.
Rebuild and publish the injector image to deploy changes to this behavior.

Offline installation is not installation deferral. The
[container injector](cmd/injector/main.go) uses `opkg --noaction
--force-postinstall configure` to derive dependency order without running scripts
or writing package status. It then runs deferred postinstall scripts only for
just-installed packages, in that order, using container `sh` with `OFFLINE_ROOT`
pointing to the mounted rootfs. It does not chroot into that rootfs. Scripts must honor
`OFFLINE_ROOT`; host/container-visible commands otherwise run in the container.
Postinstall failures produce warnings, but the injector still marks those
packages installed in opkg status. An installed status therefore does not prove
postinstall success or safe activation staging. Package lookup for these scripts
uses IPK control `Package` identity, with filename fallback.

By contrast, [`application_manifest.txt`](internal/fileutil/fileutil.go) derives
identity from filenames, not IPK control metadata: the last underscore field
is version, the preceding field is treated as architecture, and everything
before it is name. Thus `mcdd_mfg_armv7_1.2.3.ipk` records `mcdd_mfg 1.2.3`;
architecture-less filenames accepted by selection may not produce a manifest
entry. Unparsable filenames are skipped. Entries update by filename-derived
name; unrelated existing entries remain. This is an artifact injection record,
not an authoritative installed-package database or a complete image inventory.

`artifacts.deploy` remains a parsed configuration field but has no active
consumer in the current distro tools. It does not request bitbake targets,
select injected packages, or export files. `artifacts.files`,
`artifacts.variants.<variant>.files`, and `artifacts.local_files` control export.

### `distro postbuild`

```bash
distro postbuild --variant release --version 1.2.3 artifacts/miner-image-release-c3-p1-1.2.2
distro postbuild -o dist --variant release --version 1.2.3 artifacts/miner-image-release-c3-p1-1.2.2.zip
```

Version changes copy the old folder before renaming and processing. Build,
postbuild, and inject all use the same post-mutation signer. Plain ext4 remains
available for reinjection; `opkg-inject` and the container injector never sign AVB.
`--version` updates `/usr/lib/os-release` VERSION and `software.version` in every
rootfs-bearing SWU, preserving its existing variant suffix. Rebuilt rootfs/AVB and
FIP members use the exported replacement basenames, including with-FIP companions.
Unrelated payload bytes and manifest entries stay unchanged. Without `--version`,
postbuild derives the version from Git.

`--output-dir DIR` (`-o DIR`) selects a parent directory relative to the current
working directory, not the config file. It creates
`DIR/<artifact-name>/`, `DIR/<artifact-name>.zip`, and the usual
`<target>-<machine>-latest`, `miner-image-latest`, and `miner-image-latest.zip`
links there. Directory and ZIP inputs remain unchanged, including when the
version does not change. Package caches, local files, and signing keys still
use the original config paths. Omitting the option keeps the existing behavior.

The selected parent may contain unrelated artifacts and existing latest links.
An existing destination folder or ZIP is an error; output inside the input
directory is rejected, including symlink aliases. Output folders are reserved
exclusively before copying or extraction. Failed processing may leave a
partial destination folder, but preparation or injection failures do not publish
a ZIP or latest links. Choose another destination or explicitly remove the
partial folder before retrying.

### `distro swu-recreate`

Recreate exactly one signed SWU from a trusted seed. Supply `--rootfs FILE`,
`--fip FILE`, or both to replace only those components. Each supplied component
must exist in the seed; other components need no replacement and remain unchanged.
Rootfs replacement requires `--version VERSION` and matching adjacent AVB body/tail
files. `--version` is forbidden without `--rootfs`; use the plain software version,
without the variant suffix. Recreation preserves the existing `-release`, `-mfg`,
or `-dev` suffix in `sw-description`. This does not change VERSION inside the ext4.
Positional arguments, no replacements, and extra component inputs fail.
No AVB private key or Docker is needed; supplied AVB is not regenerated or authenticated.
Recreation ignores `artifacts.local_files`, including local `proto-swupdate` builds.
It preserves the seed updater, bootstrap hooks, and unrelated payload bytes; only
replacement payloads, rootfs AVB metadata, replacement manifest records, signature,
and software version on rootfs replacement change.

```bash
# Rootfs-only seed
distro swu-recreate --swu rootfs-seed.swu --output rebuilt.swu --rootfs new.rootfs.ext4 --version 1.2.3
# Rootfs and FIP seed
distro swu-recreate --swu paired-seed.swu --fip fip-new.bin --rootfs new.rootfs.ext4 --version 1.2.3
# Replace only FIP in a seed containing rootfs and FIP
distro swu-recreate --swu paired-seed.swu --fip fip-new.bin
distro swu-recreate --dry --swu rootfs-seed.swu --rootfs new.rootfs.ext4 --version 1.2.3
just swu-recreate --swu rootfs-seed.swu --rootfs new.rootfs.ext4 --version 1.2.3
```

For rootfs replacement, the supplied basename must end in `.rootfs.ext4` so the
unchanged seed updater recognizes the resulting `*.rootfs.ext4.gz` member.
Exact adjacent `new.rootfs.ext4.avb.body` and
`new.rootfs.ext4.avb.tail` files are required. Compressed rootfs, monolithic `.avb`,
missing metadata, and invalid geometry fail. Geometry cannot detect stale or
unauthenticated metadata; supply a trusted matching trio.
Seed archives may contain only regular files and directories. Symlinks, hardlinked
files, and special files fail before extraction, including during dry inspection.
Replaced archive members and matching `sw-description` filenames use the supplied
basename: `new.rootfs.ext4.gz`, `new.rootfs.ext4.avb.body`, `new.rootfs.ext4.avb.tail`, and the
FIP basename, which must match `fip-*.bin`. Name collisions fail. Rootfs replacement
updates the software version in `sw-description`; FIP-only replacement preserves it.
Hardware identifiers, selections, scripts, and unspecified members and manifest
entries remain unchanged.
Existing bootstrap updater/hooks remain seed content; there is no standalone
`--bootstrap-updater` input or generic-file replacement API. Only replaced entries'
filenames, hashes, required rootfs/AVB geometry, and rootfs software version are updated before signing.
Recreation does not authenticate unchanged companion files or check their manifest
hashes, including records in `copy1`/`copy2`. Bootstrap hooks, updater bytes, and
other seed members pass through unchanged, including existing stale hashes or
references to absent companion files. Recreation is targeted replacement, not seed
validation or repair. Callers must supply a seed suitable for installation:
SWUpdate still checks hashes for installed records, so stale hashes or missing
files in an installed selection can fail on the device. Re-signing does not
make those records valid.

#### Legacy FIP bootstrap

Yocto's with-FIP seed includes bootstrap, preserved by build/postbuild/inject.
The companion is for manual migration/recovery only, not distributed OTA. Never
deliver it through Memfault/Suricatta: streamed updates do not leave the complete
local archive required by this hook. Copy the finished SWU to `/data` or `/tmp` before
manual installation; do not modify it until installation finishes.

To recreate a manual companion, provide that companion as the seed and supply
both replacement rootfs/AVB and FIP. The command preserves its existing bootstrap
updater/hooks. Build/postbuild/inject preserve the same seed-owned bytes.

The legacy caller must explicitly select the correct inactive rootfs slot.
After its rootfs writes, the authenticated hook finds exactly one regular SWU
under `/data` or `/tmp` whose manifest matches the active manifest. In a private mount
namespace, it bind-mounts that file read-only, rechecks the manifest, extracts
and hashes only the updater, then runs `install --fip` synchronously. The SWU
bytes remain at their source path; no archive copy consumes `/tmp` space. Nothing is
permanently installed. A bind mount pins the source inode, not a snapshot of
concurrent writes through other paths. Concurrent modification is unsupported.
See [selector and repeat semantics](../../README.md#local-swu-installation).
Modern proto-swupdate sets `PROTO_SWUPDATE_SKIP_FIP=1` for every native child
because it owns the FIP pass; this bootstrap hook runs only for legacy callers.
Missing or ambiguous matches and discovery-command failures print a
`FIP bootstrap:` diagnostic, skip bootstrap, and exit successfully without
failing the outer rootfs installation. After selection, manifest recheck,
updater hash verification, mounts, extraction, and nested installation failures
remain fatal. Missing active manifest and workspace/setup errors also remain fatal.

All temporary files stay in one private `/tmp/.proto-swupdate-bootstrap.*`
directory. Discovery uses a symlink to retain the selected pathname and reuses
one manifest scratch file. The child workspace is recursively bound over `/tmp`
to retain the read-only archive mount. Both manifest files are removed before
the updater runs. The archive placeholder stays empty in the parent namespace.
The exit trap removes the workspace after success, failure, or handled signals.
No staging files are written to `/data`; reboot clears volatile `/tmp` leftovers
if cleanup cannot run, such as SIGKILL or power loss.

Required on-device environment: root/CAP_SYS_ADMIN, mount namespaces,
`/usr/bin/unshare`, `/bin/mount` with bind/remount/recursive-bind support,
BusyBox-compatible cpio stdout extraction, find with `-exec ... {} +`, mktemp,
ln, cmp, sha256sum, cat, and writable/executable `/tmp` with space for the updater
and native runtime files, not the full SWU. Use static `/etc/swupdate.cfg` without
external `processes`. Native binary, config, and configured public key must stay
accessible outside hidden `/tmp`. Missing capabilities/tools fail closed;
there is no unisolated fallback. Arbitrary older boards remain unqualified.

Development SWU signing does not establish production-key compatibility.
Supplied rootfs/AVB and signed FIP remain the caller's trusted inputs. Nested
failure prevents outer rootpart commit, but rootfs/FIP are not atomic: a later
outer commit failure can occur after FIP metadata commits.


**Flags:**
- `--swu SEED.swu` — Required trusted seed archive.
- `--rootfs FILE` — Required plain ext4 replacement with adjacent AVB body/tail when the seed contains rootfs; rejected otherwise.
- `--fip FILE` — Required replacement when the seed contains FIP; rejected otherwise.
- `--output`, `-o` — New output path; defaults to `<seed>_recreated.swu`. Its parent must exist. Existing destinations are never replaced.
- `--swu-key-file`, `--swu-key-pass` — Override the selected SWU private key and passphrase files from config.
- `--dry` — Strictly local, read-only input hashes and prospective output/member paths. Does not extract, sign, compress, use Docker, or create scratch/output files.

Normal execution needs native `cpio` and `openssl`, plus `gzip` for rootfs replacement.
It stages one finished SWU beside its destination and publishes only on success;
no companion, outer ZIP, checksum file, or latest link is generated. Dry reports
describe selection, not signing-credential usability or future execution.
**Reports include signing-credential fingerprints; keep them private.**

### `distro fetch`

Download release artifacts from GitHub using sources explicitly defined in a
configuration file. The repository's root `distro.yaml` defines no `fetch.sources`,
so it provides no source flags and cannot be used to download releases as-is.
Source flags are generated from each configured source's `name`; they are not
built-in flags. Pass the custom config as a global flag before the subcommand.

For example, create `fetch.yaml` with these consumer-selected sources:

```yaml
fetch:
  sources:
    - name: distro-base
      repo: block/proto-distro
      prefix: "v"
      pattern: "*.zip"
    - name: proto-apps
      repo: block/miner-firmware
      prefix: "proto-apps-"
      pattern: "*.ipk"
```

Each source flag takes a tag string; omit a flag to skip that source. Use `latest`
to resolve the most recent matching tag. At least one source flag is required.
Existing files are not re-downloaded when they already match release metadata
(size, and SHA-256 digest when the release provides one).

```bash
# Fetch distro build ZIPs using the explicit source configuration
distro --config fetch.yaml fetch --distro-base latest

# Fetch application IPKs
distro --config fetch.yaml fetch --proto-apps proto-apps-1.5.6

# Emit a machine-readable file list for downstream tooling
distro --config fetch.yaml fetch --distro-base latest --json

# Combine sources in an exact output directory
distro --config fetch.yaml fetch --distro-base v1.1.25 --proto-apps latest --output-dir ./tmp/downloads
```

**Flags:**

The following source flags exist only when using the example `fetch.yaml` above:

- `--distro-base` — tag for configured distro ZIPs (`block/proto-distro`, prefix: `v`)
- `--proto-apps` — tag for configured application IPKs (`block/miner-firmware`, prefix: `proto-apps-`)

The remaining flags are available independently of the configured source names:

- `--pattern`, `-p` — file pattern to match (overrides default per-source pattern, e.g. `*.swu`)
- `--output-dir`, `-o` — Parent directory for downloaded artifact files, relative to the working directory (when omitted or empty, defaults to `artifacts.root_dir` and stores files under `<source>-<resolved-tag>/`).
- `--json` — output a simplified JSON object mapping each source name to its downloaded or skipped file paths

### `injector`

Container-side binary that runs inside the Docker container. Not invoked directly.

## Requirements

- **Go 1.24+**
- **Docker** (required by opkg-inject and distro-inject)
- **just** (optional, for convenience targets)
- **gh** (GitHub CLI, required by distro-fetch)
- **openssl** (required by distro-inject for SWU signing)
- **cpio** (required by distro-inject for SWU repackaging)
- **gzip** (required by distro-inject for rootfs compression)

## Building

```bash
just build          # Build the distro CLI
go build ./...      # Verify all packages compile
```

## Architecture

The opkg-inject tool uses a two-stage architecture:

1. **Host side** (`opkg-inject`): Validates inputs, copies files, manages Docker
2. **Container side** (`injector`): Runs inside Docker with privileges to mount
   ext4 images and install packages via opkg

The injector Docker image is pulled if missing. To build it locally, run `just docker-build-injector` from the repo root.

## Configuration

The build commands use `distro.yaml` for configuration. Key sections:

- **defaults**: Default machine, distro, and variant values
- **docker**: Container image and build volume names
- **artifacts**: Artifact root/output paths and image file patterns (`root_dir` defaults to `artifacts`)
- **injection**: Variant-to-package mapping for automatic post-build IPK injection
- **avb**: Per-machine `private_key`, `public_key`, and `avb_public_key`, all relative to the config file. Only the selected machine is resolved; paths must be nonempty readable regular files. `c3-p1` uses C3 keys.
- **signing**: Independent SWU signing credentials. `--swu-key-file` and `--swu-key-pass` override these only, never AVB keys.

Environment variables are loaded from `.env` (MEMFAULT_PROJECT_KEY, etc.).

## Debugging

Set `DISTRO_VERBOSE=1` to print every `docker` command before execution:

```bash
DISTRO_VERBOSE=1 just build
```

This logs all `docker run`, `docker volume`, `docker pull`, and `docker build` invocations with their full arguments. Avoid passing secrets via command-line arguments or environment variables, since they will appear in the log output verbatim.

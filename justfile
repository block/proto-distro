set dotenv-load := true
set ignore-comments := true

PACKAGE_PREFIX := "miner-image"
RUST_CROSS_TARGET := "armv7-unknown-linux-gnueabihf"

default:
    @just --list

# Build distro image via Docker container
[no-exit-message]
build *ARGS: build-proto-swupdate
    @distro -c {{ source_directory() }}/distro.yaml build {{ ARGS }}

# Open interactive build shell
[no-exit-message]
shell: build-proto-swupdate
    @distro -c {{ source_directory() }}/distro.yaml shell

# Clean build artifacts
[no-exit-message]
clean:
    @distro -c {{ source_directory() }}/distro.yaml clean

[no-exit-message]
layers:
    @distro layers

# Resolve the distro build version (e.g. v0.0.15).
# DISTRO_VERSION env var wins; otherwise derive from git: exact distro tag (v*)
# if on one, else short SHA, with a "-dirty" suffix for uncommitted changes.
# Falls back to 0.0.0 outside a git repo. Prints the resolved version to stdout
# so other recipes can reuse it: DISTRO_VERSION="$(just distro-version)".
[private]
distro-version:
    #!/usr/bin/env bash
    set -euo pipefail
    if [[ -n "${DISTRO_VERSION:-}" ]]; then
      echo "${DISTRO_VERSION}"
      exit 0
    fi
    dirty=""
    if ! git -C {{ source_directory() }} diff --quiet --ignore-submodules HEAD; then
      dirty="-dirty"
    fi
    if version=$(git -C {{ source_directory() }} describe --tags --exact-match --match='v*' 2>/dev/null); then
      echo "${version}${dirty}"
    elif sha=$(git -C {{ source_directory() }} rev-parse --short HEAD 2>/dev/null); then
      echo "${sha}${dirty}"
    else
      echo "0.0.0"
    fi

# Build proto-swupdate IPK for ARM target
build-proto-swupdate:
    #!/usr/bin/env bash
    set -euo pipefail
    export DISTRO_VERSION="$({{ just_executable() }} distro-version)"
    echo "Building proto-swupdate at version ${DISTRO_VERSION}" >&2
    output=$(cd {{ source_directory() }}/tools/proto-swupdate && goreleaser release --clean --snapshot 2>&1) || { echo "$output"; exit 1; }
    shopt -s nullglob
    packages=("{{ source_directory() }}"/dist/proto-swupdate/release/proto-swupdate_armv7_*.ipk)
    if [[ ${#packages[@]} -ne 1 || ! -f "${packages[0]}" || -L "${packages[0]}" || ! -s "${packages[0]}" ]]; then
      echo "Expected exactly one precompiled proto-swupdate ARMv7 IPK" >&2
      exit 1
    fi
    staging_dir="{{ source_directory() }}/dist/proto-swupdate/yocto"
    mkdir -p "$staging_dir"
    temporary=$(mktemp "$staging_dir/.proto-swupdate.ipk.XXXXXX")
    trap 'rm -f "$temporary"' EXIT
    cp "${packages[0]}" "$temporary"
    chmod 0644 "$temporary"
    mv "$temporary" "$staging_dir/proto-swupdate.ipk"
    echo "Built proto-swupdate IPK"

# Run post-build steps on an existing artifact dir or zip
[no-exit-message]
postbuild *ARGS: build-proto-swupdate
    @cd {{ invocation_directory() }} && distro -c {{ source_directory() }}/distro.yaml postbuild {{ ARGS }}

[no-exit-message]
swu-recreate *ARGS:
    @cd {{ invocation_directory() }} && distro -c {{ source_directory() }}/distro.yaml swu-recreate {{ ARGS }}

# Flash image to device
[no-exit-message]
flash *ARGS:
    #!/usr/bin/env bash
    if [[ -z "{{ ARGS }}" ]]; then
      cb_st_flasher {{ source_directory() }}/artifacts/miner-image-latest/
    else
      cd {{ invocation_directory() }} && cb_st_flasher {{ ARGS }}
    fi

# Force rebuild the distro-builder Docker image
docker-build-builder:
    @cd {{ source_directory() }} && docker build -t ghcr.io/block/proto-distro-builder:latest -f docker/Dockerfile.builder .

# Force rebuild the injector Docker image
docker-build-injector:
    @cd {{ source_directory() }} && docker build -t ghcr.io/block/proto-distro-injector:latest -f docker/Dockerfile.injector .

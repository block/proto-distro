#!/bin/bash
set -e

DISTRO_DIR="/home/btcm/proto-distro"

sudo chown btcm:btcm "${DISTRO_DIR}/build"
BUILD_DIR="${DISTRO_DIR}/build"
export TEMPLATECONF="${DISTRO_DIR}/sources/meta-miner/conf/templates/proto"
INIT_CMD="source ${DISTRO_DIR}/sources/poky/oe-init-build-env ${BUILD_DIR}"

# Force Yocto to regenerate from templates on each invocation
rm -rf "${BUILD_DIR}/conf/"
# Remove stale bitbake server lockfiles/sockets from a previous interrupted run.
rm -f "${BUILD_DIR}/bitbake.lock" "${BUILD_DIR}/bitbake.sock" "${BUILD_DIR}/hashserve.sock"
# Force hosttools symlinks to be regenerated from current environment.
# This avoids stale links (e.g. old .venv-based fsbl_signer paths).
rm -rf "${BUILD_DIR}/tmp-glibc/hosttools"

cd "${DISTRO_DIR}"

# Source the Yocto build environment (sets PATH, BUILDDIR, etc.)
eval "${INIT_CMD}"

case "$1" in
    build)
        echo "Running build w/ machine=${MACHINE}, distro=${DISTRO}, target=${TARGET}"
        echo "+ bitbake ${TARGET}"
        exec bitbake ${TARGET}
        ;;
    clean)
        echo "+ bitbake -c clean ${TARGET}"
        exec bitbake -c clean ${TARGET}
        ;;
    layers)
        echo "+ bitbake-layers show-layers"
        exec bitbake-layers show-layers
        ;;
    *)
        echo "Unknown command: $1"
        echo "Usage: entrypoint.sh {build|clean|layers}"
        exit 1
        ;;
esac

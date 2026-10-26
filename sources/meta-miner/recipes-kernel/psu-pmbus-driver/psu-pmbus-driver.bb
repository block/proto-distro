require psu-pmbus-driver.inc

FILESEXTRAPATHS:prepend := "${THISDIR}:"

# Build from checked-out proto-psu-driver submodule under this recipe directory.
SRC_URI = "file://proto-psu-driver"
S = "${WORKDIR}/proto-psu-driver"

# Capture the submodule's git describe at recipe parse time and pass it into
# the kernel module build. Without this the .ko's MODULE_VERSION reports
# "unknown" because the file:// fetcher copies the gitlink file but the
# relative gitdir path no longer resolves under ${WORKDIR}.
def psu_driver_version(d):
    import os, subprocess
    src = os.path.join(d.getVar('THISDIR'), 'proto-psu-driver')
    try:
        return subprocess.check_output(
            ['git', '-C', src, 'describe', '--always', '--dirty'],
            stderr=subprocess.DEVNULL,
        ).decode().strip()
    except Exception:
        return 'unknown'

PSU_PMBUS_VERSION = "${@psu_driver_version(d)}"
EXTRA_OEMAKE += "PSU_PMBUS_VERSION=${PSU_PMBUS_VERSION}"

# Use for git fetch flow for proto-psu-driver (for recipe iteration without local submodule updates).
# require psu-pmbus-driver.inc
#
# # Git fetch flow for proto-psu-driver (for recipe iteration without local submodule updates).
# SRC_URI = "git://github.com/block/proto-psu-driver.git;protocol=ssh;user=git;branch=main"
# SRCREV = "${AUTOREV}"
# PV = "1.0+git${SRCPV}"
# S = "${WORKDIR}/git"

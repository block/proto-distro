SUMMARY = "PSU PMBus Kernel Driver"
DESCRIPTION = "Kernel driver for PMBus-compatible power supplies. \
Provides hwmon interface for sensor data and custom sysfs attributes \
for PSU control. Supports Chicony S24, BOCO BS402A17, and BOCO BS502A17 power supply models."

LICENSE = "GPL-2.0-only"
LIC_FILES_CHKSUM = "file://${COMMON_LICENSE_DIR}/GPL-2.0-only;md5=801f80980d171dd6425610833a22dbe6"

# Kernel dependency
DEPENDS = "virtual/kernel"

# Clear pseudo database before compile to avoid inode mismatch on rebuild
do_configure:prepend() {
    rm -rf ${WORKDIR}/pseudo
}

# Inherit kernel module class for automatic build/install
inherit module

# Module name configuration
MODULES_MODULE_SYMVERS_LOCATION = ""
MODULE_NAME = "psu-pmbus"

# Kernel module autoload configuration
# If uncommented, the module will be automatically loaded at boot
# KERNEL_MODULE_AUTOLOAD += "psu-pmbus"

# Files installed by this package
FILES:${PN} = "${base_libdir}/modules/${KERNEL_VERSION}/extra/psu-pmbus.ko"

# Package provides - this tells BitBake that this package provides the kernel module
RPROVIDES:${PN} += "kernel-module-psu-pmbus-${KERNEL_VERSION}"

# Ensure the kernel modules package is included in the final image
RDEPENDS:${PN} += "kernel-modules"

# Recommend lm-sensors for userspace monitoring tools
RRECOMMENDS:${PN} += "lmsensors-config-libsensors lmsensors-sensors"

# Module information
MODULE_AUTHOR = "Block Inc <mining@proto.xyz>"
MODULE_LICENSE = "GPL-2.0"
MODULE_DESCRIPTION = "PSU PMBus Driver"

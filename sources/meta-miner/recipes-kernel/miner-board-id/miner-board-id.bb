DESCRIPTION = "Kernel module for detecting miner board ID based on resistor strapping"
LICENSE = "GPL-2.0-only"
LIC_FILES_CHKSUM = "file://board_id.c;beginline=1;endline=1;md5=fcab174c20ea2e2bc0be64b493708266"
SRC_URI = "file://board_id.c \
           file://Makefile"

inherit module

S = "${WORKDIR}"

# Ensure the kernel module autoloads at boot
KERNEL_MODULE_AUTOLOAD += "board_id"

FILES_${PN} = "/lib/modules/"

# Ensure the kernel modules package is included in the final image
RDEPENDS_${PN} += "kernel-modules"

DESCRIPTION = "Kernel module for driving LEDs through single SPI pin"
LICENSE = "GPL-2.0-only"
LIC_FILES_CHKSUM = "file://miner-io-led-driver.c;beginline=618;endline=619;md5=23e98a8633cd1d207f3ae49e105942aa"
SRC_URI = "file://miner-io-led-driver.c \
           file://Makefile"

inherit module

S = "${WORKDIR}"

# Ensure the kernel module autoloads at boot
KERNEL_MODULE_AUTOLOAD += "miner-io-led-driver"

FILES_${PN} = "/lib/modules/"

# Ensure the kernel modules package is included in the final image
RDEPENDS_${PN} += "kernel-modules"

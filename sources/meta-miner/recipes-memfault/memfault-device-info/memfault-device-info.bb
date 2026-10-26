DESCRIPTION = "memfault-device-info application"
LICENSE = "CLOSED"

FILESEXTRAPATHS:prepend := "${THISDIR}/files:"

SRC_URI = "file://memfault-device-info"

S = "${WORKDIR}"

do_install() {
    install -Dm 755 ${WORKDIR}/memfault-device-info ${D}/${bindir}/memfault-device-info
}

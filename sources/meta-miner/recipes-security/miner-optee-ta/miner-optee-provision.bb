SUMMARY = "Provision service for installing attestation certificates."
DESCRIPTION = "${SUMMARY}"
LICENSE = "CLOSED"
LIC_FILES_CHKSUM = ""

inherit systemd

FILESEXTRAPATHS:prepend:="${THISDIR}/files:"

SYSTEMD_AUTO_ENABLE:${PN} = "enable"
SYSTEMD_SERVICE:${PN} = "miner-provision.service"

SRC_URI = "file://miner-provision.service \
"

FILES:${PN} += "/lib \
                /lib/systemd \
                /lib/systemd/system \
                /lib/systemd/system/miner-provision.service \
"

do_install() {
    install -d ${D}${systemd_system_unitdir}
    install -m 0644 ${WORKDIR}/miner-provision.service ${D}${systemd_system_unitdir}
}

RDEPENDS:${PN} += "e2fsprogs-resize2fs cloud-utils-growpart e2fsprogs-mke2fs e2fsprogs-e2fsck util-linux"
RDEPENDS:${PN} += "openssl"

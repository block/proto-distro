SUMMARY = "First-boot setup and shared rootfs finalization."
DESCRIPTION = "${SUMMARY}"
LICENSE = "CLOSED"
LIC_FILES_CHKSUM = ""

inherit systemd

FILESEXTRAPATHS:prepend:="${THISDIR}/files:"

SYSTEMD_AUTO_ENABLE:${PN} = "enable"
SYSTEMD_SERVICE:${PN} = "miner-firstboot.service"

SRC_URI = "file://miner-firstboot.service \
           file://initscript.sh \
           file://finalize-rootfs.sh \
"

FILES:${PN} += "/lib \
                /lib/systemd \
                /lib/systemd/system \
                /lib/systemd/system/miner-firstboot.service \
"

do_install() {
    install -d ${D}${systemd_system_unitdir}
    install -m 0644 ${WORKDIR}/miner-firstboot.service ${D}${systemd_system_unitdir}

    install -d ${D}${bindir}
    install -m 444 ${WORKDIR}/initscript.sh ${D}${bindir}
    install -m 0755 ${WORKDIR}/finalize-rootfs.sh ${D}${bindir}

    sed -i -e 's,@SBINDIR@,${sbindir},g' \
		-e 's,@SYSCONFDIR@,${sysconfdir},g' \
		-e 's,@BASE_BINDIR@,${base_bindir},g' \
		${D}${systemd_system_unitdir}/miner-firstboot.service
}

RDEPENDS:${PN} += "e2fsprogs-resize2fs cloud-utils-growpart e2fsprogs-mke2fs e2fsprogs-e2fsck util-linux"
RDEPENDS:${PN} += "openssl libdevmapper"
RDEPENDS:${PN} += "e2fsprogs-dumpe2fs util-linux-blockdev"
RDEPENDS:${PN} += "miner-boot"

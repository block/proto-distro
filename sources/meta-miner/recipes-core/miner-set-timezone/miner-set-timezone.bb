SUMMARY = "Bash script that sets the miner timezone"
DESCRIPTION = "${SUMMARY}"
LICENSE = "CLOSED"
LIC_FILES_CHKSUM = ""

inherit systemd

FILESEXTRAPATHS:prepend:="${THISDIR}/files:"

SYSTEMD_AUTO_ENABLE:${PN} = "enable"
SYSTEMD_SERVICE:${PN} = "miner-set-timezone.service"

RDEPENDS:${PN}:append = "curl tzdata"

SRC_URI = "file://miner-set-timezone.service \
           file://set-timezone.sh \
"

FILES:${PN} += "/lib \
                /lib/systemd \
                /lib/systemd/system \
                /lib/systemd/system/miner-set-timezone.service \
"

do_install:append() {
    install -d ${D}${systemd_system_unitdir}
    install -m 0644 ${WORKDIR}/miner-set-timezone.service ${D}${systemd_system_unitdir}

    install -d ${D}${bindir}
    install -m 0644 ${WORKDIR}/set-timezone.sh ${D}${bindir}
    sed -i -e 's,@SBINDIR@,${sbindir},g' \
		-e 's,@SYSCONFDIR@,${sysconfdir},g' \
		-e 's,@BASE_BINDIR@,${base_bindir},g' \
		${D}${systemd_system_unitdir}/miner-set-timezone.service
}

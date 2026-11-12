SUMMARY = "Bash script that runs on first boot of image to set miner hostname"
DESCRIPTION = "${SUMMARY}"
LICENSE = "CLOSED"
LIC_FILES_CHKSUM = ""

inherit systemd

FILESEXTRAPATHS:prepend:="${THISDIR}/files:"

SYSTEMD_AUTO_ENABLE:${PN} = "enable"
SYSTEMD_SERVICE:${PN} = "miner-hostname.service"

SRC_URI = "file://miner-hostname.service \
           file://init_hostname.sh \
"

FILES:${PN} += "/lib \
                /lib/systemd \
                /lib/systemd/system \
                /lib/systemd/system/miner-hostname.service \
"

do_install:append() {
    install -d ${D}${systemd_system_unitdir}
    install -m 0644 ${WORKDIR}/miner-hostname.service ${D}${systemd_system_unitdir}

    install -d ${D}${bindir}
    install -m 0644 ${WORKDIR}/init_hostname.sh ${D}${bindir}
    sed -i -e 's,@SBINDIR@,${sbindir},g' \
		-e 's,@SYSCONFDIR@,${sysconfdir},g' \
		-e 's,@BASE_BINDIR@,${base_bindir},g' \
		${D}${systemd_system_unitdir}/miner-hostname.service
}

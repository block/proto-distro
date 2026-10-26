SUMMARY = "Bash script that runs on every boot to update memfault attributes."
DESCRIPTION = "${SUMMARY}"
LICENSE = "CLOSED"
LIC_FILES_CHKSUM = ""

inherit systemd

FILESEXTRAPATHS:prepend:="${THISDIR}/files:"

SYSTEMD_AUTO_ENABLE:${PN} = "enable"
SYSTEMD_SERVICE:${PN} = "memfault-attributes.service"

SRC_URI = "file://memfault-attributes.service \
           file://memfault-attributes.sh \
"

FILES:${PN} += "/lib \
                /lib/systemd \
                /lib/systemd/system \
                /lib/systemd/system/memfault-attributes.service \
"

do_install() {
    install -d ${D}${systemd_system_unitdir}
    install -m 0644 ${WORKDIR}/memfault-attributes.service ${D}${systemd_system_unitdir}

    install -d ${D}${bindir}
    install -m 444 ${WORKDIR}/memfault-attributes.sh ${D}${bindir}

    sed -i -e 's,@SBINDIR@,${sbindir},g' \
		-e 's,@SYSCONFDIR@,${sysconfdir},g' \
		-e 's,@BASE_BINDIR@,${base_bindir},g' \
		${D}${systemd_system_unitdir}/memfault-attributes.service
}

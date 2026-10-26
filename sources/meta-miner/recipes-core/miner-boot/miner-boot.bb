SUMMARY = "Bash script that runs on every boot of image."
DESCRIPTION = "${SUMMARY}"
LICENSE = "CLOSED"
LIC_FILES_CHKSUM = ""

inherit systemd

RDEPENDS:${PN} += "miner-firstboot"

FILESEXTRAPATHS:prepend:="${THISDIR}/files:"

SYSTEMD_AUTO_ENABLE:${PN} = "enable"
SYSTEMD_SERVICE:${PN} = "miner-boot.service"

SRC_URI = "file://miner-boot.service \
           file://ota-boot.service \
           file://miner-boot-security.service \
           file://boot-security.sh \
           file://application-first-boot.target \
           file://application-ota-boot.target \
           file://application-boot.target \
           file://boot.sh \
           file://ota-boot.sh \
           file://get_serial.sh \
"

FILES:${PN} += "/lib \
                /lib/systemd \
                /lib/systemd/system \
                /lib/systemd/system/miner-boot.service \
                /lib/systemd/system/ota-boot.service \
                /lib/systemd/system/miner-boot-security.service \
                /lib/systemd/system/application-first-boot.target \
                /lib/systemd/system/application-ota-boot.target \
                /lib/systemd/system/application-boot.target \
"

do_install:append() {
    install -d ${D}${systemd_system_unitdir}
    install -m 0644 ${WORKDIR}/miner-boot.service ${D}${systemd_system_unitdir}
    install -m 0644 ${WORKDIR}/ota-boot.service ${D}${systemd_system_unitdir}
    install -m 0644 ${WORKDIR}/miner-boot-security.service ${D}${systemd_system_unitdir}
    install -m 0644 ${WORKDIR}/application-first-boot.target ${D}${systemd_system_unitdir}
    install -m 0644 ${WORKDIR}/application-ota-boot.target ${D}${systemd_system_unitdir}
    install -m 0644 ${WORKDIR}/application-boot.target ${D}${systemd_system_unitdir}

    install -d ${D}${bindir}
    install -m 0644 ${WORKDIR}/boot.sh ${D}${bindir}
    install -m 0755 ${WORKDIR}/ota-boot.sh ${D}${bindir}
    install -m 0755 ${WORKDIR}/boot-security.sh ${D}${bindir}
    sed -i -e 's,@SBINDIR@,${sbindir},g' \
		-e 's,@SYSCONFDIR@,${sysconfdir},g' \
		-e 's,@BASE_BINDIR@,${base_bindir},g' \
		${D}${systemd_system_unitdir}/miner-boot.service \
		${D}${systemd_system_unitdir}/ota-boot.service

    install -m 0755 ${WORKDIR}/get_serial.sh ${D}${bindir}
}

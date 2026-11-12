SUMMARY = "Save active logs to persistent storage before shutdown"
DESCRIPTION = "Systemd service that copies active logs from RAM (/var/log) to flash (/data/log) before system shutdown or reboot"
LICENSE = "CLOSED"

inherit systemd

RDEPENDS:${PN} = "bash gzip"

SRC_URI = " \
    file://save-active-logs.sh \
    file://save-logs-on-shutdown.service \
"

S = "${WORKDIR}"

SYSTEMD_SERVICE:${PN} = "save-logs-on-shutdown.service"
SYSTEMD_AUTO_ENABLE = "enable"

do_install() {
    # Install the script
    install -d ${D}${bindir}
    install -m 0755 ${WORKDIR}/save-active-logs.sh ${D}${bindir}/save-active-logs.sh

    # Install the systemd service
    install -d ${D}${systemd_system_unitdir}
    install -m 0644 ${WORKDIR}/save-logs-on-shutdown.service ${D}${systemd_system_unitdir}/save-logs-on-shutdown.service
}

FILES:${PN} = " \
    ${bindir}/save-active-logs.sh \
    ${systemd_system_unitdir}/save-logs-on-shutdown.service \
"

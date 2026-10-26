FILESEXTRAPATHS:prepend := "${THISDIR}/files:"

SRC_URI += "file://logrotate.timer.d/frequent.conf"

do_install:append() {
    # Install systemd timer override for frequent rotation
    # This protects against runaway logging filling up RAM
    install -d ${D}${systemd_system_unitdir}/logrotate.timer.d
    install -m 0644 ${WORKDIR}/logrotate.timer.d/frequent.conf ${D}${systemd_system_unitdir}/logrotate.timer.d/frequent.conf
}

FILES:${PN} += "${systemd_system_unitdir}/logrotate.timer.d/frequent.conf"

DESCRIPTION = "Enable auto-login on serial consoles for root user"
LICENSE = "CLOSED"
LIC_FILES_CHKSUM = ""

inherit systemd

SYSTEMD_AUTO_ENABLE = "enable"

SRC_URI = "file://auto-login-serial-root.conf"

do_install() {
    # Create systemd override directory for serial-getty services in /etc/systemd/system/
    install -d ${D}${sysconfdir}/systemd/system/serial-getty@ttySTM0.service.d

    # Install auto-login configuration for root user
    install -m 0644 ${WORKDIR}/auto-login-serial-root.conf ${D}${sysconfdir}/systemd/system/serial-getty@ttySTM0.service.d/override.conf
}

FILES:${PN} += "${sysconfdir}/systemd/system"

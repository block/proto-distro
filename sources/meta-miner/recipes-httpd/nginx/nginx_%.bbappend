FILESEXTRAPATHS:prepend := "${THISDIR}/files:"

SRC_URI:append = " \
    file://default.conf \
    file://nginx-rsyslog.conf \
    file://nginx-logrotate.conf \
"

PACKAGECONFIG:append = " ssl"

# Capture startup/configuration failures without creating a tmpfs error file.
EXTRA_OECONF:append = " --error-log-path=stderr"

do_install:append() {
    install -d ${D}${sysconfdir}/rsyslog.d ${D}${sysconfdir}/logrotate.d
    install -m 0644 ${WORKDIR}/nginx-rsyslog.conf ${D}${sysconfdir}/rsyslog.d/nginx.conf
    install -m 0644 ${WORKDIR}/nginx-logrotate.conf ${D}${sysconfdir}/logrotate.d/nginx
    if ${@bb.utils.contains('DISTRO_FEATURES', 'systemd', 'true', 'false', d)}; then
        # Install our custom default.conf
        install -d ${D}${sysconfdir}/nginx/conf.d
        install -d ${D}${sysconfdir}/nginx/ssl
        install -m 0644 ${WORKDIR}/default.conf ${D}${sysconfdir}/nginx/conf.d/
    fi
}

FILES:${PN} += "${sysconfdir}/nginx/ssl/* ${sysconfdir}/rsyslog.d/nginx.conf ${sysconfdir}/logrotate.d/nginx"
CONFFILES:${PN} += "${sysconfdir}/rsyslog.d/nginx.conf ${sysconfdir}/logrotate.d/nginx"
RDEPENDS:${PN} += "rsyslog logrotate"

FILESEXTRAPATHS:prepend:="${THISDIR}/files:"

#enable ntpd
SRC_URI += "file://enable-ntpd.cfg \
            file://disable-network.cfg \
            file://ntp.conf"

FILES_${PN} += "${sysconfdir}/ntp.conf"

do_install:append() {
    install -d ${D}${sysconfdir}
    install -m 0644 ${WORKDIR}/ntp.conf ${D}${sysconfdir}/ntp.conf
}

FILESEXTRAPATHS:prepend:="${THISDIR}/files:"

#enable ntpd
SRC_URI += "file://http.service \
            file://ssh.service"

FILES_${PN} += "${sysconfdir}/avahi/services/http.service"
FILES_${PN} += "${sysconfdir}/avahi/services/ssh.service"

do_install:append() {
    install -d ${D}${sysconfdir}
    install -m 0644 ${WORKDIR}/http.service ${D}${sysconfdir}/avahi/services/http.service
    install -m 0644 ${WORKDIR}/ssh.service ${D}${sysconfdir}/avahi/services/ssh.service
}
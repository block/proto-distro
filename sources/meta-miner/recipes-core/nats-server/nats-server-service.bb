SUMMARY = "Nats server systemd service"
DESCRIPTION = "${SUMMARY}"
LICENSE = "CLOSED"
LIC_FILES_CHKSUM = ""

inherit systemd

FILESEXTRAPATHS:prepend:="${THISDIR}/files:"

RDEPENDS:${PN} = "nats-server"

SYSTEMD_SERVICE:${PN} = "nats-server@.service"
SYSTEMD_AUTO_ENABLE:${PN} = "disable"

SRC_URI = "file://nats-server@.service \
           file://nats-auth.conf \
           file://nats-websocket.conf \
           file://nats-resources.conf \
           file://nats-open.conf \
           file://nats-secure.conf \
"

FILES:${PN} += "${systemd_system_unitdir}/nats-server@.service \
                /etc/nats \
                /etc/nats/nats-auth.conf \
                /etc/nats/nats-websocket.conf \
                /etc/nats/nats-resources.conf \
                /etc/nats/nats-open.conf \
                /etc/nats/nats-secure.conf \
"

do_install() {
    install -d ${D}${systemd_system_unitdir}
    install -m 0644 ${WORKDIR}/nats-server@.service ${D}${systemd_system_unitdir}

    install -d ${D}/etc/nats
    install -m 0444 ${WORKDIR}/nats-auth.conf ${D}/etc/nats
    install -m 0444 ${WORKDIR}/nats-websocket.conf ${D}/etc/nats
    install -m 0444 ${WORKDIR}/nats-resources.conf ${D}/etc/nats
    install -m 0444 ${WORKDIR}/nats-open.conf ${D}/etc/nats
    install -m 0444 ${WORKDIR}/nats-secure.conf ${D}/etc/nats
}

FILESEXTRAPATHS:prepend := "${THISDIR}/files:"

SRC_URI += "file://profile"

do_install:append() {
    install -m 0644 ${WORKDIR}/profile ${D}${sysconfdir}/profile
}

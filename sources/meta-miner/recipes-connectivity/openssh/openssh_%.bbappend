FILESEXTRAPATHS:prepend := "${THISDIR}/files:"

SRC_URI += "file://sshd.service"

SYSTEMD_SERVICE:${PN}-sshd = "sshd.service"
# Do not auto-enable sshd via systemd here; sshd will be enabled later by boot.sh
# depending on the security state of the device.
SYSTEMD_AUTO_ENABLE:${PN}-sshd = "disable"


do_install:append() {
    install -d ${D}${systemd_system_unitdir}
    install -c -m 0644 ${WORKDIR}/sshd.service ${D}${systemd_system_unitdir}
}

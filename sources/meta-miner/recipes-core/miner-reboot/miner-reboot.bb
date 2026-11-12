SUMMARY = "Override default linux reboot command with some additional functionality"
LICENSE = "CLOSED"

RDEPENDS:${PN} += "bash"

SRC_URI = "file://user_reboot.sh"

FILES_${PN} += "${bindir}/user_reboot.sh"

do_install() {
    install -d ${D}${bindir}
    install -m 0755 ${WORKDIR}/user_reboot.sh ${D}${bindir}/user_reboot.sh

    # Ensure the /sbin directory exists before creating the symlink
    install -d ${D}${sbindir}

    # Force remove any existing reboot symlink or binary
    rm -f ${D}${sbindir}/reboot

    # Create a new symlink to the custom reboot script
    ln -sf ${bindir}/user_reboot.sh ${D}${sbindir}/reboot
}

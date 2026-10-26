SUMMARY = "Miner system tag file"
DESCRIPTION = "Creates /etc/miner/system-tag file for storing system tag value"
LICENSE = "CLOSED"

SRC_URI = "file://system-tag.template"

# Changes to this file are stored in the overlay (upper) layer.
# They persist across reboots but are lost after a factory reset,
# at which point the file reverts to its default template.
do_install() {
    install -d ${D}${sysconfdir}/miner
    install -m 0644 ${WORKDIR}/system-tag.template ${D}${sysconfdir}/miner/system-tag
}

FILES:${PN} = "${sysconfdir}/miner/system-tag"

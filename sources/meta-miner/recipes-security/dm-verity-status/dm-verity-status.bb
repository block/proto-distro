SUMMARY = "Report dm-verity status of the mounted root filesystem"
LICENSE = "CLOSED"

SRC_URI = "file://dm-verity-status"
S = "${WORKDIR}"

inherit allarch

# The lvm2 recipe packages ${sbindir}/dmsetup in libdevmapper.
RDEPENDS:${PN} = "libdevmapper util-linux-findmnt"

do_install() {
    install -d ${D}${sbindir}
    install -m 0755 ${WORKDIR}/dm-verity-status ${D}${sbindir}/dm-verity-status
}

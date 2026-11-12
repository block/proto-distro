SUMMARY = "Release packages for production images."

PACKAGE_ARCH = "${MACHINE_ARCH}"

inherit packagegroup


RDEPENDS:packagegroup-proto-release = "\
    memfaultd-conf-release \
    miner-ip-dhcp \
    miner-optee-ta \
    systemd-conf \
"

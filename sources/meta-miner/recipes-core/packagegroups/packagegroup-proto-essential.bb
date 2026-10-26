SUMMARY = "Essential packages, installed on all images"

PACKAGE_ARCH = "${MACHINE_ARCH}"

inherit packagegroup

RDEPENDS:packagegroup-proto-essential = "\
    avahi-daemon \
    miner-boot \
    miner-home \
    miner-firstboot \
    miner-hostname \
    miner-set-timezone \
    nginx \
    optee-client \
    miner-optee-ta \
    lmsensors \
    i2c-tools \
"

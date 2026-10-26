SUMMARY = "Manufacturing packages"

PACKAGE_ARCH = "${MACHINE_ARCH}"

inherit packagegroup

# TODO: Add memtester recipe to this packagegroup.
RDEPENDS:packagegroup-proto-mfg = "\
    avahi-daemon \
    evtest \
    i2c-tools \
    iperf3 \
    libgpiod \
    libgpiod-tools \
    memfaultd-conf-release \
    minicom \
    miner-ip-dhcp \
    miner-optee-ta \
    miner-optee-provision \
    nano \
    optee-examples \
    python3 \
    spitools \
    systemd-analyze \
    tcpdump \
    usbutils \
"

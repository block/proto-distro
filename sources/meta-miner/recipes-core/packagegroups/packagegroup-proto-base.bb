SUMMARY = "Base packages, installed on all images to enable basic functionality (serial console, ssh)"

PACKAGE_ARCH = "${MACHINE_ARCH}"

inherit packagegroup

RDEPENDS:packagegroup-proto-base = "\
    dm-verity-status \
    devmem2 \
    iproute2 \
    libdevmapper \
    libudev \
    miner-board-id \
    miner-reboot \
    miner-system-tag \
    nats-server \
    nats-server-service \
    openssh-sftp-server \
    os-release \
    save-logs-on-shutdown \
    u-boot-fw-config-stm32mp \
    u-boot-fw-utils \
"

# Required by mcdd and various services for logging
RDEPENDS:packagegroup-proto-base += "\
    rsyslog \
    logrotate \
"

# Required by hbtools
RDEPENDS:packagegroup-proto-base += "\
    libusb1 \
"

# Add miner-io-led-driver only for C3 products
RDEPENDS:packagegroup-proto-base += "${@bb.utils.contains('PRODUCT_FAMILY', 'C3', 'miner-io-led-driver', '', d)}"
RDEPENDS:packagegroup-proto-base += "${@bb.utils.contains('PRODUCT_FAMILY', 'C3', 'psu-pmbus-driver', '', d)}"

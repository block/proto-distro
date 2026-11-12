SUMMARY = "Packages to enable memfault functionality."

PACKAGE_ARCH = "${MACHINE_ARCH}"

inherit packagegroup

RDEPENDS:packagegroup-proto-memfault = "\
    bash \
    curl \
    memfaultd \
    memfault-attributes \
    memfault-device-info \
    netcat \
    u-boot-fw-config-stm32mp \
    u-boot-fw-utils \
"

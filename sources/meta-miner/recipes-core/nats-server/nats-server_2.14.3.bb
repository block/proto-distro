SUMMARY = "NATS Server (prebuilt binary for ARMv7)"
DESCRIPTION = "High-performance messaging server from NATS.io"
HOMEPAGE = "https://github.com/nats-io/nats-server"
LICENSE = "Apache-2.0"
LIC_FILES_CHKSUM = "file://LICENSE;md5=86d3f3a95c324c9479bd8986968f4327"

SRC_URI = "https://github.com/nats-io/nats-server/releases/download/v${PV}/nats-server-v${PV}-linux-arm7.tar.gz"
SRC_URI[sha256sum] = "d70d6de7ad0d587ad3047f4516a054f64b693edb79894d7ec0a2d176be31f6cf"

S = "${WORKDIR}/nats-server-v${PV}-linux-arm7"

inherit bin_package

do_install() {
    install -d ${D}${bindir}
    install -m 0755 ${S}/nats-server ${D}${bindir}/nats-server
}

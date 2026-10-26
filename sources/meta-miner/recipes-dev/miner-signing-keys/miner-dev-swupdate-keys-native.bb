SUMMARY = "Signing keys for U-boot"
LICENSE = "CLOSED"
SRC_URI = "file://swupdate_dev_keys \
"

KEYDIR = "swupdate_dev_keys"
S = "${WORKDIR}/${KEYDIR}"

inherit native

do_install() {
    install -d ${D}${datadir}/${KEYDIR}
    install -m 0644 ${S}/passout ${D}${datadir}/${KEYDIR}
    install -m 0644 ${S}/priv.pem ${D}${datadir}/${KEYDIR}
    install -m 0644 ${S}/public.pem ${D}${datadir}/${KEYDIR}
}

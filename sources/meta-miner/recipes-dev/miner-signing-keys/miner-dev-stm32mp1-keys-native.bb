SUMMARY = "Signing keys for FSBL TFA"
LICENSE = "CLOSED"
SRC_URI = "file://stm32mp13_dev_keys \
"

KEYDIR = "stm32mp13_dev_keys"
S = "${WORKDIR}/${KEYDIR}"

inherit native

do_install() {
    # Install your file or tool into that directory
    install -d ${D}${datadir}/${KEYDIR}
    if [ -n "${TFA_SIGN_KEY_FILE}" ]; then
        install -m 0644 ${S}/${TFA_SIGN_KEY_FILE} ${D}${datadir}/${KEYDIR}/${TFA_SIGN_KEY_FILE}
    fi
    if [ -n "${FIP_SIGN_KEY_FILE}" ]; then
        install -m 0644 ${S}/${FIP_SIGN_KEY_FILE} ${D}${datadir}/${KEYDIR}/${FIP_SIGN_KEY_FILE}
    fi
    install -m 0644 ${S}/publicKey00.pem ${D}${datadir}/${KEYDIR}/publicKey00.pem
    install -m 0644 ${S}/publicKey01.pem ${D}${datadir}/${KEYDIR}/publicKey01.pem
    install -m 0644 ${S}/publicKeysHashHashes.bin ${D}${datadir}/${KEYDIR}/publicKeysHashHashes.bin
}

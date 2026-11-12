SUMMARY = "Signing keys for U-boot"
LICENSE = "CLOSED"
SRC_URI = "file://dev \
"

KEYDIR = "uboot_dev_keys"
S = "${WORKDIR}/dev"

inherit native

do_install() {

    # Get product family. Raises a fatal error if keys are not found
    PRODUCT_FAMILY_LOWER="${@'${PRODUCT_FAMILY}'.lower()}"

    # Define source paths for certificate and key
    CRT_SOURCE="${S}/certs/dev/${PRODUCT_FAMILY_LOWER}/${PRODUCT_FAMILY_LOWER}-linux-image-signing-key-dev.crt"
    KEY_SOURCE="${S}/keys/dev/${PRODUCT_FAMILY_LOWER}/intermediate/${PRODUCT_FAMILY_LOWER}-linux-image-signing-key-dev.priv.pem"

    # Verify required files exist
    if [ ! -f "${CRT_SOURCE}" ]; then
        bbfatal "Certificate not found at fallback path: ${CRT_SOURCE}"
    fi

    if [ ! -f "${KEY_SOURCE}" ]; then
        bbfatal "Key not found at fallback path: ${KEY_SOURCE}"
    fi

    # Install Linux signing certificate and key
    install -d ${D}${datadir}/${KEYDIR}
    install -m 0644 ${CRT_SOURCE} ${D}${datadir}/${KEYDIR}/linux-signing-key.crt
    install -m 0644 ${KEY_SOURCE} ${D}${datadir}/${KEYDIR}/linux-signing-key.key
}

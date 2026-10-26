SUMMARY = "Signing keys for dm-verity rootfs verification"
LICENSE = "CLOSED"
SRC_URI = "file://dev"

KEYDIR = "c3_dm_verity_keys"
S = "${WORKDIR}/dev"

inherit native

do_install() {
    # Get product family. Raises a fatal error if keys are not found
    PRODUCT_FAMILY_LOWER="${@'${PRODUCT_FAMILY}'.lower()}"

    # Define source paths for certificate and key
    PRIVKEY_SOURCE="${S}/keys/dev/${PRODUCT_FAMILY_LOWER}/intermediate/${PRODUCT_FAMILY_LOWER}-dm-verity-rootfs-signing-key-dev.priv.pem"
    PUBKEY_SOURCE="${S}/keys/dev/${PRODUCT_FAMILY_LOWER}/intermediate/${PRODUCT_FAMILY_LOWER}-dm-verity-rootfs-signing-key-dev.pub.pem"
    AVBKEY_SOURCE="${S}/keys/dev/${PRODUCT_FAMILY_LOWER}/intermediate/${PRODUCT_FAMILY_LOWER}-dm-verity-rootfs-signing-key-dev.avbpubkey"

    # Verify required files exist

    if [ ! -f "${PRIVKEY_SOURCE}" ]; then
        bbfatal "Key not found: ${PRIVKEY_SOURCE}"
    fi

    if [ ! -f "${PUBKEY_SOURCE}" ]; then
        bbfatal "Public key not found: ${PUBKEY_SOURCE}"
    fi

    if [ ! -f "${AVBKEY_SOURCE}" ]; then
        bbfatal "Key not found: ${AVBKEY_SOURCE}"
    fi


    # Install dm-verity signing certificate and key
    install -d ${D}${datadir}/${KEYDIR}
    install -m 0644 ${PRIVKEY_SOURCE} ${D}${datadir}/${KEYDIR}/dm-verity-rootfs-signing-key.priv.pem
    install -m 0644 ${PUBKEY_SOURCE} ${D}${datadir}/${KEYDIR}/dm-verity-rootfs-signing-key.pub.pem
    install -m 0644 ${AVBKEY_SOURCE} ${D}${datadir}/${KEYDIR}/dm-verity-rootfs-signing-key.avbpubkey

}

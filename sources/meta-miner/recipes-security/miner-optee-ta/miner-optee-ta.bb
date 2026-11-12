SUMMARY = "Miner OP-TEE TAs and host CA clients"
DESCRIPTION = "Builds the miner trusted applications and their host clients. \
TAs are installed to ${nonarch_base_libdir}/optee_armtz so tee-supplicant can \
load them as REE-FS TAs when OP-TEE core does not already ship an embedded \
early-TA with the same UUID. OP-TEE resolves TA stores by priority \
(early_ta=2 < secstor=4 < ree_fs=9) so the embedded copy always wins when \
present, and this rootfs copy is the fallback for devices running an older \
OP-TEE image that predates early-TA embedding."
LICENSE = "CLOSED"

DEPENDS = "optee-client virtual/optee-os mbedtls"
DEPENDS += "python3-pycryptodomex-native python3-cryptography-native"

inherit python3native

require recipes-security/miner-optee-ta/miner-optee-ta-variant.inc

SRC_URI = " \
    file://optee-ta-src \
    file://is_system_secure \
"

TEEC_EXPORT = "${STAGING_DIR_HOST}${prefix}"
TA_DEV_KIT_DIR = "${STAGING_INCDIR}/optee/export-user_ta"

MINER_TA_SRC = "${WORKDIR}/optee-ta-src"
OUTPUT_DIR = "${WORKDIR}/out"

do_compile() {
    export CFLAGS="${CFLAGS} --sysroot=${STAGING_DIR_HOST}"
    export OPENSSL_MODULES=${STAGING_LIBDIR_NATIVE}/ossl-modules/

    oe_runmake -C ${MINER_TA_SRC} \
        OUTPUT_DIR=${OUTPUT_DIR} \
        TA_DEV_KIT_DIR=${TA_DEV_KIT_DIR} \
        HOST_CROSS_COMPILE=${TARGET_PREFIX} \
        TA_CROSS_COMPILE=${TARGET_PREFIX} \
        TEEC_EXPORT=${TEEC_EXPORT} \
        DEVICE_IDENTITY_VARIANT=${MINER_DEVICE_IDENTITY_VARIANT} \
        MINER_TA_RUNTIME_LOG_LEVEL="${MINER_TA_RUNTIME_LOG_LEVEL}" \
        V=1 \
        lock_ctrl_ta device_identity_ta
}

do_install() {
    install -d ${D}${nonarch_base_libdir}/optee_armtz
    install -m 0444 ${OUTPUT_DIR}/ta/*.ta ${D}${nonarch_base_libdir}/optee_armtz/

    install -d ${D}${bindir}
    install -m 0755 ${OUTPUT_DIR}/ca/lock_ctrl_ta_client ${D}${bindir}/lock_ctrl_ta_client
    install -m 0755 ${OUTPUT_DIR}/ca/${MINER_DEVICE_IDENTITY_SUBDIR}_client ${D}${bindir}/${MINER_DEVICE_IDENTITY_SUBDIR}_client
    install -m 0755 ${WORKDIR}/is_system_secure ${D}${bindir}/is_system_secure
}

INSANE_SKIP:${PN} += "ldflags"

FILES:${PN} += "${nonarch_base_libdir}/optee_armtz/ ${bindir}"
PACKAGE_ARCH = "${MACHINE_ARCH}"

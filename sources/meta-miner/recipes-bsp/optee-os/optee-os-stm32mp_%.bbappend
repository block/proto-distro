FILESEXTRAPATHS:prepend:="${THISDIR}/files:"
FILESEXTRAPATHS:prepend:="${THISDIR}/../../recipes-security/miner-optee-ta/files:"
FILESEXTRAPATHS:prepend:="${THISDIR}/../../dts:"
FILESEXTRAPATHS:prepend:="${THISDIR}/../../dts/cx-common:"
FILESEXTRAPATHS:prepend:="${THISDIR}/../../dts/cx-common/tf-a:"
FILESEXTRAPATHS:prepend:="${THISDIR}/../../dts/c2:"
FILESEXTRAPATHS:prepend:="${THISDIR}/../../dts/c2/optee-os:"
FILESEXTRAPATHS:prepend:="${THISDIR}/../../dts/c2/tf-a:"
FILESEXTRAPATHS:prepend:="${THISDIR}/../../dts/c3:"
FILESEXTRAPATHS:prepend:="${THISDIR}/../../dts/c3/optee-os:"
FILESEXTRAPATHS:prepend:="${THISDIR}/../../dts/c3/tf-a:"

SRC_URI += " \
            file://cx-common \
            file://cx-common/tf-a/stm32mp13-mx.dtsi \
            file://cx-common/stm32mp131f-cx.dtsi \
            file://cx-common/stm32mp131f-cx-pinctrl.dtsi \
            file://c2 \
            file://c2/optee-os \
            file://c2/tf-a \
            file://c3 \
            file://c3/optee-os \
            file://c3/tf-a \
            file://mp135f-dk-remove-pmic-add-fixed-regs.patch \
            file://0001-allow-user-ta-access-bsec_pta.patch \
            file://0001-Add-st-non-secure-otp-post-provision-property-to-otp.patch \
            file://0001-rpmb-return-STORAGE_NOT_AVAILABLE-on-init-failure.patch \
            file://optee-ta-src \
"

DEPENDS += "mbedtls optee-client"

inherit python3native

ST_OPTEE_DEBUG_LOG_LEVEL = "${@bb.utils.contains('MINER_DEVICE_IDENTITY_VARIANT', 'release', '1', '2', d)}"
EXTRA_OEMAKE += "CFG_WITH_USER_TA=y"
EXTRA_OEMAKE += "CFG_RPMB_FS=y"
EXTRA_OEMAKE += "CFG_REE_FS=n"

EXTRA_OEMAKE += "${@bb.utils.contains('MINER_DEVICE_IDENTITY_VARIANT', 'mfg', 'CFG_RPMB_WRITE_KEY=y CFG_TA_STM32MP_NVMEM=y', '', d)}"

EXTRA_OEMAKE += "${@bb.utils.contains('MINER_DEVICE_IDENTITY_VARIANT', 'release', 'CFG_RPMB_WRITE_KEY=n CFG_TA_STM32MP_NVMEM=n CFG_TEE_CORE_DEBUG=n CFG_WARN_INSECURE=n', '', d)}"

# Debug/dev configs
# EXTRA_OEMAKE += "CFG_RPMB_FS_DEBUG_DATA=y"
# EXTRA_OEMAKE += "CFG_RPMB_RESET_FAT=y" # Used for debugging to always rease rpmb on reboot

# Only MP13-based boards support optee-os; mp15x-dk dts files are not included.
do_configure:append() {
    # Copy the common Cx dts files to the build directory
    cp ${WORKDIR}/cx-common/stm32mp131f-cx.dtsi ${S}/core/arch/arm/dts
    cp ${WORKDIR}/cx-common/stm32mp131f-cx-pinctrl.dtsi ${S}/core/arch/arm/dts
    cp ${WORKDIR}/cx-common/tf-a/stm32mp13-mx.dtsi ${S}/core/arch/arm/dts

    # Copy the C2 common dtsi files to the build directory
    cp ${WORKDIR}/c2/optee-os/stm32mp131f-c2-common.dtsi ${S}/core/arch/arm/dts
    # Copy the C2 Proto1 dts files to the build directory
    cp ${WORKDIR}/c2/optee-os/stm32mp131f-c2p1.dts ${S}/core/arch/arm/dts
    # Copy the C3 Proto1 dts files to the build directory
    cp ${WORKDIR}/c3/optee-os/stm32mp131f-c3p1.dts ${S}/core/arch/arm/dts
    # Copy the C2 EVT dts files to the build directory
    cp ${WORKDIR}/c2/optee-os/stm32mp131f-c2evt.dts ${S}/core/arch/arm/dts

    if [ -z "${TARGET}" ]; then
        bberror "TARGET env not defined!!"
    fi
}

# Bake miner TAs into the OP-TEE core via EARLY_TA_PATHS so they are usable
# before the rootfs mounts. attestation_ta and provision_ta share a UUID by
# design (same RPMB namespace), hence only one is ever embedded per variant.
#
# The miner TAs are conf-independent (they only depend on OPTEE_ARCH), so we
# build them once against the dev kit of the first OPTEE_CONF and let every
# upstream per-conf oe_runmake embed the same stripped ELFs via EARLY_TA_PATHS.

require recipes-security/miner-optee-ta/miner-optee-ta-variant.inc

EXTRA_OEMAKE += "CFG_EARLY_TA=y"

MINER_TA_SRC = "${WORKDIR}/optee-ta-src"
MINER_TA_OUT = "${WORKDIR}/miner-ta-out"
TEEC_EXPORT = "${STAGING_DIR_HOST}${prefix}"

MINER_EARLY_TAS_mfg = "lock_ctrl_ta/a75709fa-f0cc-41c6-98ec-b39b1f993116 provision_ta/0d746117-7c6a-43e1-89a3-5927238b28fe"
MINER_EARLY_TAS_release = "lock_ctrl_ta/a75709fa-f0cc-41c6-98ec-b39b1f993116 attestation_ta/0d746117-7c6a-43e1-89a3-5927238b28fe"
MINER_EARLY_TAS = "${@bb.utils.contains('TARGET', 'miner-image-mfg', '${MINER_EARLY_TAS_mfg}', \
                   bb.utils.contains('TARGET', 'miner-image-release', '${MINER_EARLY_TAS_release}', '', d), d)}"

# This overrides upstream do_compile (optee-os-stm32mp-common.inc). Keep in sync
# with upstream if its OPTEE_CONF loop or SoC-detection logic changes.
do_compile() {
    export CFLAGS="${CFLAGS} --sysroot=${STAGING_DIR_HOST}"
    export OPENSSL_MODULES=${STAGING_LIBDIR_NATIVE}/ossl-modules/

    if [ -z "${OPTEE_CONF}" ]; then
        bbfatal "OPTEE_CONF empty; miner early-TA build requires at least one conf"
    fi

    # Echo the CFG_<SOC>=y flag that matches a given conf name.
    soc_flag() {
        for soc in ${STM32MP_SOC_NAME}; do
            case "$1" in
                *${soc}*) echo "$(echo CFG_${soc} | tr a-z A-Z)=y"; return ;;
            esac
        done
    }

    # Build miner TAs once against the first conf's dev kit.
    set -- ${OPTEE_CONF}
    first_conf=$1
    oe_runmake -C ${S} O=${B}/${first_conf} CFG_EMBED_DTB_SOURCE_FILE=${first_conf}.dts $(soc_flag ${first_conf}) ta_dev_kit

    rm -rf ${MINER_TA_OUT}
    oe_runmake -C ${MINER_TA_SRC} \
        OUTPUT_DIR=${MINER_TA_OUT} \
        TA_DEV_KIT_DIR=${B}/${first_conf}/export-ta_${OPTEE_ARCH} \
        HOST_CROSS_COMPILE=${TARGET_PREFIX} \
        TA_CROSS_COMPILE=${TARGET_PREFIX} \
        TEEC_EXPORT=${TEEC_EXPORT} \
        DEVICE_IDENTITY_VARIANT=${MINER_DEVICE_IDENTITY_VARIANT} \
        MINER_TA_RUNTIME_LOG_LEVEL="${MINER_TA_RUNTIME_LOG_LEVEL}" \
        V=1 \
        lock_ctrl_ta device_identity_ta

    early_paths=""
    for ta in ${MINER_EARLY_TAS}; do
        sub=${ta%%/*}
        uuid=${ta#*/}
        early_paths="${early_paths} ${MINER_TA_OUT}/${sub}/ta/${uuid}.stripped.elf"
    done

    # Link OP-TEE core per conf, embedding the miner TAs as early TAs.
    for conf in ${OPTEE_CONF}; do
        oe_runmake -C ${S} O=${B}/${conf} CFG_EMBED_DTB_SOURCE_FILE=${conf}.dts $(soc_flag ${conf}) EARLY_TA_PATHS="${early_paths}"
    done
}

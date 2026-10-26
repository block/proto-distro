FILESEXTRAPATHS:prepend:="${THISDIR}/files:"
FILESEXTRAPATHS:prepend:="${THISDIR}/../../dts:"
FILESEXTRAPATHS:prepend:="${THISDIR}/../../dts/cx-common:"
FILESEXTRAPATHS:prepend:="${THISDIR}/../../dts/cx-common/u-boot:"
FILESEXTRAPATHS:prepend:="${THISDIR}/../../dts/c2:${THISDIR}/../../dts/c2/u-boot:"
FILESEXTRAPATHS:prepend:="${THISDIR}/../../dts/c3:${THISDIR}/../../dts/c3/u-boot:"

SRC_URI += "file://0002-add-default-boot-partition-layout-env-variables.patch \
            file://0003-Enable-gpio-read-cmd-and-feature.patch \
            file://0004-CB-detect-factory-reset-button-using-board-driver.patch \
            file://0005-Detect-IO-board-ID.patch \
            file://0006-Remove-default-eth-phy-GBit-speed-support.patch \
            file://0007-Miner-IO-LED-driver-support-for-proto-cb.patch \
            file://0008-Factory-reset-flow-LED-display.patch \
            file://0009-uboot-to-retry-boot-after-timeout.patch \
            file://0010-Use-mmc-partition-info-to-setup-env-variable.patch \
            file://0011-Harden-uboot-for-fit-image-verify.patch \
            file://0012-avb-Use-rootfs-partitions-for-dm-verity-verification.patch \
            file://0013-Add-CONFIG_AVB_PUBKEY_FILE-to-specify-the-root-avb-p.patch \
            file://0014-Patch-on-top-of-CONFIG_AVB_PUBKEY_FILE-patch.patch \
            file://0015-use-rootfs-over-boot-partition.patch \
            file://0016-Silence-AVB-warning-messages-for-dm-verity-use-case.patch \
            file://0017-Remove-default-defconfig.patch \
            file://0018-Simplify-boot-env-remove-unused-boot-targets.patch \
            file://0020-source-verify-FIT-configurations.patch \
            file://0022-Restore-boot-policy-and-enter-fastboot-on-boot-failure.patch \
            file://cx-common \
            file://cx-common/u-boot \
            file://c2 \
            file://c3 \
            file://enable-dfu-fastboot.cfg \
            file://enable-avb-pubkey.cfg \
            file://default-config.cfg \
"

# Opt in from local.conf to require verification even on OTP-unlocked boards.
# This does not program OTP; boot artifacts must match the configured signing keys.
UBOOT_FORCE_VERIFY ?= "0"
SRC_URI:append = "${@bb.utils.contains('UBOOT_FORCE_VERIFY', '1', ' file://0019-Force-verification-regardless-of-OTP-state.patch', '', d)}"

# Honor board verification policy; verified scripts cannot fall back unsigned.
SRC_URI:append = "${@bb.utils.contains('UBOOT_SIGN_ENABLE', '1', ' file://0021-source-require-signed-script-configurations.patch', '', d)}"

SRC_URI += "file://${@bb.utils.contains('TARGET', 'miner-image-release', 'release-config.cfg', 'mfg-config.cfg', d)} \
"

python () {
    target = d.getVar('TARGET')
    if not target:
        bb.fatal("TARGET is not set; cannot determine whether to use release-config.cfg or mfg-config.cfg")
}

UBOOT_CONFIG_FRAGMENTS += "faster-dfu.cfg"

DEPENDS:remove = " stm32mp-uboot-scr"
DEPENDS:append = " miner-u-boot-scr"
DEPENDS:append = "${@bb.utils.contains('UBOOT_SIGN_ENABLE', '1', ' miner-dev-uboot-keys-native', '', d)}"
DEPENDS:append = "${@bb.utils.contains('DM_VERITY_ENABLE', '1', ' miner-dev-c3-dm-verity-keys-native', '', d)}"

# Make ${DM_VERITY_SIGN_KEYDIR} visible inside U-Boot's make environment
export DM_VERITY_SIGN_KEYDIR = "${WORKDIR}/keys"

do_configure:prepend() {
    mkdir -p ${DM_VERITY_SIGN_KEYDIR}
    if [ -f "${STAGING_DATADIR_NATIVE}/c3_dm_verity_keys/dm-verity-rootfs-signing-key.avbpubkey" ]; then
        cp ${STAGING_DATADIR_NATIVE}/c3_dm_verity_keys/dm-verity-rootfs-signing-key.avbpubkey ${DM_VERITY_SIGN_KEYDIR}/
    else
        bbwarn "AVB public key not found at ${STAGING_DATADIR_NATIVE}/c3_dm_verity_keys/dm-verity-rootfs-signing-key.avbpubkey"
    fi
}

do_configure:append() {
    # Copy C2 Common dtsi files to the build directory
    cp ${WORKDIR}/c2/stm32mp131f-c2-pinctrl.dtsi ${S}/arch/arm/dts
    cp ${WORKDIR}/c2/stm32mp131f-c2-common.dtsi ${S}/arch/arm/dts
    # Copy C2 Proto1 dts files to the build directory
    cp ${WORKDIR}/c2/stm32mp131f-c2p1.dts ${S}/arch/arm/dts
    cp ${WORKDIR}/cx-common/u-boot/stm32mp131f-cx-u-boot.dtsi ${S}/arch/arm/dts/stm32mp131f-c2p1-u-boot.dtsi
    # Copy C2 EVT dts files to the build directory
    cp ${WORKDIR}/c2/stm32mp131f-c2evt.dts ${S}/arch/arm/dts
    cp ${WORKDIR}/cx-common/u-boot/stm32mp131f-cx-u-boot.dtsi ${S}/arch/arm/dts/stm32mp131f-c2evt-u-boot.dtsi
    # Copy C3 Proto1 dts files to the build directory
    cp ${WORKDIR}/c3/u-boot/stm32mp131f-c3p1.dts ${S}/arch/arm/dts
    cp ${WORKDIR}/c3/stm32mp131f-c3-pinctrl.dtsi ${S}/arch/arm/dts
    cp ${WORKDIR}/c3/stm32mp131f-c3-common.dtsi ${S}/arch/arm/dts
    cp ${WORKDIR}/c3/stm32mp131f-c3-io-common.dtsi ${S}/arch/arm/dts
    cp ${WORKDIR}/cx-common/u-boot/stm32mp131f-cx-u-boot.dtsi ${S}/arch/arm/dts/stm32mp131f-c3p1-u-boot.dtsi
    # Copy common Cx dts files to the build directory
    cp ${WORKDIR}/cx-common/stm32mp131f-cx.dtsi ${S}/arch/arm/dts
    cp ${WORKDIR}/cx-common/stm32mp131f-cx-pinctrl.dtsi ${S}/arch/arm/dts
    cp ${WORKDIR}/cx-common/stm32mp131f-cx-thermal-sensor.dtsi ${S}/arch/arm/dts
}

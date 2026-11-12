FILESEXTRAPATHS:prepend := "${THISDIR}/files:"
FILESEXTRAPATHS:prepend:="${THISDIR}/../../dts:"
FILESEXTRAPATHS:prepend:="${THISDIR}/../../dts/cx-common:"
FILESEXTRAPATHS:prepend:="${THISDIR}/../../dts/c2:${THISDIR}/../../dts/c2/kernel:"
FILESEXTRAPATHS:prepend:="${THISDIR}/../../dts/c3:${THISDIR}/../../dts/c3/kernel:"
FILESEXTRAPATHS:prepend:="${THISDIR}/../../dts/mp15x-dk:"

KERNEL_CONFIG_FRAGMENTS:append = " ${WORKDIR}/cx-kernel-config-changes.config"
KERNEL_CONFIG_FRAGMENTS:append = " ${WORKDIR}/dmverity.config"
# These are for debugging purposes only, enable as needed
# KERNEL_CONFIG_FRAGMENTS:append = " ${WORKDIR}/cx-debug-kernel-config-changes.config"
KERNEL_CONFIG_FRAGMENTS:append:c2-p1 = " ${WORKDIR}/c2-kernel-config-changes.config"
KERNEL_CONFIG_FRAGMENTS:append:c2-evt = " ${WORKDIR}/c2-kernel-config-changes.config"
KERNEL_CONFIG_FRAGMENTS:append:c2-evt = " ${WORKDIR}/c2evt-kernel-config-changes.config"
KERNEL_CONFIG_FRAGMENTS:append:c3-p1 = " ${WORKDIR}/c3-kernel-config-changes.config"

DEPENDS:append = "${@bb.utils.contains('UBOOT_SIGN_ENABLE', '1', ' miner-dev-uboot-keys-native', '', d)}"

SRC_URI += "file://cx-kernel-config-changes.config \
            file://cx-debug-kernel-config-changes.config \
            file://c2evt-kernel-config-changes.config \
            file://c2-kernel-config-changes.config \
            file://c3-kernel-config-changes.config \
            file://dmverity.config \
            file://0002-backport-i2c-pec-fix.patch \
            file://0001-Fix-bug-in-stm32-romem-when-writing-to-31th-word.patch \
            file://stm32mp157d-dk1.dts \
            file://stm32mp157f-dk2.dts \
            file://c2 \
            file://c3 \
            file://cx-common \
"

do_configure:append() {
    # Copy the DK1/DK2 dts files to the build directory
    cp ${WORKDIR}/stm32mp157d-dk1.dts ${S}/arch/arm/boot/dts
    cp ${WORKDIR}/stm32mp157f-dk2.dts ${S}/arch/arm/boot/dts
    # Copy the C2 Proto1 and EVT dts files to the build directory
    cp ${WORKDIR}/c2/stm32mp131f-c2p1.dts ${S}/arch/arm/boot/dts
    cp ${WORKDIR}/c2/stm32mp131f-c2evt.dts ${S}/arch/arm/boot/dts
    cp ${WORKDIR}/c2/stm32mp131f-c2-pinctrl.dtsi ${S}/arch/arm/boot/dts
    cp ${WORKDIR}/c2/stm32mp131f-c2-common.dtsi ${S}/arch/arm/boot/dts
    # Copy the C3 Proto1 dts files to the build directory
    cp ${WORKDIR}/c3/stm32mp131f-c3p1.dts ${S}/arch/arm/boot/dts
    cp ${WORKDIR}/c3/stm32mp131f-c3p1-io-rgb.dts ${S}/arch/arm/boot/dts
    cp ${WORKDIR}/c3/stm32mp131f-c3-pinctrl.dtsi ${S}/arch/arm/boot/dts
    cp ${WORKDIR}/c3/stm32mp131f-c3-common.dtsi ${S}/arch/arm/boot/dts
    cp ${WORKDIR}/c3/stm32mp131f-c3-io-common.dtsi ${S}/arch/arm/boot/dts
    # Copy the common Cx dts files to the build directory
    cp ${WORKDIR}/cx-common/stm32mp131f-cx.dtsi ${S}/arch/arm/boot/dts
    cp ${WORKDIR}/cx-common/stm32mp131f-cx-pinctrl.dtsi ${S}/arch/arm/boot/dts
    cp ${WORKDIR}/cx-common/stm32mp131f-cx-thermal-sensor.dtsi ${S}/arch/arm/boot/dts
}

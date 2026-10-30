SUMMARY = "Provide custom miner boot script for U-Boot"
LICENSE = "CLOSED"

DEPENDS += "u-boot-mkimage-native u-boot-tools-native dtc-native"
DEPENDS:append = "${@bb.utils.contains('UBOOT_SIGN_ENABLE', '1', ' miner-dev-uboot-keys-native', '', d)}"

PACKAGE_ARCH = "${MACHINE_ARCH}"

# Select boot script based on image variant
# Release images use boot-release.scr.cmd (with dm-verity/AVB verification)
# Manufacturing images use boot-default.scr.cmd.
BOOTSCR = "${@bb.utils.contains('TARGET', 'miner-image-release', 'boot-release.scr.cmd', 'boot-default.scr.cmd', d)}"

# Conditionally include only the required boot script file
SRC_URI = "file://${BOOTSCR}"
SRC_URI:append = "${@bb.utils.contains('UBOOT_SIGN_ENABLE', '1', ' file://u-boot-scr-fit-image.its', '', d)}"

PV = "3.1.1"

inherit kernel-arch uboot-config deploy

B = "${WORKDIR}/build"
UBOOT_BOOTSCR = "${WORKDIR}/${BOOTSCR}"
UBOOT_BOOTSCR_IMG = "${WORKDIR}/boot.scr.uimg"

UBOOT_INSTALL_DIR ?= "/boot"

do_compile() {
    if [ "${UBOOT_SIGN_ENABLE}" = "1" ]; then
        # Update its file with signing info
        sed -i 's/@@BOOTSCR@@/${BOOTSCR}/g' ${WORKDIR}/u-boot-scr-fit-image.its
        sed -i 's/@@UBOOT_SIGN_KEYNAME@@/${UBOOT_SIGN_KEYNAME}/g' ${WORKDIR}/u-boot-scr-fit-image.its

        if [ -e ${UBOOT_SIGN_KEYDIR} ]; then
            uboot-mkimage -k ${UBOOT_SIGN_KEYDIR} -f ${WORKDIR}/u-boot-scr-fit-image.its ${UBOOT_BOOTSCR_IMG}
        else
            bberror "No signing keys found in ${UBOOT_SIGN_KEYDIR}"
        fi
    else
        uboot-mkimage -C none -A ${UBOOT_ARCH} -T script -d ${UBOOT_BOOTSCR} ${UBOOT_BOOTSCR_IMG}
    fi
}

do_install() {
    install -d ${D}/${UBOOT_INSTALL_DIR}
    # Install boot script
    if [ -e ${UBOOT_BOOTSCR_IMG} ]; then
        install -m 755 ${UBOOT_BOOTSCR_IMG} ${D}/${UBOOT_INSTALL_DIR}
    fi
}

do_deploy() {
    if [ "${UBOOT_SIGN_ENABLE}" = "1" ]; then
        install -d ${DEPLOYDIR}/kernel
        install -m 755 ${WORKDIR}/u-boot-scr-fit-image.its ${DEPLOYDIR}/kernel
    fi
}

addtask do_deploy after do_install

FILES:${PN} = "${UBOOT_INSTALL_DIR}"

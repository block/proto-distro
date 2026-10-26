FILESEXTRAPATHS:prepend := "${THISDIR}/files:"

SRC_URI:append = " \
    file://09-swupdate-args.in \
    file://swupdate.cfg \
    file://defconfig \
"

DEPENDS:append = "${@bb.utils.contains('SWUPDATE_SIGNING', 'RSA', ' miner-dev-swupdate-keys-native', '', d)}"

SWUPDATE_INIT = "tiny"

# Disable main swupdate service
SYSTEMD_AUTO_ENABLE:${PN} = "disable"

RDEPENDS:${PN} += "bash"

do_swupdate_args_update() {
    # Yocto dependency checking can be broken if we modify the source file
    # directly during the build process, create a 'output' file to modify
    cp ${WORKDIR}/09-swupdate-args.in ${WORKDIR}/09-swupdate-args
    sed -i -e "s%__MEMFAULT_HARDWARE_VERSION%${MEMFAULT_HARDWARE_VERSION}%" ${WORKDIR}/09-swupdate-args
    sed -i -e "s%__UBOOT_ENV_PARTITION_A%${UBOOT_ENV_PARTITION_A}%" ${WORKDIR}/09-swupdate-args
}
addtask do_swupdate_args_update before do_install after do_unpack

do_install:append() {
    install -Dm 0644 ${WORKDIR}/09-swupdate-args ${D}${libdir}/swupdate/conf.d/09-swupdate-args
    install -Dm 0644 ${WORKDIR}/swupdate.cfg ${D}${sysconfdir}/swupdate.cfg

    if [ "${SWUPDATE_SIGNING}" = "RSA" ]; then
        install -Dm 0755 ${SWUPDATE_PUBLIC_KEY} ${D}${sysconfdir}/swupdate/public_key.pem
    fi

    # Create status directory
    install -d ${D}${localstatedir}/lib/swupdate
}

FILES:${PN} += "${localstatedir}/lib/swupdate"

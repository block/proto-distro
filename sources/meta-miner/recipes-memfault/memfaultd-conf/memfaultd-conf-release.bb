require memfaultd-conf.inc

SRC_URI:append = " \
    file://memfaultd_release.conf.in \
"

MEMFAULT_SOFTWARE_TYPE := "proto-distro-image"

do_install:append() {
    # Yocto dependency checking can be broken if we modify the source file
    # directly during the build process, create a 'output' file to modify
    cp ${WORKDIR}/memfaultd_release.conf.in ${WORKDIR}/memfaultd_release.conf
    
    sed -i -e "s%MEMFAULT_PROJECT_KEY%${MEMFAULT_PROJECT_KEY}%" ${WORKDIR}/memfaultd_release.conf
    sed -i -e "s%MEMFAULT_SOFTWARE_TYPE%${MEMFAULT_SOFTWARE_TYPE}%" ${WORKDIR}/memfaultd_release.conf
    sed -i -e "s%MEMFAULT_SOFTWARE_VERSION%${DISTRO_VERSION}%" ${WORKDIR}/memfaultd_release.conf

    install -d ${D}${sysconfdir}
    install -m 0644 ${WORKDIR}/memfaultd_release.conf ${D}${sysconfdir}/memfaultd.conf

    # Ensure this directory exists to generate the swupdate.cfg file
    install -d ${D}${sysconfdir}/memfault-generated
}

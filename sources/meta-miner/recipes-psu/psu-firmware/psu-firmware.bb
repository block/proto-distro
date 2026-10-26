SUMMARY = "PSU Firmware Files"
DESCRIPTION = "PSU firmware files installed to /etc/psu-firmware"
LICENSE = "CLOSED"

# Firmware files should be placed in files/ directory
SRC_URI += "file://Boco_BS402A17_PKG_13_13_15.bin"
SRC_URI += "file://S4K0E001P_00.14.18_BANK1.bin"
SRC_URI += "file://Boco_BS502A17_PKG_20_20_22.bin"


S = "${WORKDIR}"

do_install() {
    install -d ${D}/etc/psu-firmware

    # Install all firmware files (*.bin, *.fw, *.img) if they exist
    # The -f test handles non-existent patterns safely in POSIX sh
    for fw in ${WORKDIR}/*.bin ${WORKDIR}/*.fw ${WORKDIR}/*.img; do
        if [ -f "$fw" ]; then
            install -m 0444 "$fw" ${D}/etc/psu-firmware/
        fi
    done
}

FILES:${PN} = "/etc/psu-firmware/*"

# Append configuration for PSU PMBus driver sensors

FILESEXTRAPATHS:prepend := "${THISDIR}/files:"

SRC_URI += "file://psu-sensors.conf"

do_install:append() {
    # Install PSU sensor configuration to sensors.d directory
    install -d ${D}${sysconfdir}/sensors.d
    install -m 0644 ${WORKDIR}/psu-sensors.conf ${D}${sysconfdir}/sensors.d/psu-pmbus.conf
}

# Ensure the PSU config is included in the libsensors package
FILES:${PN}-libsensors += "${sysconfdir}/sensors.d/psu-pmbus.conf"

# Add runtime dependency on our PSU driver
RDEPENDS:${PN}-libsensors += "kernel-module-psu-pmbus-driver"
FILESEXTRAPATHS:prepend:="${THISDIR}/files:"

SRC_URI += "file://fw_env.config.mmc"

# Create a symlink to the correct fw_env.config file for fw_printenv/setenv
do_install:append() {
    ln -sf fw_env.config.mmc ${D}${sysconfdir}/fw_env.config
}

FILESEXTRAPATHS:prepend := "${THISDIR}/files:"

SRC_URI += "file://opkg-guard.sh"

# Wrap the target opkg binary with a guard that refuses rootfs-mutating
# operations (install/remove/upgrade/...) when / is mounted read-only. On
# release images / is read-only, and such operations otherwise fail part-way
# and leave a corrupt opkg state. Queries still work. The native/nativesdk opkg
# used by the build itself is left untouched (class-target only).
do_install:append:class-target() {
    mv ${D}${bindir}/opkg ${D}${bindir}/opkg.real
    install -m 0755 ${WORKDIR}/opkg-guard.sh ${D}${bindir}/opkg
}

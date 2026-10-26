SUMMARY = "Precompiled SWUpdate orchestration tool"
LICENSE = "CLOSED"

inherit bin_package deploy

FILESEXTRAPATHS:prepend := "${THISDIR}/../../../../dist/proto-swupdate/yocto:"
# GoReleaser emits a tar-based IPK; BitBake's IPK unpacker expects ar.
SRC_URI = "file://proto-swupdate.ipk;subdir=proto-swupdate;unpack=0"
S = "${WORKDIR}/proto-swupdate"

PV = "${@(lambda version: version if version[:1].isdigit() else '0.0.0+' + version)((d.getVar('DISTRO_VERSION') or '').removeprefix('v').replace('-', '+'))}"

python () {
    if not d.getVar('DISTRO_VERSION'):
        bb.fatal("proto-swupdate requires a nonempty DISTRO_VERSION")
}

COMPATIBLE_HOST = "arm.*-linux"
INHIBIT_PACKAGE_STRIP = "1"
INHIBIT_PACKAGE_DEBUG_SPLIT = "1"

unpack_proto_swupdate() {
    tar -xOf ${S}/proto-swupdate.ipk ./data.tar.gz | tar --extract --gzip --no-same-owner -C ${S}
}
do_unpack[postfuncs] += "unpack_proto_swupdate"

do_install() {
    install -d ${D}${bindir}
    install -m 0755 ${S}${bindir}/proto-swupdate ${D}${bindir}/proto-swupdate
}

do_deploy() {
    install -d ${DEPLOYDIR}
    install -m 0755 ${S}${bindir}/proto-swupdate ${DEPLOYDIR}/proto-swupdate
}
addtask deploy after do_unpack before do_build

FILES:${PN} = "${bindir}/proto-swupdate"
RREPLACES:${PN} = "fwu-metadata-tool"

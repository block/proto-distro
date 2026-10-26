FILESEXTRAPATHS:prepend := "${THISDIR}/files:"

SRC_URI += "file://timesyncd.conf \
            file://logind.conf \
            file://logind_c2p1.conf \
            file://journald.conf \
            file://sysctl.conf \
            file://40-miner-hardening.conf \
            file://journal-directory.conf \
            file://0001-Change-Long-press-duration-to-10-sec.patch \
            "

PACKAGECONFIG:append = " networkd resolved"

do_configure:append() {
    sed -i -e "s/enable systemd-timesyncd.service/disable systemd-timesyncd.service/g" ${S}/presets/90-systemd.preset
    sed -i -e "s/enable systemd-resolved.service/disable systemd-resolved.service/g" ${S}/presets/90-systemd.preset
}

do_install:append() {
    install -d ${D}${sysconfdir}/systemd
    install -d ${D}${sysconfdir}/systemd/journald.conf.d

    install -m 0644 ${WORKDIR}/timesyncd.conf ${D}${sysconfdir}/systemd/timesyncd.conf

    if echo ${MACHINE} | grep -q "c2-p1"; then
        install -m 0644 ${WORKDIR}/logind_c2p1.conf ${D}${sysconfdir}/systemd/logind.conf
    else
        install -m 0644 ${WORKDIR}/logind.conf ${D}${sysconfdir}/systemd/logind.conf
    fi

    install -m 0644 ${WORKDIR}/journald.conf ${D}${sysconfdir}/systemd/journald.conf.d/journald.conf

    install -m 0644 ${WORKDIR}/sysctl.conf ${D}${sysconfdir}/sysctl.d/sysctl.conf

    # Ship vendor policy outside the persistent /etc overlay for OTA updates.
    # Use vendor priority 40; local overrides should use later filenames (60-90).
    install -d ${D}${exec_prefix}/lib/sysctl.d
    install -m 0644 ${WORKDIR}/40-miner-hardening.conf ${D}${exec_prefix}/lib/sysctl.d/40-miner-hardening.conf

    # Install tmpfiles configuration to create /var/log/journal directory at runtime
    install -d ${D}${sysconfdir}/tmpfiles.d
    install -m 0644 ${WORKDIR}/journal-directory.conf ${D}${sysconfdir}/tmpfiles.d/journal-directory.conf
}

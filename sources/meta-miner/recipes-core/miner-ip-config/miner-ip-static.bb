SUMMARY = "Setup systemd network configs for a configurable IP configuration, defaulting to static"
DESCRIPTION = "${SUMMARY}"
LICENSE = "CLOSED"
LIC_FILES_CHKSUM = ""
DEPENDS = "systemd-conf"

# ----------------------------------------
# Prepend to FILESEXTRAPATHS
# ----------------------------------------
FILESEXTRAPATHS:prepend := "${THISDIR}/files:"

# -------------------------------------
# Append to SRC_URI the network configs
# -------------------------------------
SRC_URI:append :=  "file://10-eth-dhcp.network \
                    file://11-eth-static.network \
                    file://10-en-dhcp.network \
                    file://11-en-static.network \
"

# -----------------------------------------------
# Set the Files List for DHCP & STATIC ip configs
# -----------------------------------------------
FILES:${PN} += "${sysconfdir}/systemd/network/10-eth-dhcp.network \
                ${sysconfdir}/systemd/network/11-eth-static.network \
                ${sysconfdir}/systemd/network/10-en-dhcp.network \
                ${sysconfdir}/systemd/network/11-en-static.network \
"

# -----------------------------
# Finally, install systemd conf
# -----------------------------
do_install() {
    install -d ${D}${sysconfdir}/systemd/network
    install -m 0644 ${WORKDIR}/10-eth-dhcp.network ${D}${sysconfdir}/systemd/network/10-eth-dhcp.network.inactive
    install -m 0644 ${WORKDIR}/11-eth-static.network ${D}${sysconfdir}/systemd/network/11-eth-static.network
}

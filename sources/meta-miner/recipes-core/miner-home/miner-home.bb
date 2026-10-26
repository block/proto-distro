SUMMARY = "Setup writeable overlayfs for /home"
DESCRIPTION = "${SUMMARY}"
LICENSE = "CLOSED"
LIC_FILES_CHKSUM = ""

inherit overlayfs

OVERLAYFS_MOUNT_POINT[home] = "/data/overlay-home"
OVERLAYFS_WRITABLE_PATHS[home] = "/home"

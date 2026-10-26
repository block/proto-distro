FILESEXTRAPATHS:prepend := "${THISDIR}/files:"

# Security-patched host tar uses openat2, bypassing this pseudo's fd tracking.
SRC_URI += "file://0001-pseudo-handle-openat2-syscall.patch"

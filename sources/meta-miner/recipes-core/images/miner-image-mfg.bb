inherit core-image
require miner-image-swupdate.bb

DESCRIPTION = "Image used for manufacturing purposes."

VARIANT = "mfg"

EXTRA_IMAGE_FEATURES += "debug-tweaks"

IMAGE_INSTALL += "\
    packagegroup-proto-mfg \
    packagegroup-proto-memfault \
    miner-boot \
    miner-hostname \
    miner-serial-autologin-root \
    "

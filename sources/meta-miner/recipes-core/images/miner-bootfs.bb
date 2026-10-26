DESCRIPTION = "Minimal bootfs image with only miner-u-boot-scr"

include recipes-st/images/st-image-partitions.inc

IMAGE_INSTALL = "miner-u-boot-scr"

# Disable features we don't need for this minimal image
IMAGE_FEATURES = ""
EXTRA_IMAGE_FEATURES = ""
PACKAGE_INSTALL = "${IMAGE_INSTALL}"

# Set a small size for the image
IMAGE_ROOTFS_SIZE = "512"
IMAGE_ROOTFS_EXTRA_SPACE = "0"

IMAGE_FSTYPES = "ext4"

IMAGE_NAME_SUFFIX = ".bootfs"

IMAGE_PARTITION_MOUNTPOINT = "/boot"

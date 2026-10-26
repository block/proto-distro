DESCRIPTION = "Data filesystem image with nothing installed"
LICENSE = "MIT"

include recipes-st/images/st-image-partitions.inc

IMAGE_FEATURES = ""
EXTRA_IMAGE_FEATURES = ""
PACKAGE_INSTALL = ""

IMAGE_ROOTFS_MAXSIZE = ""
IMAGE_ROOTFS_SIZE = "4096"
IMAGE_ROOTFS_EXTRA_SPACE = "0"
IMAGE_FSTYPES = "ext4"

IMAGE_NAME_SUFFIX = ".datafs"

IMAGE_PARTITION_MOUNTPOINT = "/data"

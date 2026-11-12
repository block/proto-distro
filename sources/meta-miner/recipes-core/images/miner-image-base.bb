DESCRIPTION = "Base image recipe for all proto image variants. This image is not meant to be booted into directly, is still a buildable standalone target"

inherit core-image

VARIANT ?= "base"

# Explicitly set IMAGE_INSTALL for full control over packages
IMAGE_INSTALL = "\
    packagegroup-core-boot \
    packagegroup-proto-base \
    kernel-imagebootfs \
    ${CORE_IMAGE_EXTRA_INSTALL} \
    "

IMAGE_FEATURES += "overlayfs-etc"
OVERLAYFS_ETC_INIT_TEMPLATE = "${THISDIR}/files/overlayfs-etc-data-preinit.sh.in"

# Ship opkg runtime + on-image package index so packages can be inspected/
# installed from local .ipk files at runtime on all proto images.
IMAGE_FEATURES += "package-management"

# overlayfs-etc (mode OVERLAYFS_ETC_USE_ORIG_INIT_NAME=1, the default) declares a
# hard conflict with package-management because it replaces /sbin/init and an
# opkg upgrade of the init manager would clobber the generated init. We only use
# opkg for local inspection/install of .ipk files, not system upgrades, so clear
# the conflict. Caveat: do not `opkg upgrade` systemd/init on a writable image.
IMAGE_FEATURES_CONFLICTS_overlayfs-etc = ""

IMAGE_FSTYPES += "ext4.gz"

add_variantinfo () {
     echo 'VARIANT="${VARIANT}"' >> ${IMAGE_ROOTFS}/usr/lib/os-release
}

remove_unused_boot_files() {
    rm -f ${IMAGE_ROOTFS}/boot/Image*
    rm -f ${IMAGE_ROOTFS}/boot/zImage*
    rm -f ${IMAGE_ROOTFS}/boot/vmlinux*
}

add_data_partition_to_fstab() {
    echo "/dev/mmcblk0p${UBOOT_ENV_PARTITION_DATA} /data ext4 rw,sync 0 2" >> ${IMAGE_ROOTFS}${sysconfdir}/fstab
}

# Create mount points and directories needed by preinit before dm-verity locks rootfs
# With dm-verity, rootfs becomes truly read-only, so all directories must pre-exist
create_data_mountpoint() {
    mkdir -p ${IMAGE_ROOTFS}/data
    # These directories are created by preinit but must exist when rootfs is read-only
    mkdir -p ${IMAGE_ROOTFS}/proc
    mkdir -p ${IMAGE_ROOTFS}/sys
    mkdir -p ${IMAGE_ROOTFS}/run
    mkdir -p ${IMAGE_ROOTFS}/var/run
}

do_image[depends] += "miner-bootfs:do_image_complete"
do_image[depends] += "miner-datafs:do_image_complete"

ROOTFS_POSTPROCESS_COMMAND += " add_variantinfo ; remove_unused_boot_files ; add_data_partition_to_fstab ; create_data_mountpoint ; "

include conf/machine/include/st-machine-common-stm32mp.inc
include conf/machine/include/st-machine-providers-stm32mp.inc

# Define specific family common machine name
MACHINEOVERRIDES .= ":stm32mp1common:stm32mp13common"

# =========================================================================
# Chip architecture
# =========================================================================
DEFAULTTUNE = "cortexa7thf-neon-vfpv4"
include conf/machine/include/arm/armv7a/tune-cortexa7.inc

# Miner application .ipk packages are built externally (cargo-nfpm / goreleaser)
# and stamped with the Debian-style architecture "armhf", which is not part of
# the OE tune-arch namespace. Declare it as a runtime-compatible package
# architecture so opkg-arch-config emits "arch armhf" into /etc/opkg/arch.conf;
# otherwise the on-device opkg hides those packages from `opkg list[-installed]`
# even when their status is "installed".
PACKAGE_EXTRA_ARCHS:append = " armhf"

# =========================================================================
# boot scheme
# =========================================================================
BOOTSCHEME_LABELS += "optee"

# =========================================================================
# boot device
# =========================================================================
# Define the boot device supported
BOOTDEVICE_LABELS += "emmc"

UBOOT_ENV_PARTITION_A = "7"
UBOOT_ENV_PARTITION_B = "8"
UBOOT_ENV_PARTITION_RECOVERY = "9"
UBOOT_ENV_PARTITION_DATA = "10"

UBOOT_ENV_DEVNUM = "1"
UBOOT_ENV_BOOT_PARTNUM = "${UBOOT_ENV_PARTITION_A}"

# Overlayfs configuration
OVERLAYFS_ETC_MOUNT_POINT = "/data"
OVERLAYFS_ETC_DEVICE = "/dev/mmcblk0p${UBOOT_ENV_PARTITION_DATA}"
OVERLAYFS_ETC_FSTYPE = "ext4"


# =========================================================================
# Build kernel fitImage
# =========================================================================
KERNEL_IMAGETYPE = "fitImage"
KERNEL_CLASSES = "kernel-fitimage"

# Disable all ST image types
ST_VENDORFS = "0"
ST_USERFS   = "0"
ST_BOOTFS   = "0"

IMAGE_INSTALL:append = " libstdc++"

# Enable Uboot script + Linux signing
UBOOT_SIGN_ENABLE = "1"
UBOOT_FITIMAGE_ENABLE="1"
UBOOT_MKIMAGE_DTCOPTS = "-I dts -O dtb -p 2000"
FIT_SIGN_INDIVIDUAL = "1"

UBOOT_EXTLINUX = "0"

# Disable U-Boot FIT image signing which is not needed due to Chain of Trust certificates
# This triggers the following bbwarn: "Failure while adding public key to u-boot binary. Verified boot won't be available."
# This error is noise since `kernel-fitimage.bbclass` takes care of signing the FIT image and
# adding the public key to U-boot's devicetree.
SPL_SIGN_ENABLE = "0"
SPL_DTB_BINARY = ""

UBOOT_SIGN_KEYNAME = "linux-signing-key"
UBOOT_SIGN_KEYDIR = "${STAGING_DATADIR_NATIVE}/uboot_dev_keys"

# Enable Optee/Uboot Signing
TF_A_SIGN_ENABLE="1"

# The TFA image signing key and FIP signing key maybe be different keys
TFA_SIGN_KEY_FILE="privateKey01.pem"
TFA_SIGN_KEY_PASS="qwert2"
FIP_SIGN_KEY_FILE="privateKey00.pem"
FIP_SIGN_KEY_PASS="qwert1"
STM32MP1_SIGN_KEYDIR = "${STAGING_DATADIR_NATIVE}/stm32mp13_dev_keys"
TFA_SIGN_KEY_stm32mp13="${STM32MP1_SIGN_KEYDIR}/${TFA_SIGN_KEY_FILE}"
FIP_SIGN_KEY_stm32mp13="${STM32MP1_SIGN_KEYDIR}/${FIP_SIGN_KEY_FILE}"

# TBBR non-volatile counter values burned into FIP certificates by cert_create.
# Must be >= the hardcoded plat_get_nv_ctr return value in TF-A (see
# 0006-tf-a-bypass-nvcounter-rollback-check.patch).
TFW_NVCTR_VAL ?= "2"
NTFW_NVCTR_VAL ?= "2"

# =========================================================================
# dm-verity support
# =========================================================================
MACHINE_FEATURES += "dm-verity"

# Enable dm-verity signing similar to FIT image signing
DM_VERITY_ENABLE = "1"
DM_VERITY_SIGN_KEYNAME = "dm-verity-rootfs-signing-key"
DM_VERITY_SIGN_KEYDIR = "${STAGING_DATADIR_NATIVE}/c3_dm_verity_keys"

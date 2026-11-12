# Shared variant detection for miner OP-TEE recipes.
#
# TARGET is set by the build CLI (miner-image-mfg / miner-image-release).
# MINER_DEVICE_IDENTITY_VARIANT: passed to the miner TA Makefile as DEVICE_IDENTITY_VARIANT.
# MINER_DEVICE_IDENTITY_SUBDIR: subdir name (provision_ta / attestation_ta) that ships for the variant.

MINER_DEVICE_IDENTITY_VARIANT = "${@bb.utils.contains('TARGET', 'miner-image-mfg', 'mfg', \
                                   bb.utils.contains('TARGET', 'miner-image-release', 'release', '', d), d)}"

MINER_DEVICE_IDENTITY_SUBDIR = "${@bb.utils.contains('MINER_DEVICE_IDENTITY_VARIANT', 'mfg', 'provision_ta', \
                                  bb.utils.contains('MINER_DEVICE_IDENTITY_VARIANT', 'release', 'attestation_ta', '', d), d)}"

# OP-TEE has no warning level: 1 retains errors and suppresses INFO at runtime.
MINER_TA_RUNTIME_LOG_LEVEL = "${@bb.utils.contains('MINER_DEVICE_IDENTITY_VARIANT', 'release', '1', '', d)}"

python __anonymous() {
    if not d.getVar('MINER_DEVICE_IDENTITY_VARIANT'):
        bb.fatal("TARGET='%s' not recognized for %s; expected miner-image-mfg or miner-image-release"
                 % (d.getVar('TARGET'), d.getVar('PN')))
}

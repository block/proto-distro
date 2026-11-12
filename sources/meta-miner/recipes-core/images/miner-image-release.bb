inherit core-image
inherit extrausers
require miner-image-swupdate.bb

DESCRIPTION = "Image optimized & secured for release to customers."

VARIANT = "release"

IMAGE_FEATURES += "read-only-rootfs"

# Root is blocked by nologin shell only - shadow-level locking (-L -e 1)
# would propagate via /etc overlay copy-up if anyone ran passwd(1) and
# break release->mfg OTA.
EXTRA_USERS_PARAMS:append = "\
    usermod -s /sbin/nologin root; \
    "

IMAGE_INSTALL += "\
    packagegroup-proto-essential \
    packagegroup-proto-release \
    packagegroup-proto-memfault \
    "

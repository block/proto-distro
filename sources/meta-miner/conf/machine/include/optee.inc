FIP_DEVICETREE_OPTEE = "${MINER_DEVICETREE}"
DISTRO_FEATURES:append = " optee"
IMAGE_INSTALL:append = " optee-os-stm32mp"

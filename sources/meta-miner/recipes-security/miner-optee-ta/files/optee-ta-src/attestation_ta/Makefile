export V?=0

# If _HOST or _TA specific compilers are not specified, then use CROSS_COMPILE
HOST_CROSS_COMPILE ?= $(CROSS_COMPILE)
TA_CROSS_COMPILE ?= $(CROSS_COMPILE)

HOST_COMMON_LIB_DIR ?= $(COMMON_LIB_DIR)/host/
TA_COMMON_LIB_DIR ?= $(COMMON_LIB_DIR)/ta/

TA_NAME="attestation_ta"

.PHONY: all
all:
	@test -e $(OUTPUT_DIR)/$(TA_NAME) || mkdir -p $(OUTPUT_DIR)/$(TA_NAME)
	$(MAKE) -C host OUTPUT_DIR=$(OUTPUT_DIR)/$(TA_NAME) CROSS_COMPILE="$(HOST_CROSS_COMPILE)" COMMON_LIB_DIR="$(HOST_COMMON_LIB_DIR)" --no-builtin-variables
	$(MAKE) -C ta OUTPUT_DIR=$(OUTPUT_DIR)/$(TA_NAME) CROSS_COMPILE="$(TA_CROSS_COMPILE)" COMMON_LIB_DIR="$(TA_COMMON_LIB_DIR)" LDFLAGS=""

.PHONY: clean
clean:
	$(MAKE) -C host clean

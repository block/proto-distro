export V?=0

HOST_CROSS_COMPILE ?= $(CROSS_COMPILE)
TA_CROSS_COMPILE ?= $(CROSS_COMPILE)

.PHONY: all
all:
	@test -e $(OUTPUT_DIR)/libminer_optee_common || mkdir -p $(OUTPUT_DIR)/libminer_optee_common
	$(MAKE) -C host OUTPUT_DIR=$(OUTPUT_DIR)/libminer_optee_common  CROSS_COMPILE="$(HOST_CROSS_COMPILE)" --no-builtin-variables
	$(MAKE) -C ta OUTPUT_DIR=$(OUTPUT_DIR)/libminer_optee_common CROSS_COMPILE="$(TA_CROSS_COMPILE)" LDFLAGS=""

.PHONY: clean
clean:
	$(MAKE) -C host clean

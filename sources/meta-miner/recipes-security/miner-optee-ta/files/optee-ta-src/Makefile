export V ?= 0

OUTPUT_DIR ?= $(CURDIR)/out
DEVICE_IDENTITY_VARIANT ?= release

COMMON_INC := -I$(CURDIR)/libminer_optee_common/host/include/ -I$(CURDIR)/libminer_optee_common/ta/include/

TA_LIST := $(subst /,,$(dir $(wildcard *_ta/Makefile)))

.PHONY: clean
clean: clean prepare-for-rootfs-clean

prepare:
	@mkdir -p $(OUTPUT_DIR)
	@mkdir -p $(OUTPUT_DIR)/ta
	@mkdir -p $(OUTPUT_DIR)/ca

lib_optee_common:
	$(MAKE) -C libminer_optee_common CROSS_COMPILE="$(HOST_CROSS_COMPILE)"

check_device_identity_variant:
	@if [ "$(DEVICE_IDENTITY_VARIANT)" != "mfg" ] && [ "$(DEVICE_IDENTITY_VARIANT)" != "release" ]; then \
		echo "Invalid DEVICE_IDENTITY_VARIANT='$(DEVICE_IDENTITY_VARIANT)'. Expected one of: mfg, release"; \
		exit 1; \
	fi

device_identity_ta: prepare lib_optee_common check_device_identity_variant
	@if [ "$(DEVICE_IDENTITY_VARIANT)" = "mfg" ]; then \
		$(MAKE) -C $(CURDIR)/provision_ta CROSS_COMPILE="$(HOST_CROSS_COMPILE)" COMMON_INC="$(COMMON_INC)" COMMON_LIB_DIR="$(OUTPUT_DIR)/libminer_optee_common" && \
		cp -p $(OUTPUT_DIR)/provision_ta/host/*_client $(OUTPUT_DIR)/ca/ && \
		cp -pr $(OUTPUT_DIR)/provision_ta/ta/*.ta $(OUTPUT_DIR)/ta/; \
	else \
		$(MAKE) -C $(CURDIR)/attestation_ta CROSS_COMPILE="$(HOST_CROSS_COMPILE)" COMMON_INC="$(COMMON_INC)" COMMON_LIB_DIR="$(OUTPUT_DIR)/libminer_optee_common" && \
		cp -p $(OUTPUT_DIR)/attestation_ta/host/*_client $(OUTPUT_DIR)/ca/ && \
		cp -pr $(OUTPUT_DIR)/attestation_ta/ta/*.ta $(OUTPUT_DIR)/ta/; \
	fi

%_ta: prepare lib_optee_common
	$(MAKE) -C $(CURDIR)/$@ CROSS_COMPILE="$(HOST_CROSS_COMPILE)" COMMON_INC="$(COMMON_INC)" COMMON_LIB_DIR="$(OUTPUT_DIR)/libminer_optee_common"
	cp -p $(OUTPUT_DIR)/$@/host/*_client $(OUTPUT_DIR)/ca/;
	cp -pr $(OUTPUT_DIR)/$@/ta/*.ta $(OUTPUT_DIR)/ta/;

clean:
	@for ta in $(TA_LIST); do \
		$(MAKE) -C $$ta clean || exit 1; \
	done

prepare-for-rootfs-clean:
	@rm -rf $(OUTPUT_DIR)

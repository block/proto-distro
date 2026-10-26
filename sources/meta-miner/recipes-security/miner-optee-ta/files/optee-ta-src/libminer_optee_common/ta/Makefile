CFG_TEE_TA_LOG_LEVEL ?= 2

O=$(OUTPUT_DIR)/ta
LIBNAME=$(O)/libminer_optee_ta_common

all:
	@test -e $(O) || mkdir -p $(O)

ifneq ($(strip $(MINER_TA_RUNTIME_LOG_LEVEL)),)
CFLAGS += -DMINER_TA_RUNTIME_LOG_LEVEL=$(MINER_TA_RUNTIME_LOG_LEVEL)
endif

-include $(TA_DEV_KIT_DIR)/mk/ta_dev_kit.mk

ifeq ($(wildcard $(TA_DEV_KIT_DIR)/mk/ta_dev_kit.mk), )
clean:
	@echo 'Note: $$(TA_DEV_KIT_DIR)/mk/ta_dev_kit.mk not found, cannot clean TA'
	@echo 'Note: TA_DEV_KIT_DIR=$(TA_DEV_KIT_DIR)'
endif

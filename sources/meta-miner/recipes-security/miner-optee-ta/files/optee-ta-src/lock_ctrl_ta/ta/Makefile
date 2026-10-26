CFG_TEE_TA_LOG_LEVEL ?= 2

# The UUID for the Trusted Application
BINARY=a75709fa-f0cc-41c6-98ec-b39b1f993116
O=$(OUTPUT_DIR)/ta

LDADD += -L$(COMMON_LIB_DIR) -lminer_optee_ta_common
CFLAGS += $(COMMON_INC) -Wall -Wextra -Werror

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

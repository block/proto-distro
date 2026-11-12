CFG_TEE_TA_LOG_LEVEL ?= 2

include ../../common_ta_uuid.mk

# The UUID for the Trusted Application
BINARY=$(DEVICE_IDENTITY_TA_UUID)
O=$(OUTPUT_DIR)/ta

LDADD += -L$(COMMON_LIB_DIR) -lminer_optee_ta_common
CFLAGS += $(COMMON_INC) -Wall -Wextra -Werror

all:
	@test -e $(O) || mkdir -p $(O)

-include $(TA_DEV_KIT_DIR)/mk/ta_dev_kit.mk

ifeq ($(wildcard $(TA_DEV_KIT_DIR)/mk/ta_dev_kit.mk), )
clean:
	@echo 'Note: $$(TA_DEV_KIT_DIR)/mk/ta_dev_kit.mk not found, cannot clean TA'
	@echo 'Note: TA_DEV_KIT_DIR=$(TA_DEV_KIT_DIR)'
endif

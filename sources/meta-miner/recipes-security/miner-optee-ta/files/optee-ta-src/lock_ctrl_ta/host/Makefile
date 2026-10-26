CC      ?= $(CROSS_COMPILE)gcc
LD      ?= $(CROSS_COMPILE)ld
AR      ?= $(CROSS_COMPILE)ar
NM      ?= $(CROSS_COMPILE)nm
OBJCOPY ?= $(CROSS_COMPILE)objcopy
OBJDUMP ?= $(CROSS_COMPILE)objdump
READELF ?= $(CROSS_COMPILE)readelf

OUTDIR = $(OUTPUT_DIR)/host

OBJS = $(OUTDIR)/main.o

CFLAGS += -Wall -Werror -Wextra -I../ta/include -I$(TEEC_EXPORT)/include -I./include $(COMMON_INC)
#Add/link other required libraries here
LDADD += -L$(COMMON_LIB_DIR) -lminer_optee_host_common -L$(TEEC_EXPORT)/lib -lteec

BINARY = $(OUTDIR)/lock_ctrl_ta_client

.PHONY: all
all: $(OUTDIR) $(BINARY)

$(OUTDIR):
	@test -e $(OUTDIR) || mkdir -p $(OUTDIR)

$(BINARY): $(OBJS)
	$(CC) $(LDFLAGS) -o $@ $< $(LDADD)

$(OUTDIR)/%.o: %.c | $(OUTDIR)
	$(CC) $(CFLAGS) -c $< -o $@

.PHONY: clean
clean:
	rm -f $(OBJS) $(BINARY)

CC      ?= $(CROSS_COMPILE)gcc
LD      ?= $(CROSS_COMPILE)ld
AR      ?= $(CROSS_COMPILE)ar
NM      ?= $(CROSS_COMPILE)nm
OBJCOPY ?= $(CROSS_COMPILE)objcopy
OBJDUMP ?= $(CROSS_COMPILE)objdump
READELF ?= $(CROSS_COMPILE)readelf

OUTDIR = $(OUTPUT_DIR)/host

OBJS = $(OUTDIR)/common.o $(OUTDIR)/rpmb.o

CFLAGS += -Wall -Werror -Wextra -I../ta/include -I$(TEEC_EXPORT)/include -I./include

BINARY = $(OUTDIR)/libminer_optee_host_common.a

.PHONY: all
all: $(OUTDIR) $(BINARY)

$(OUTDIR):
	@test -e $(OUTDIR) || mkdir -p $(OUTDIR)

$(BINARY): $(OBJS)
	$(AR) rcs -o $@ $(OBJS)

$(OUTDIR)/%.o: %.c | $(OUTDIR)
	$(CC) $(CFLAGS) -c $< -o $@

.PHONY: clean
clean:
	rm -f $(OBJS) $(BINARY)

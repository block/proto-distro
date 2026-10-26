# Define the module's object files
obj-m := board_id.o

SRC := $(shell pwd)

all:
	$(MAKE) -C $(KERNEL_SRC) M=$(SRC) modules

modules_install:
	$(MAKE) -C $(KERNEL_SRC) M=$(SRC) modules_install

clean:
	rm -f *.o *~ core .depend .*.cmd *.ko *.mod.C
	rm -f Module.markers Module.symvers modules.order
	rm -rf .tmp_versions Modules.symvers

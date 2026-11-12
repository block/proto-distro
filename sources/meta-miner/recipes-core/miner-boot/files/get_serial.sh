#!/bin/sh

serial_hex=$(dd if=/sys/bus/nvmem/devices/stm32-romem0/nvmem bs=4 skip=59 count=4 2>/dev/null | hexdump -ve '1/1 "%.2x"')
serial_ascii=$(echo -n $serial_hex | awk '{for(i=1;i<=length;i+=2)printf "%c", strtonum("0x"substr($0,i,2))}')

echo $serial_ascii

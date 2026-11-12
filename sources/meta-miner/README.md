# meta-miner
Embedded linux Yocto project layer for miner packages and images.

This README file contains information on the contents of the miner layer.

Core Recipes
============

busybox
- Appended to enable `ntpd` for system time synchronization.

Application daemons
- Application packages own their daemons. The distro injector accepts application IPKs; selected packages depend on injection configuration.
- See [application/platform contracts](../../doc/application-contracts.md) for lifecycle, manufacturing, OTA confirmation, persistence, and packaging boundaries.

Development-kit SD-card layouts
==============================
- Retained WIC templates describe STM32 development-kit SD-card partitions.
- `conf/machine/stm32mp135f-dk.conf` selects `sdcard-stm32mp1-256MB-optee.wks.in` by default, or `sdcard-stm32mp135f-dk-optee-vendorfs-1GB.wks.in` with vendorfs enabled.
- `conf/machine/stm32mp157d-dk1.conf` and `conf/machine/stm32mp157f-dk2.conf` select `sdcard-stm32mp15x-trusted.wks.in`.
- These layouts are separate from the C2/C3 eMMC/flasher flow, whose partition layout lives in [`tools/flashimage/layout.go`](../../tools/flashimage/layout.go).

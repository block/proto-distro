# PSU Firmware Files

Place PSU firmware binary files in this directory. Supported formats:
- `*.bin` - Binary firmware files
- `*.fw` - Firmware files
- `*.img` - Image files

These files will be installed to `/etc/psu-firmware/` on the target system.

## Supported PSU Types

Based on the psuctl implementation, the following PSU firmware formats are supported:
- Chicony M-CRPS format
- Boco Package format (multi-MCU)

The `psuctl` tool will automatically detect PSU types and match them with compatible firmware files during the update process.

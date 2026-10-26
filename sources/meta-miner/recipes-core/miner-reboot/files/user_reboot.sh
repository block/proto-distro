#!/bin/bash

# Function to show usage information
show_usage() {
  echo "Usage: $0 [OPTION]"
  echo ""
  echo "Options:"
  echo "  --fastboot [IP] [NETMASK] [GATEWAY]   Reboot into fastboot mode with optional network config"
  echo "  --st-dfu                              Reboot into ST DFU mode (for use with ST_Programmer_CLI)"
  echo "  --factory-reset [--yes]               Factory reset the device (requires confirmation unless --yes)"
  echo "  --clear-user-settings [--yes]         Clear user settings (requires confirmation unless --yes)"
  echo "  --help                                Show this help message"
  echo ""
  echo "Note: This script requires root privileges"
}

# Check if running as root
if [ "$(id -u)" -ne 0 ]; then
  echo "reboot: Need to be root"
  exit 1
fi

# Parse command line arguments (only if arguments were provided)
if [ $# -gt 0 ]; then
  case "$1" in
    --fastboot)
      echo "Setting up fastboot mode..."
      
      # Validate IP address if provided
      if [ -n "$2" ]; then
        fw_setenv ipaddr "$2"
        echo "IP address set to: $2"
      fi
      
      # Validate netmask if provided
      if [ -n "$3" ]; then
        fw_setenv netmask "$3"
        echo "Netmask set to: $3"
      fi
      
      # Validate gateway if provided
      if [ -n "$4" ]; then
        fw_setenv gatewayip "$4"
        echo "Gateway set to: $4"
      fi
      
      fw_setenv reboot_fastboot 1
      echo "Device will reboot into fastboot mode"
    ;;
    
    --st-dfu)
      echo "Setting up ST DFU mode..."
      fw_setenv reboot_dfu 2
      echo "Device will reboot into ST DFU mode"
    ;;
    
    --factory-reset)
      echo "Setting up factory reset..."
      
      # Check for yes flag to bypass confirmation
      if [ "$2" = "--yes" ]; then
        echo "Yes flag detected - proceeding without confirmation."
      else
        read -p "Are you sure you want to factory reset? This will erase all data. (y/N): " confirm
        
        # Convert input to lowercase
        confirm=${confirm,,}
        
        if [[ "$confirm" != "y" ]]; then
          echo "Factory reset cancelled."
          exit 1
        fi
        
        echo "Factory reset confirmed."
      fi
      
      fw_setenv factory_reset 'y'
      echo "Device will factory reset on next boot"
    ;;
    
    --clear-user-settings)
      echo "Setting up user settings clear..."
      
      # Check for yes flag to bypass confirmation
      if [ "$2" = "--yes" ]; then
        echo "Yes flag detected - proceeding without confirmation."
      else
        read -p "Are you sure you want to clear all user settings? This cannot be undone. (y/N): " confirm
        
        # Convert input to lowercase
        confirm=${confirm,,}
        
        if [[ "$confirm" != "y" ]]; then
          echo "User settings clear cancelled."
          exit 1
        fi
        
        echo "User settings clear confirmed."
      fi
      
      echo "Clearing user settings..."
      
      # Try to unmount data partition if mounted
      if mountpoint -q /data 2>/dev/null; then
        umount /data
      fi
      
      # Clear the data partition by writing zeros to the beginning
      if [ -b /dev/mmcblk0p10 ]; then
        dd if=/dev/zero of=/dev/mmcblk0p10 bs=1M count=10
        echo "User settings cleared, rebooting..."
      else
        echo "Error: Data partition not found. Cannot clear user settings."
        exit 1
      fi
    ;;
    
    --help|-h)
      show_usage
      exit 0
    ;;
    
    *)
      echo "Error: Unknown argument '$1'"
      show_usage
      exit 1
    ;;
  esac
fi

# See: https://docs.memfault.com/docs/reference/reference-reboot-reason-ids
if [ -f "/tmp/swupdate/ready_to_reboot" ]; then
  echo "Rebooting with reason code FW Update (kMfltRebootReason_FirmwareUpdate)"
  echo "3" >/etc/memfault/last_reboot_reason
else
  echo "Rebooting with reason code User Reset (kMfltRebootReason_UserReset)"
  echo "2" >/etc/memfault/last_reboot_reason
fi

echo "Rebooting the device..."
systemctl reboot

#!/bin/sh

# Write memfault attributes from os-release
if [ -f /etc/os-release ]; then
  source /etc/os-release
  echo "Writing distro git hash: $GIT_HASH"
  echo "Writing distro build datetime: $BUILD_DATETIME_UTC"
  memfaultctl write-attributes sysprop.distro_git_hash=$GIT_HASH
  memfaultctl write-attributes sysprop.distro_build_datetime=$BUILD_DATETIME_UTC
fi

# Write mac address
MAC_ADDRESS=$(ip link show eth0 | grep -oE '([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}' | awk 'NR==1')
echo "Writing mac address: $MAC_ADDRESS"
memfaultctl write-attributes mac_address=$MAC_ADDRESS

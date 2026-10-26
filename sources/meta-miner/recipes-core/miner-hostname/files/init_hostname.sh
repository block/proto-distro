### Set hostname ###
LAST4_MAC=$(sed -rn "s/^.*([0-9A-F:]{5})$/\1/gi;s/://p" /sys/class/net/eth0/address)
LAST4_MAC_UPPER=$(echo $LAST4_MAC | tr '[a-z]' '[A-Z]')
HOSTNAME=proto-miner-${LAST4_MAC_UPPER:-0000}

echo "Firstboot: Setting hostname to $HOSTNAME..."
echo $HOSTNAME > /etc/hostname
/bin/hostname -F /etc/hostname

### Disable firstboot service after running ###
systemctl disable miner-hostname.service
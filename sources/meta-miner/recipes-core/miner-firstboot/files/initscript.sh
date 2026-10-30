### Generate unique SSL certificate ###
SSL_DIR="/etc/nginx/ssl"
if [ ! -f "${SSL_DIR}/nginx.key" ]; then
  echo "Generating unique SSL certificate..."

  # Ensure SSL directory exists with proper permissions
  mkdir -p "${SSL_DIR}"
  chmod 700 "${SSL_DIR}"

  # Get unique device information
  SERIAL=$(cat /proc/cpuinfo | grep Serial | cut -d ' ' -f 2 || hostname)
  MACADDR=$(cat /sys/class/net/eth0/address 2>/dev/null || echo "unknown")

  # Generate unique certificate
  openssl req -x509 -nodes -days 3650 -newkey rsa:2048 \
    -keyout "${SSL_DIR}/nginx.key" \
    -out "${SSL_DIR}/nginx.crt" \
    -subj "/C=US/ST=State/O=Organization/CN=${SERIAL}"

  # Set proper permissions
  chmod 400 "${SSL_DIR}/nginx.key"
  chmod 444 "${SSL_DIR}/nginx.crt"
  chown -R nginx:nginx "${SSL_DIR}"

  # Full restart of nginx to ensure it uses the new certificate
  systemctl restart nginx.service
fi

### Resize datafs regardless of rootfs dm-verity state ###
echo "Firstboot: Resizing data partition to end of sdmmc..."
DATA_PART_DEV=$(findmnt /data -o source -n)
DATA_PART_NAME=$(echo "$DATA_PART_DEV" | cut -d "/" -f 3)
DATA_DEV_NAME=$(echo /sys/block/*/"${DATA_PART_NAME}" | cut -d "/" -f 4)
DATA_DEV="/dev/${DATA_DEV_NAME}"
DATA_PART_NUM=$(cat "/sys/block/${DATA_DEV_NAME}/${DATA_PART_NAME}/partition")

printf "w\n" | fdisk "$DATA_DEV"
growpart "$DATA_DEV" "$DATA_PART_NUM"
resize2fs "$DATA_PART_DEV"

# Keep platform preparation best-effort. The unit disables itself after application hooks.
exit 0

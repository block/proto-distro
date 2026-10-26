OS_RELEASE=/etc/os-release

# Function to check if SSH key is valid
check_ssh_key() {
  local key_file="$1"
  if [ ! -f "$key_file" ]; then
    return 1
  fi

  # Try to get key fingerprint - this will fail if key is corrupted or invalid
  if ssh-keygen -l -f "$key_file" >/dev/null 2>&1; then
    return 0
  else
    return 1
  fi
}

# Overwrite os-release with the one from /usr/lib if it is a symlink or empty
if [ -L "$OS_RELEASE" ] || [ ! -e "$OS_RELEASE" ]; then
  cp /usr/lib/os-release "$OS_RELEASE"
fi

# Overwrite os-release file if version or variant changed (e.g. after OTA).
# The overlay's copy can lag the rootfs after an OTA - keep it in sync so
# downstream VARIANT/VERSION checks see the current rootfs's values.
if [ -f /usr/lib/os-release ] && [ -f "$OS_RELEASE" ]; then
  usr_version=$(grep '^VERSION=' /usr/lib/os-release | cut -d'"' -f2)
  current_version=$(grep '^VERSION=' "$OS_RELEASE" | cut -d'"' -f2)
  usr_variant=$(grep '^VARIANT=' /usr/lib/os-release | cut -d'"' -f2)
  current_variant=$(grep '^VARIANT=' "$OS_RELEASE" | cut -d'"' -f2)
  if [ "$usr_version" != "$current_version" ] || [ "$usr_variant" != "$current_variant" ]; then
    cp /usr/lib/os-release "$OS_RELEASE"
  fi
fi

### Finalize the current rootfs on every boot, including OTA and flashing ###
"$(dirname "$0")/finalize-rootfs.sh" ||
  echo "Warning: rootfs finalization failed (status $?); continuing boot setup."

### Map log journal to /data/journal for mfg build only
if grep -q 'VARIANT="mfg"' $OS_RELEASE; then
  mkdir -p /data/journal
  ln -s /data/journal /var/log/journal
  # Restart systemd-journal-flush service to make sure it write log to data part
  systemctl restart systemd-journal-flush.service
fi

### Read out mfg serial number to /etc/machine-info if not already set ###
MACHINE_INFO=/etc/machine-info
serial_ascii=$(./usr/bin/get_serial.sh)
if [ -n "$serial_ascii" ] && ! grep -q 'MFG_SERIAL_NUMBER' "$MACHINE_INFO" 2>/dev/null; then
  echo "MFG_SERIAL_NUMBER=\"$serial_ascii\"" >> "$MACHINE_INFO"
fi

### Start these services here, since they are disabled by default ###
systemctl start systemd-resolved
systemctl start systemd-timesyncd

### Validate and regenerate SSH host keys if needed ###
echo "Checking SSH host key validity..."
SSH_KEY_FILE="/etc/ssh/ssh_host_ed25519_key"
SSH_PUB_KEY_FILE="/etc/ssh/ssh_host_ed25519_key.pub"

# Check if SSH key is valid, regenerate if not
if ! check_ssh_key "$SSH_KEY_FILE"; then
  echo "SSH host key is missing or invalid. Regenerating..."

  # Remove any existing corrupted keys
  rm -f "$SSH_KEY_FILE" "$SSH_PUB_KEY_FILE"

  # Generate new SSH host key
  ssh-keygen -t ed25519 -f "$SSH_KEY_FILE" -N "" -C "root@$(hostname)"

  # Set proper permissions
  chmod 600 "$SSH_KEY_FILE"
  chmod 644 "$SSH_PUB_KEY_FILE"

  # Verify the new key is valid
  if check_ssh_key "$SSH_KEY_FILE"; then
    echo "SSH host key successfully regenerated."
  else
    echo "Warning: Failed to regenerate valid SSH host key."
  fi
else
  echo "SSH host key is valid."
fi

# Platform preparation remains best-effort; security selection runs in its own service.
exit 0

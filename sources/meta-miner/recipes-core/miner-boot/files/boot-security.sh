#!/bin/sh

### Ensure sshd is enabled/disabled on secure/non-secure devices ###
IS_SYSTEM_SECURE=/usr/bin/is_system_secure
if [ ! -x "$IS_SYSTEM_SECURE" ]; then
  echo "Warning: $IS_SYSTEM_SECURE command not found or not executable. Defaulting to secure mode."
  system_secure=0
elif "$IS_SYSTEM_SECURE"; then
  system_secure=0
else
  system_secure=1
fi

if [ "$system_secure" -eq 0 ]; then
  echo "System is secure. Disabling SSH and enabling secure NATS server..."
  systemctl disable --now sshd.service

  systemctl disable --now nats-server@open.service
  systemctl enable --now nats-server@secure.service
else
  echo "System is not secure. Enabling SSH and starting open NATS server..."
  systemctl enable --now sshd.service

  systemctl enable --now nats-server@open.service
  systemctl disable --now nats-server@secure.service
fi

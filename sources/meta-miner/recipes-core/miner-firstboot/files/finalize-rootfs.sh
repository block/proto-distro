#!/bin/sh
# Called by boot.sh on every boot.

### Skip rootfs finalization on dm-verity or an unknown root device ###
# Use the mounted device number, not its name (/dev/root, /dev/dm-0, etc.).
FINALIZE_ROOTFS=0
if ROOT_DEVNO=$(findmnt / -r -n -o MAJ:MIN) &&
   [ -n "$ROOT_DEVNO" ] && [ -e "/sys/dev/block/$ROOT_DEVNO" ]; then
  if [ ! -d "/sys/dev/block/$ROOT_DEVNO/dm" ]; then
    FINALIZE_ROOTFS=1
  elif VERITY_TABLE=$(dmsetup table --target verity \
      -j "${ROOT_DEVNO%:*}" -m "${ROOT_DEVNO#*:}"); then
    [ -n "$VERITY_TABLE" ] || FINALIZE_ROOTFS=1
  fi
fi
if [ "$FINALIZE_ROOTFS" != "1" ]; then
  echo "Rootfs finalization: dm-verity rootfs or unknown root device; skipping rootfs modifications."
  exit 0
fi

### Check whether the filesystem can grow before making rootfs writable ###
ROOT_PART_DEV=$(findmnt / -r -o source -n)
if [ -z "$ROOT_PART_DEV" ] ||
   ! ROOT_PART_SIZE=$(blockdev --getsize64 "$ROOT_PART_DEV") ||
   ! ROOT_FS_INFO=$(LC_ALL=C dumpe2fs -h "$ROOT_PART_DEV"); then
  echo "Warning: Could not determine rootfs or partition size. Skipping rootfs resize."
  exit 1
fi
ROOT_BLOCK_COUNT=
ROOT_BLOCK_SIZE=
while read -r FIELD NAME VALUE REST; do
  case "$FIELD $NAME" in
    "Block count:") ROOT_BLOCK_COUNT=$VALUE ;;
    "Block size:") ROOT_BLOCK_SIZE=$VALUE ;;
  esac
done <<EOF
$ROOT_FS_INFO
EOF
case "$ROOT_PART_SIZE:$ROOT_BLOCK_COUNT:$ROOT_BLOCK_SIZE" in
  *[!0-9:]*|:*|*::*|*:)
    echo "Warning: Invalid rootfs or partition size. Skipping rootfs resize."
    exit 1 ;;
esac
if [ "$ROOT_BLOCK_SIZE" -eq 0 ] || [ "$ROOT_BLOCK_COUNT" -eq 0 ]; then
  echo "Warning: Invalid ext4 block size or count. Skipping rootfs resize."
  exit 1
fi
# Ignore trailing bytes that cannot hold a complete filesystem block.
if [ "$ROOT_BLOCK_COUNT" -ge "$((ROOT_PART_SIZE / ROOT_BLOCK_SIZE))" ]; then
  exit 0
fi

### Make rootfs writable for finalization ###
# Only release images need a remount; restore read-only only if we changed it.
ROOT_REMOUNTED_RW=0
if grep -q 'VARIANT="release"' /etc/os-release; then
  echo "Rootfs finalization: remounting / read-write..."
  if mount -o remount,rw /; then
    ROOT_REMOUNTED_RW=1
  else
    echo "Warning: failed to remount / read-write; skipping rw-only steps."
    exit 1
  fi
fi

### Make initscript immutable to prevent tampering ###
chattr +i "$(dirname "$0")/initscript.sh"

### Resize rootfs to fill its existing partition ###
RESIZE_STATUS=0
echo "Rootfs finalization: Resizing root partition to fill partition..."
resize2fs "$ROOT_PART_DEV" || RESIZE_STATUS=$?

### Restore read-only rootfs ###
if [ "$ROOT_REMOUNTED_RW" = "1" ]; then
  echo "Rootfs finalization: remounting / read-only..."
  sync
  mount -o remount,ro / || echo "Warning: failed to remount / read-only."
fi

exit "$RESIZE_STATUS"

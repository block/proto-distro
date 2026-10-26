echo "Bootcount: ${bootcount}!"

# Check if we should go into fastboot mode
if test "${reboot_fastboot}" = 1; then
    # Reset the reboot variable so we don't enter fastboot mode again
    env delete reboot_fastboot
    saveenv
    bootcount reset

    echo "Entering fastboot mode..."
    if test -z "${ipaddr}"; then
        echo "No IP address configured, using dhcp"
        dhcp
    fi
    fastboot udp
    reset
fi

if test -n "${ipaddr}"; then
  # Ensure ipaddr, netmask, and gateway are unset after fastboot
  env delete ipaddr
  env delete netmask
  env delete gateway
  saveenv
fi

# Explicit ST programming must not replace automatic UDP recovery.
if test "${reboot_dfu}" = 2; then
  env delete reboot_dfu
  saveenv
  bootcount reset
  echo "Entering ST dfu mode..."
  stm32prog usb 0
  reset
fi

if test "${ustate}" = 1; then
    echo "OTA update in progress, ustate: ${ustate}, boot count: ${bootcount}"
    if test "${bootcount}" = 3; then
        echo "Boot count reached 3, setting ustate to 3 (failed)"
        setenv ustate 3
        saveenv
        reset
    fi
fi

if test "${ustate}" = 3; then
    echo "OTA update failed, initiating fallback..."
    if test "${rootpart}" = "${a_partnum}"; then
        echo "Fallback to partition B"
        setenv rootpart ${b_partnum}
    else
        echo "Fallback to partition A"
        setenv rootpart ${a_partnum}
    fi
    saveenv
fi

if test "${factory_reset}" = "y"; then
    env delete factory_reset
    saveenv

    # Check that recovery_part_blk_addr exists as indication if env variables
    # are updated, if not don't try to copy recovery image

    if env exists recovery_part_blk_addr; then
        echo "Starting factory reset!"
    else
        # Likely due to old env variables, should run $ env erase and reset
        echo "recovery_part_blk_addr not found, skipping factory reset"
        reset
    fi

    # Just copy the recovery image into part a
    setexpr target_partnum ${a_partnum}
    setexpr target_addr ${a_part_blk_addr}

    # Prepare for the copy
    setexpr i 0
    setexpr n ${rootfs_part_size} / ${recovery_part_copy_blk_cnt}
    setexpr src_addr ${recovery_part_blk_addr}

    # Just in case devnum is not set
    mmc dev ${devnum}
    while itest $i < $n; do
      mmc read ${kernel_addr_r} ${src_addr} ${recovery_part_copy_blk_cnt}
      mmc write ${kernel_addr_r} ${target_addr} ${recovery_part_copy_blk_cnt}

      setexpr src_addr ${src_addr} + ${recovery_part_copy_blk_cnt}
      setexpr target_addr ${target_addr} + ${recovery_part_copy_blk_cnt}
      setexpr i ${i} + 1
    done

    # Delete part b and data part
    mmc erase ${b_part_blk_addr} ${rootfs_part_size}
    # Minimal mmc erase block size, just enough to trash the partition
    mmc erase ${data_part_blk_addr} 0x400

    # Set the rootpart to newly copied partition and boot into it
    setenv rootpart ${target_partnum}
    saveenv
fi

echo "Booting from mmc ${devnum}:${rootpart}..."

# Select the same A/B slot for the kernel and root filesystem in either mode.
if test "${rootpart}" = "${a_partnum}"; then
    setenv avb_slot _a
elif test "${rootpart}" = "${b_partnum}"; then
    setenv avb_slot _b
else
    echo "Invalid rootfs slot: ${rootpart}"
    reset
    exit
fi

# Never reuse arguments from a previous slot or an earlier verification.
setenv avb_bootargs
setenv verity_root
if test "${verify}" = "n"; then
    # Explicitly unlocked devices can use older bootchains without AVB.
    setenv rootpart_partuuid
    if part uuid mmc ${devnum}:${rootpart} rootpart_partuuid && test -n "${rootpart_partuuid}"; then
        echo "verify=n, booting writable rootfs without dm-verity"
        setenv bootargs "root=PARTUUID=${rootpart_partuuid} rw"
    else
        echo "Failed to resolve rootfs${avb_slot} PARTUUID"
        reset
        exit
    fi
else
    # Only verify=n opts out; missing or unexpected values must fail closed.
    if avb init ${devnum} && avb verify ${avb_slot}; then
        # The signer pins the vroot dm-init mapping to minor 0, hence /dev/dm-0.
        for arg in ${avb_bootargs}; do
            if test "${arg}" = "root=/dev/dm-0"; then
                setenv verity_root y
            fi
        done
        if test "${verity_root}" != "y"; then
            echo "AVB metadata does not enable the dm-verity root"
            reset
            exit
        fi
    else
        echo "AVB verification failed for rootfs${avb_slot}"
        reset
        exit
    fi
    # Drop libavb reporting fields, not the signed dm-verity table or verification.
    if setexpr bootargs gsub " androidboot[.][^ ]*" "" "${avb_bootargs}"; then
        setenv bootargs "${bootargs} console=ttynull ro"
    else
        echo "Failed to prepare verified boot arguments"
        reset
        exit
    fi
fi

setenv bootargs "${bootargs} rootwait quiet"
echo "Bootargs: ${bootargs}"

# Load the kernel and device tree from the verified rootfs slot.
if load mmc ${devnum}:${rootpart} ${kernel_addr_r} /boot/fitImage; then
    bootm ${kernel_addr_r}${kernel_boot_conf}
fi

# A failed load or boot must not fall through to another unverified boot path.
echo "Failed to boot rootfs${avb_slot}"
reset

#!/usr/bin/env python3

"""
Minimal script for replacing AVB public keys in U-Boot binary images.

This script searches for an AVB public key (LEN_AVBPUBKEY bytes) in a U-Boot binary
and replaces it with a different AVB public key. It fails if the old key is missing
or occurs more than once. If the old and new keys are identical, it copies the file
unchanged (acceptable for development/testing).
"""

import logging
import sys

LEN_AVBPUBKEY = 520  # AVB public key size in bytes

logger = logging.getLogger(__name__)


def replace_avbpubkey(old_uboot_bin: str, new_uboot_bin: str, old_avbpubkey: str, new_avbpubkey: str) -> None:
    """
    Replace AVB public key in U-Boot binary.

    Args:
        old_uboot_bin: Path to input U-Boot binary with old key
        new_uboot_bin: Path to output U-Boot binary with new key
        old_avbpubkey: Path to old AVB public key file (key to search for)
        new_avbpubkey: Path to new AVB public key file (key to replace with)

    Notes:
        If old and new keys are identical, the function prints a warning and
        copies the input file to output without modification. This is acceptable
        for development/testing purposes.

    Raises:
        ValueError: If key sizes are incorrect, paths are the same, keys are not found,
                   or verification fails
    """
    if old_uboot_bin == new_uboot_bin:
        raise ValueError("Input and output U-Boot binary paths must be different")

    with open(old_avbpubkey, "rb") as f:
        old_key = f.read()
    with open(new_avbpubkey, "rb") as f:
        new_key = f.read()

    if len(old_key) != LEN_AVBPUBKEY:
        raise ValueError(f"Old key length {len(old_key)} != {LEN_AVBPUBKEY}")
    if len(new_key) != LEN_AVBPUBKEY:
        raise ValueError(f"New key length {len(new_key)} != {LEN_AVBPUBKEY}")

    with open(old_uboot_bin, "rb") as f:
        data = f.read()
    occurrences = data.count(old_key)

    if occurrences == 0:
        raise ValueError("Old key not found in U-Boot binary")
    elif occurrences > 1:
        raise ValueError(f"Old key found {occurrences} times, expected exactly one")

    if old_key == new_key:
        logger.warning("Old key and new key are identical")
        logger.warning("  This is acceptable for development/testing but should not be used in production")
        logger.warning("  Skipping replacement as keys are the same")
        # Copy the input file to output without modification
        with open(new_uboot_bin, "wb") as f:
            f.write(data)
        logger.info(f"Copied U-Boot binary (no replacement needed): {old_uboot_bin} → {new_uboot_bin}")
        return

    replaced = data.replace(old_key, new_key)
    with open(new_uboot_bin, "wb") as f:
        f.write(replaced)

    # Verify the replacement was successful
    with open(new_uboot_bin, "rb") as f:
        verify_data = f.read()
    new_occurrences = verify_data.count(new_key)
    old_occurrences = verify_data.count(old_key)

    if new_occurrences != 1:
        raise ValueError(f"Verification failed - new key found {new_occurrences} times, expected exactly one")
    if old_occurrences != 0:
        raise ValueError(f"Verification failed - old key still found {old_occurrences} times in output")

    logger.info(f"Successfully replaced AVB key: {old_uboot_bin} → {new_uboot_bin}")


def main() -> None:
    """CLI entry point."""
    # Configure logging for CLI usage
    logging.basicConfig(
        level=logging.INFO,
        format="%(levelname)s: %(message)s",
    )

    if len(sys.argv) != 5:
        logger.error("Usage: replace_uboot_avbkey.py <old_uboot.bin> <new_uboot.bin> <old.avbpubkey> <new.avbpubkey>")
        sys.exit(1)

    replace_avbpubkey(*sys.argv[1:])


if __name__ == "__main__":
    main()

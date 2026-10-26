"""Replace U-Boot AVB Key package

This package provides tools for replacing AVB public keys in U-Boot binary images.
"""

from .replace_uboot_avbkey import LEN_AVBPUBKEY, replace_avbpubkey

__all__ = ["LEN_AVBPUBKEY", "replace_avbpubkey"]

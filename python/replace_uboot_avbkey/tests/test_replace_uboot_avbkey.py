#!/usr/bin/env python3

"""
Unit tests for replace_uboot_avbkey.py script.
"""

import pytest

from replace_uboot_avbkey import LEN_AVBPUBKEY, replace_avbpubkey


class TestReplaceAvbpubkey:
    """Tests for the AVB public key replacement function."""

    def test_successful_replacement(self, tmp_path):
        """Test successful replacement when key exists exactly once."""
        # Create LEN_AVBPUBKEY-byte keys
        old_key = bytes([0xDE, 0xAD, 0xBE, 0xEF] * (LEN_AVBPUBKEY // 4))
        new_key = bytes([0xCA, 0xFE, 0xBA, 0xBE] * (LEN_AVBPUBKEY // 4))

        # Create U-Boot binary with the old key embedded
        prefix = bytes([0x00] * 100)
        suffix = bytes([0xFF] * 100)
        old_uboot_data = prefix + old_key + suffix

        # Write test files
        old_uboot_bin = tmp_path / "old_uboot.bin"
        new_uboot_bin = tmp_path / "new_uboot.bin"
        old_avbpubkey = tmp_path / "old.avbpubkey"
        new_avbpubkey = tmp_path / "new.avbpubkey"

        old_uboot_bin.write_bytes(old_uboot_data)
        old_avbpubkey.write_bytes(old_key)
        new_avbpubkey.write_bytes(new_key)

        # Perform replacement
        replace_avbpubkey(str(old_uboot_bin), str(new_uboot_bin), str(old_avbpubkey), str(new_avbpubkey))

        # Verify the output
        new_uboot_data = new_uboot_bin.read_bytes()

        assert new_key in new_uboot_data
        assert old_key not in new_uboot_data
        assert new_uboot_data[:100] == prefix  # Prefix unchanged
        assert new_uboot_data[100 + LEN_AVBPUBKEY :] == suffix  # Suffix unchanged

    def test_old_key_wrong_size(self, tmp_path):
        """Test error when old key is not LEN_AVBPUBKEY bytes."""
        old_key = bytes([0xDE, 0xAD] * 100)  # 200 bytes, not LEN_AVBPUBKEY
        new_key = bytes([0xCA, 0xFE] * (LEN_AVBPUBKEY // 2))

        old_uboot_bin = tmp_path / "old_uboot.bin"
        new_uboot_bin = tmp_path / "new_uboot.bin"
        old_avbpubkey = tmp_path / "old.avbpubkey"
        new_avbpubkey = tmp_path / "new.avbpubkey"

        old_uboot_bin.write_bytes(bytes([0x00] * 1000))
        old_avbpubkey.write_bytes(old_key)
        new_avbpubkey.write_bytes(new_key)

        with pytest.raises(ValueError, match=f"Old key length 200 != {LEN_AVBPUBKEY}"):
            replace_avbpubkey(str(old_uboot_bin), str(new_uboot_bin), str(old_avbpubkey), str(new_avbpubkey))

    def test_new_key_wrong_size(self, tmp_path):
        """Test error when new key is not LEN_AVBPUBKEY bytes."""
        old_key = bytes([0xDE, 0xAD] * (LEN_AVBPUBKEY // 2))
        new_key = bytes([0xCA, 0xFE] * 100)  # 200 bytes, not LEN_AVBPUBKEY

        old_uboot_bin = tmp_path / "old_uboot.bin"
        new_uboot_bin = tmp_path / "new_uboot.bin"
        old_avbpubkey = tmp_path / "old.avbpubkey"
        new_avbpubkey = tmp_path / "new.avbpubkey"

        old_uboot_bin.write_bytes(bytes([0x00] * 1000))
        old_avbpubkey.write_bytes(old_key)
        new_avbpubkey.write_bytes(new_key)

        with pytest.raises(ValueError, match=f"New key length 200 != {LEN_AVBPUBKEY}"):
            replace_avbpubkey(str(old_uboot_bin), str(new_uboot_bin), str(old_avbpubkey), str(new_avbpubkey))

    def test_key_not_found(self, tmp_path):
        """Test error when old key is not found in U-Boot binary."""
        old_key = bytes([0xDE, 0xAD] * (LEN_AVBPUBKEY // 2))
        new_key = bytes([0xCA, 0xFE] * (LEN_AVBPUBKEY // 2))

        # U-Boot binary doesn't contain the old key
        old_uboot_data = bytes([0x11, 0x22] * 500)

        old_uboot_bin = tmp_path / "old_uboot.bin"
        new_uboot_bin = tmp_path / "new_uboot.bin"
        old_avbpubkey = tmp_path / "old.avbpubkey"
        new_avbpubkey = tmp_path / "new.avbpubkey"

        old_uboot_bin.write_bytes(old_uboot_data)
        old_avbpubkey.write_bytes(old_key)
        new_avbpubkey.write_bytes(new_key)

        with pytest.raises(ValueError, match="Old key not found in U-Boot binary"):
            replace_avbpubkey(str(old_uboot_bin), str(new_uboot_bin), str(old_avbpubkey), str(new_avbpubkey))

    def test_key_found_multiple_times(self, tmp_path):
        """Test error when old key is found multiple times."""
        old_key = bytes([0xDE, 0xAD] * (LEN_AVBPUBKEY // 2))
        new_key = bytes([0xCA, 0xFE] * (LEN_AVBPUBKEY // 2))

        # U-Boot binary contains the old key twice
        old_uboot_data = old_key + bytes([0x00] * 100) + old_key

        old_uboot_bin = tmp_path / "old_uboot.bin"
        new_uboot_bin = tmp_path / "new_uboot.bin"
        old_avbpubkey = tmp_path / "old.avbpubkey"
        new_avbpubkey = tmp_path / "new.avbpubkey"

        old_uboot_bin.write_bytes(old_uboot_data)
        old_avbpubkey.write_bytes(old_key)
        new_avbpubkey.write_bytes(new_key)

        with pytest.raises(ValueError, match="Old key found 2 times, expected exactly one"):
            replace_avbpubkey(str(old_uboot_bin), str(new_uboot_bin), str(old_avbpubkey), str(new_avbpubkey))

    def test_replacement_preserves_surrounding_data(self, tmp_path):
        """Test that replacement only modifies the key, not surrounding data."""
        old_key = bytes([0xAA] * LEN_AVBPUBKEY)
        new_key = bytes([0xBB] * LEN_AVBPUBKEY)

        # Create U-Boot binary with data before and after the key
        prefix = bytes([0xFF] * 1000)
        suffix = bytes([0xEE] * 1000)
        old_uboot_data = prefix + old_key + suffix

        old_uboot_bin = tmp_path / "old_uboot.bin"
        new_uboot_bin = tmp_path / "new_uboot.bin"
        old_avbpubkey = tmp_path / "old.avbpubkey"
        new_avbpubkey = tmp_path / "new.avbpubkey"

        old_uboot_bin.write_bytes(old_uboot_data)
        old_avbpubkey.write_bytes(old_key)
        new_avbpubkey.write_bytes(new_key)

        # Perform replacement
        replace_avbpubkey(str(old_uboot_bin), str(new_uboot_bin), str(old_avbpubkey), str(new_avbpubkey))

        # Verify the output
        new_uboot_data = new_uboot_bin.read_bytes()

        # Verify prefix and suffix are unchanged
        assert new_uboot_data[:1000] == prefix
        assert new_uboot_data[1000 + LEN_AVBPUBKEY :] == suffix

        # Verify key was replaced
        assert new_uboot_data[1000 : 1000 + LEN_AVBPUBKEY] == new_key

    def test_identical_keys_warning(self, tmp_path, caplog):
        """Test that identical keys produce a warning and copy the file unchanged.

        Note: The U-Boot binary must contain the old key for this test to reach
        the identical key check. The validation happens in this order:
        1. Check if old key exists in binary (must pass)
        2. Check if old key occurs exactly once (must pass)
        3. Check if keys are identical (triggers warning path)
        """
        import logging

        same_key = bytes([0xCC] * LEN_AVBPUBKEY)

        # Create U-Boot binary with the key embedded (important: must contain the key!)
        prefix = bytes([0x00] * 100)
        suffix = bytes([0xFF] * 100)
        old_uboot_data = prefix + same_key + suffix

        old_uboot_bin = tmp_path / "old_uboot.bin"
        new_uboot_bin = tmp_path / "new_uboot.bin"
        old_avbpubkey = tmp_path / "old.avbpubkey"
        new_avbpubkey = tmp_path / "new.avbpubkey"

        old_uboot_bin.write_bytes(old_uboot_data)
        old_avbpubkey.write_bytes(same_key)
        new_avbpubkey.write_bytes(same_key)

        # Capture INFO level logs (caplog defaults to WARNING and above)
        with caplog.at_level(logging.INFO):
            # Perform replacement (should succeed with warning)
            replace_avbpubkey(str(old_uboot_bin), str(new_uboot_bin), str(old_avbpubkey), str(new_avbpubkey))

        # Verify the output file exists and is identical to input
        new_uboot_data = new_uboot_bin.read_bytes()
        assert new_uboot_data == old_uboot_data

        # Verify warning was logged
        assert "Old key and new key are identical" in caplog.text
        assert "development/testing" in caplog.text
        assert "Skipping replacement" in caplog.text
        assert "Copied U-Boot binary (no replacement needed)" in caplog.text


if __name__ == "__main__":
    pytest.main([__file__, "-v"])

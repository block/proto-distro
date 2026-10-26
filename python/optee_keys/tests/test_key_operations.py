"""Tests for key operation utility functions."""

import shutil
from pathlib import Path

import pytest

from optee_keys.src.core import (
    compare_key_do,
    inject_key_do,
    print_key_do,
    read_key_from_elf,
)
from optee_keys.src.types import RSA_F4

# Get path to test data directory
TEST_DATA_DIR = Path(__file__).parent / "data"


def test_print_key_do():
    """Test printing key components from ELF file."""
    elf_path = TEST_DATA_DIR / "original.elf"
    hex_exp, hex_mod = print_key_do(elf_path)

    # Check that we get hex strings
    assert hex_exp.startswith("0x")
    assert isinstance(hex_mod, str)
    assert len(hex_mod) > 0
    # RSA-2048 modulus should be 512 hex characters (256 bytes)
    assert len(hex_mod) == 512


def test_compare_key_do_matching():
    """Test comparing ELF and PEM keys when they match."""
    elf_path = TEST_DATA_DIR / "original.elf"
    pem_path = TEST_DATA_DIR / "default_key.pub.pem"

    # Original ELF should match default key
    assert compare_key_do(elf_path, pem_path) is True


def test_compare_key_do_not_matching():
    """Test comparing ELF and PEM keys when they don't match."""
    elf_path = TEST_DATA_DIR / "original.elf"
    dev_pem_path = TEST_DATA_DIR / "dev_key.pub.pem"

    # Original ELF should not match dev key
    assert compare_key_do(elf_path, dev_pem_path) is False


# Negative test scenarios


class TestELFKeyManagementNegative:
    """Negative test cases for ELF key management."""

    def test_elf_key_mismatch(self):
        """Test that ELF files with different keys are detected."""
        original_elf = TEST_DATA_DIR / "original.elf"
        modified_elf = TEST_DATA_DIR / "modified.elf"

        # Read keys from both ELF files
        orig_exp, orig_mod = read_key_from_elf(original_elf)
        mod_exp, mod_mod = read_key_from_elf(modified_elf)

        # Keys should be different
        assert orig_mod != mod_mod, "Original and modified ELFs should have different keys"
        # Exponents should be the same
        assert orig_exp == mod_exp, "Exponents should match for both ELF files"
        # Exponents should be standard value
        assert orig_exp == RSA_F4, "Exponent should be RSA_F4 (65537)"

    def test_compare_key_do_detects_mismatch(self):
        """Test that compare_key_do properly detects key mismatches."""
        original_elf = TEST_DATA_DIR / "original.elf"
        dev_key = TEST_DATA_DIR / "dev_key.pub.pem"

        # Original ELF with default key should not match dev key
        assert not compare_key_do(original_elf, dev_key)

    def test_inject_wrong_size_key_fails(self, tmp_path):
        """Test that injecting a key with wrong size fails."""
        original_elf = TEST_DATA_DIR / "original.elf"
        rsa4096_key = TEST_DATA_DIR / "rsa4096_key.pub.pem"

        # Copy original ELF to temp location
        temp_elf = tmp_path / "temp.elf"
        shutil.copy(original_elf, temp_elf)

        # Test with RSA-4096 key (expects RSA-2048, 256 bytes)
        # RSA-4096 has 512-byte modulus, should fail
        output_elf = tmp_path / "output.elf"
        with pytest.raises(ValueError, match="Modulus size mismatch"):
            inject_key_do(temp_elf, rsa4096_key, output_elf)

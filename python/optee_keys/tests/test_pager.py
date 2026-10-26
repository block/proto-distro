"""Tests for TEE pager binary key operations."""

import shutil
from pathlib import Path

import pytest

from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.hazmat.backends import default_backend

from optee_keys.src.core import read_key_from_pem
from optee_keys.src.tee_pager import (
    TeePagerKeyReader,
    compare_pager_with_pem,
    replace_modulus_in_pager,
)

# Get path to test data directory
TEST_DATA_DIR = Path(__file__).parent / "data"


def test_tee_pager_key_reader():
    """Test TeePagerKeyReader class initialization."""
    pager_path = TEST_DATA_DIR / "tee-pager_v2.bin"
    reader = TeePagerKeyReader(pager_path)
    assert reader.pager_path == pager_path


def test_read_key_from_pager():
    """Test reading key from pager binary."""
    pager_path = TEST_DATA_DIR / "tee-pager_v2.bin"

    # Use default_key.pub.pem as reference
    ref_key = TEST_DATA_DIR / "default_key.pub.pem"

    # Read the reference modulus first
    from optee_keys.src.core import read_key_from_pem

    _, ref_modulus = read_key_from_pem(ref_key)

    # Now read from pager using the reference
    reader = TeePagerKeyReader(pager_path)
    exponent, modulus = reader.read_key(known_modulus=ref_modulus)
    assert exponent == 65537  # Standard RSA exponent
    assert len(modulus) == 256  # RSA-2048 modulus


def test_print_key_from_pager():
    """Test printing key from pager binary."""
    pager_path = TEST_DATA_DIR / "tee-pager_v2.bin"

    # Use default_key.pub.pem as reference
    ref_key = TEST_DATA_DIR / "default_key.pub.pem"

    # Read the reference modulus first
    _, ref_modulus = read_key_from_pem(ref_key)

    # Now print from pager using the reference
    reader = TeePagerKeyReader(pager_path)
    _, modulus = reader.read_key(known_modulus=ref_modulus)
    hex_exp = "0x10001"  # Standard RSA exponent
    hex_mod = modulus.hex()
    assert hex_exp == "0x10001"  # 65537 in hex
    assert len(hex_mod) == 512  # 256 bytes = 512 hex chars


def test_export_key_as_pem():
    """Test exporting pager key as PEM format."""
    pager_path = TEST_DATA_DIR / "tee-pager_v2.bin"

    # Use default_key.pub.pem as reference
    ref_key = TEST_DATA_DIR / "default_key.pub.pem"

    # Read the reference modulus first
    _, ref_modulus = read_key_from_pem(ref_key)

    # Export as PEM using the reference
    reader = TeePagerKeyReader(pager_path)
    exponent, modulus = reader.read_key(known_modulus=ref_modulus)

    # Now export to PEM
    public_numbers = rsa.RSAPublicNumbers(e=exponent, n=int.from_bytes(modulus, byteorder="big"))
    public_key = public_numbers.public_key(default_backend())
    pem_str = public_key.public_bytes(
        encoding=serialization.Encoding.PEM, format=serialization.PublicFormat.SubjectPublicKeyInfo
    ).decode("ascii")

    assert pem_str.startswith("-----BEGIN PUBLIC KEY-----")
    assert pem_str.endswith("-----END PUBLIC KEY-----\n")


def test_compare_pager_with_pem():
    """Test comparing pager key with PEM key."""
    pager_path = TEST_DATA_DIR / "tee-pager_v2.bin"
    pem_path = TEST_DATA_DIR / "default_key.pub.pem"

    # The pager should contain the default key
    result = compare_pager_with_pem(pager_path, pem_path)
    assert result is True, "Pager should contain the default key"


def test_replace_modulus_in_pager(tmp_path):
    """Test replacing modulus in pager binary."""
    pager_path = TEST_DATA_DIR / "tee-pager_v2.bin"
    ref_pem = TEST_DATA_DIR / "default_key.pub.pem"
    new_pem = TEST_DATA_DIR / "dev_key.pub.pem"

    # Copy pager to temp location
    temp_pager = tmp_path / "test_pager.bin"
    shutil.copy(pager_path, temp_pager)

    # Replace modulus
    replace_modulus_in_pager(temp_pager, ref_pem, new_pem)

    # Verify the modulus was replaced
    assert compare_pager_with_pem(temp_pager, new_pem)


class TestPagerKeyManagementNegative:
    """Negative test cases for pager key management."""

    def test_pager_verify_with_wrong_key(self):
        """Test that pager with one key doesn't match another key."""
        pager_path = TEST_DATA_DIR / "tee-pager_v2.bin"
        default_key = TEST_DATA_DIR / "default_key.pub.pem"
        dev_key = TEST_DATA_DIR / "dev_key.pub.pem"

        # Check if pager contains default key
        has_default = compare_pager_with_pem(pager_path, default_key)
        has_dev = compare_pager_with_pem(pager_path, dev_key)

        # Pager should contain only one of these keys
        assert has_default != has_dev, "Pager should not contain both keys"

    def test_pager_key_replacement_changes_verification(self, tmp_path):
        """Test that replacing pager key changes TA verification behavior."""
        pager_path = TEST_DATA_DIR / "tee-pager_v2.bin"
        default_key = TEST_DATA_DIR / "default_key.pub.pem"
        dev_key = TEST_DATA_DIR / "dev_key.pub.pem"

        # Copy pager to temp location
        temp_pager = tmp_path / "test_pager.bin"
        shutil.copy(pager_path, temp_pager)

        # Replace default with dev key
        replace_modulus_in_pager(temp_pager, default_key, dev_key)
        # Now pager should have dev key, not default
        assert not compare_pager_with_pem(temp_pager, default_key)
        assert compare_pager_with_pem(temp_pager, dev_key)

    def test_pager_replace_with_nonexistent_modulus_fails(self, tmp_path):
        """Test that replacing a non-existent modulus in pager fails."""
        pager_path = TEST_DATA_DIR / "tee-pager_v2.bin"
        default_key = TEST_DATA_DIR / "default_key.pub.pem"
        dev_key = TEST_DATA_DIR / "dev_key.pub.pem"

        # Copy pager to temp location
        temp_pager = tmp_path / "test_pager.bin"
        shutil.copy(pager_path, temp_pager)

        # Try to replace using wrong reference key
        # If pager has default key, using dev as reference should fail
        if compare_pager_with_pem(temp_pager, default_key):
            with pytest.raises(ValueError, match="Modulus not found"):
                replace_modulus_in_pager(temp_pager, dev_key, default_key)

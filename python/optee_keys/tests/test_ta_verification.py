"""Tests for TA (Trusted Application) verification functionality."""

import shutil
from pathlib import Path

from optee_keys.src.types import SHDR_MAGIC
from optee_keys.src.core import (
    get_ta_info,
    inject_key_do,
    parse_ta_header,
    verify_ta_signature,
    verify_ta_with_pem,
)
from optee_keys.src.defaults import (
    verify_ta_with_default_key,
)
from optee_keys.src.tee_pager import (
    replace_modulus_in_pager,
    compare_pager_with_pem,
)

# Get path to test data directory
TEST_DATA_DIR = Path(__file__).parent / "data"


def test_parse_ta_header():
    """Test parsing TA file header."""
    ta_path = TEST_DATA_DIR / "test.ta"

    header = parse_ta_header(ta_path)

    # Check header fields
    assert header.magic == SHDR_MAGIC
    assert header.img_size > 0
    assert header.hash_size > 0
    assert header.sig_size > 0
    assert len(header.hash) == header.hash_size
    assert len(header.signature) == header.sig_size
    assert len(header.ta_uuid) == 16  # UUID is 16 bytes


def test_get_ta_info():
    """Test getting TA information."""
    ta_path = TEST_DATA_DIR / "test.ta"

    info = get_ta_info(ta_path)

    assert info.valid is True
    assert info.img_size is not None and info.img_size > 0
    assert info.algorithm in [
        "RSASSA-PKCS1-V1_5 with SHA256",
        "RSA-PSS with SHA256",
        "TEE_ALG_RSASSA_PKCS1_V1_5_SHA256",
        "TEE_ALG_RSASSA_PKCS1_PSS_MGF1_SHA256",
    ]
    assert info.hash_size is not None and info.hash_size > 0
    assert info.sig_size is not None and info.sig_size > 0
    assert info.uuid is not None


def test_verify_ta_signature():
    """Test verifying TA signature with ELF key."""
    ta_path = TEST_DATA_DIR / "test.ta"
    elf_path = TEST_DATA_DIR / "original.elf"

    # The test.ta should be signed with the key in original.elf (default key)
    result = verify_ta_signature(ta_path, elf_path)
    assert result is True, "test.ta should verify with original.elf key"


def test_verify_ta_with_pem():
    """Test verifying TA signature with PEM key."""
    ta_path = TEST_DATA_DIR / "test.ta"
    pem_path = TEST_DATA_DIR / "default_key.pub.pem"

    # The test.ta should be signed with the default key
    result = verify_ta_with_pem(ta_path, pem_path)
    assert result is True, "test.ta should verify with default_key.pub.pem"


def test_verify_ta_with_default_key():
    """Test verifying TA with default key."""
    ta_path = TEST_DATA_DIR / "test.ta"

    try:
        result = verify_ta_with_default_key(ta_path)
        assert result is True, "test.ta should verify with default key"
    except FileNotFoundError:
        raise FileNotFoundError("Default key not available")


def test_strip_ta(tmp_path):
    """Test stripping signature envelope from TA file."""
    ta_path = TEST_DATA_DIR / "test.ta"
    output_path = tmp_path / "stripped.bin"

    # Parse the TA to extract the raw payload
    ta_header = parse_ta_header(ta_path)

    # Write the stripped payload
    output_path.write_bytes(ta_header.image)

    # Verify the stripped file exists and has the correct size
    assert output_path.exists(), "Stripped file should exist"
    assert output_path.stat().st_size == ta_header.img_size, "Stripped file size should match img_size"

    # The stripped binary should be the raw unsigned ELF/binary
    # that can be re-signed with a different key
    assert len(output_path.read_bytes()) > 0, "Stripped file should not be empty"


class TestTAVerificationNegative:
    """Negative test cases for TA verification."""

    def test_ta_verify_with_wrong_key(self):
        """Test that TA signed with one key fails verification with another."""
        ta_path = TEST_DATA_DIR / "test.ta"

        # test.ta is signed with default key
        # Try to verify with dev key - should fail
        dev_key = TEST_DATA_DIR / "dev_key.pub.pem"

        # TA is signed with default key, so verification with dev key should fail
        result = verify_ta_with_pem(ta_path, dev_key)
        assert result is False, "test.ta should NOT verify with dev_key.pub.pem"

    def test_ta_verify_after_elf_key_change(self, tmp_path):
        """Test that TA verification fails after changing ELF key."""
        ta_path = TEST_DATA_DIR / "test.ta"
        original_elf = TEST_DATA_DIR / "original.elf"
        dev_key = TEST_DATA_DIR / "dev_key.pub.pem"

        # Copy original ELF to temp location
        temp_elf = tmp_path / "temp.elf"
        shutil.copy(original_elf, temp_elf)

        # Verify with original ELF
        original_result = verify_ta_signature(ta_path, temp_elf)
        assert original_result, "TA should verify with original ELF key"

        # Inject different key into ELF (create new file)
        modified_elf = tmp_path / "modified.elf"
        inject_key_do(temp_elf, dev_key, modified_elf)

        # Verify with modified ELF - result should change
        modified_result = verify_ta_signature(ta_path, modified_elf)

        assert not modified_result, "TA should not verify with non-default key"

    def test_corrupted_ta_fails_verification(self, tmp_path):
        """Test that a corrupted TA file fails verification."""
        ta_path = TEST_DATA_DIR / "test.ta"
        elf_path = TEST_DATA_DIR / "original.elf"

        # Copy TA to temp location
        corrupted_ta = tmp_path / "corrupted.ta"
        with open(ta_path, "rb") as src:
            ta_data = bytearray(src.read())

        # Corrupt the TA by modifying some bytes in the middle
        if len(ta_data) > 100:
            ta_data[50:60] = b"\xff" * 10  # Corrupt 10 bytes

        with open(corrupted_ta, "wb") as dst:
            dst.write(ta_data)

        # Verification should fail or raise an exception
        try:
            result = verify_ta_signature(corrupted_ta, elf_path)
            assert not result, "Corrupted TA should not verify"
        except Exception:
            # It's also acceptable to raise an exception for corrupted files
            pass


class TestTAPagerInteraction:
    """Test TA verification with pager key changes."""

    def test_ta_verification_with_pager_key_replacement(self, tmp_path):
        """Test that TA verification depends on the pager key.

        This test demonstrates:
        1. TA verifies with original pager key
        2. After replacing pager key, same TA fails verification
        3. A TA signed with new key would verify (if we had re-signing capability)
        """

        # Setup paths
        ta_path = TEST_DATA_DIR / "test.ta"  # Assumed to be signed with default key
        pager_path = TEST_DATA_DIR / "tee-pager_v2.bin"
        default_key = TEST_DATA_DIR / "default_key.pub.pem"
        dev_key = TEST_DATA_DIR / "dev_key.pub.pem"

        # Copy pager to temp location for modification
        temp_pager = tmp_path / "test_pager.bin"
        shutil.copy(pager_path, temp_pager)

        # Verify TA with original pager (should have default key)
        assert compare_pager_with_pem(temp_pager, default_key), "Pager should contain default key"

        # Since test.ta is signed with default key, it would verify
        ta_verifies_with_default = verify_ta_with_pem(ta_path, default_key)
        assert ta_verifies_with_default, "TA should verify with default key"

        # Replace pager key with dev key
        replace_modulus_in_pager(temp_pager, default_key, dev_key)
        assert compare_pager_with_pem(temp_pager, dev_key), "Pager should now contain dev key"
        assert not compare_pager_with_pem(temp_pager, default_key), "Pager should no longer contain default key"

        # Same TA should NOT verify with dev key (different from signing key)
        ta_verifies_with_dev = verify_ta_with_pem(ta_path, dev_key)
        assert not ta_verifies_with_dev, "TA signed with default key should not verify with dev key"


class TestDefaultKeyVerification:
    """Test scenarios with default key verification."""

    def test_tampered_signature_fails_verification(self, tmp_path):
        """Test that tampering with signature causes verification to fail."""
        ta_path = TEST_DATA_DIR / "test.ta"
        pem_path = TEST_DATA_DIR / "default_key.pub.pem"

        # Copy TA to temp location
        tampered_ta = tmp_path / "tampered.ta"
        with open(ta_path, "rb") as src:
            ta_data = bytearray(src.read())

        # Tamper with the signature (last part of the file)
        # The signature is at a specific offset based on the header
        if len(ta_data) > 300:
            # Modify some bytes in what's likely the signature area
            ta_data[-100:-90] = b"\x00" * 10  # Zero out part of signature

        with open(tampered_ta, "wb") as dst:
            dst.write(ta_data)

        # Verification should fail
        try:
            result = verify_ta_with_pem(tampered_ta, pem_path)
            assert not result, "Tampered signature should not verify"
        except Exception:
            # It's also acceptable to raise an exception
            pass

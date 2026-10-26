"""Tests for the optee_keys module."""

from pathlib import Path

import pytest
from Crypto.PublicKey import RSA
from elftools.elf.elffile import ELFFile

from optee_keys.src.core import (
    find_symbol_offset,
    inject_key_do,
    read_key_from_elf,
    read_key_from_pem,
)

# Get path to test data directory
TEST_DATA_DIR = Path(__file__).parent / "data"

# Required test files - if any of these are missing, tests should fail
REQUIRED_TEST_FILES = {
    "original.elf": "Original ELF file with default TA key",
    "modified.elf": "Modified ELF file with dev key",
    "dev_key.pub.pem": "Development TA key",
    "default_key.pub.pem": "Default TA key that is automatically generated as part of the build",
}


def check_required_files():
    """Check that all required test files exist."""
    missing = []
    for filename, description in REQUIRED_TEST_FILES.items():
        if not (TEST_DATA_DIR / filename).exists():
            missing.append(f"{filename} ({description})")

    if missing:
        raise RuntimeError(
            "Missing required test files:\n"
            + "\n".join(f"- {f}" for f in missing)
            + "\n\nThese files should be in: "
            + str(TEST_DATA_DIR)
        )


@pytest.fixture(scope="session", autouse=True)
def verify_test_files():
    """Verify all required test files exist before running any tests."""
    check_required_files()


@pytest.fixture(scope="session")
def test_dir(tmp_path_factory):
    """Create a temporary directory for test files."""
    return tmp_path_factory.mktemp("optee_keys_test")


@pytest.fixture(scope="session")
def test_keys(test_dir):
    """Generate test keys."""
    # Generate test key
    key = RSA.generate(2048)

    # Save private key
    private_key_path = test_dir / "test_private.pem"
    with open(private_key_path, "wb") as f:
        f.write(key.export_key("PEM"))

    # Save public key
    public_key_path = test_dir / "test_public.pem"
    with open(public_key_path, "wb") as f:
        f.write(key.publickey().export_key("PEM"))

    return private_key_path, public_key_path


@pytest.fixture(scope="session")
def original_elf(test_dir):
    """Copy the original test ELF file to the test directory."""
    src_path = TEST_DATA_DIR / "original.elf"
    dst_path = test_dir / "original.elf"
    with open(src_path, "rb") as src, open(dst_path, "wb") as dst:
        dst.write(src.read())
    return dst_path


@pytest.fixture(scope="session")
def modified_elf(test_dir):
    """Copy the modified test ELF file to the test directory."""
    src_path = TEST_DATA_DIR / "modified.elf"
    dst_path = test_dir / "modified.elf"
    with open(src_path, "rb") as src, open(dst_path, "wb") as dst:
        dst.write(src.read())
    return dst_path


@pytest.fixture(scope="session")
def dev_pub_key(test_dir):
    """Copy the development public key to the test directory."""
    src_path = TEST_DATA_DIR / "dev_key.pub.pem"
    dst_path = test_dir / "dev_key.pub.pem"
    with open(src_path, "rb") as src, open(dst_path, "wb") as dst:
        dst.write(src.read())
    return dst_path


@pytest.fixture(scope="session")
def default_key(test_dir):
    """Copy the default public key to the test directory."""
    src_path = TEST_DATA_DIR / "default_key.pub.pem"
    dst_path = test_dir / "default_key.pub.pem"
    with open(src_path, "rb") as src, open(dst_path, "wb") as dst:
        dst.write(src.read())
    return dst_path


def test_find_symbol_offset(original_elf):
    """Test finding symbol offsets in ELF file."""
    with open(original_elf, "rb") as f:
        elf = ELFFile(f)
        offset, size = find_symbol_offset(elf, "ta_pub_key_modulus")
        # Check the offset and size are what we expect from the known ELF file
        assert offset == 453100
        assert size == 256


def test_read_key_from_elf(original_elf):
    """Test reading key components from ELF file."""
    exponent, modulus = read_key_from_elf(original_elf)
    assert isinstance(exponent, int)
    assert isinstance(modulus, bytes)
    assert len(modulus) == 256  # Expected modulus size for the test key


def test_read_key_from_pem(dev_pub_key):
    """Test reading key components from PEM file."""
    exponent, modulus = read_key_from_pem(dev_pub_key)
    assert isinstance(exponent, int)
    assert isinstance(modulus, bytes)
    # The modulus size depends on the key type
    assert len(modulus) == 256  # Expected size for a 2048-bit RSA key


def test_compare_original_with_default(original_elf, default_key):
    """Test comparing original ELF key with the default key."""
    elf_exp, elf_mod = read_key_from_elf(original_elf)
    pem_exp, pem_mod = read_key_from_pem(default_key)

    # Original ELF should match default key
    assert elf_exp == pem_exp
    assert elf_mod == pem_mod


def test_compare_modified_with_default(modified_elf, default_key):
    """Test comparing modified ELF key with the default key."""
    elf_exp, elf_mod = read_key_from_elf(modified_elf)
    pem_exp, pem_mod = read_key_from_pem(default_key)

    # Modified ELF should not match default key
    assert elf_exp == pem_exp
    assert elf_mod != pem_mod


def test_compare_original_with_dev(original_elf, dev_pub_key):
    """Test comparing modified ELF key with the default key."""
    elf_exp, elf_mod = read_key_from_elf(original_elf)
    pem_exp, pem_mod = read_key_from_pem(dev_pub_key)

    # Original ELF should not match dev key
    assert elf_exp == pem_exp
    assert elf_mod != pem_mod


def test_compare_modified_with_dev(modified_elf, dev_pub_key):
    """Test comparing modified ELF key with dev key."""
    # Read both keys
    elf_exp, elf_mod = read_key_from_elf(modified_elf)
    pem_exp, pem_mod = read_key_from_pem(dev_pub_key)

    # Modified ELF should have dev key
    assert elf_exp == pem_exp
    assert elf_mod == pem_mod


def test_inject_key_function(original_elf, dev_pub_key, test_dir):
    """Test injecting a new key into ELF file."""
    output_path = test_dir / "injected.elf"
    inject_key_do(original_elf, dev_pub_key, output_path)

    # Verify the key was injected correctly
    org_exp, org_mod = read_key_from_elf(original_elf)
    elf_exp, elf_mod = read_key_from_elf(output_path)
    pem_exp, pem_mod = read_key_from_pem(dev_pub_key)

    # Original key should not match injected key
    assert org_exp == elf_exp
    assert org_mod != elf_mod

    # Injected key should match dev key
    assert elf_exp == pem_exp
    assert elf_mod == pem_mod


def test_find_symbol_offset_errors(original_elf):
    """Test error handling for invalid symbols."""
    with open(original_elf, "rb") as f:
        elf = ELFFile(f)
        with pytest.raises(RuntimeError, match="Symbol .* not found"):
            find_symbol_offset(elf, "nonexistent_symbol")


def test_read_key_from_invalid_elf(test_dir):
    """Test error handling for invalid ELF file."""
    invalid_elf = test_dir / "invalid.elf"
    with open(invalid_elf, "wb") as f:
        f.write(b"not an elf file")

    with pytest.raises(Exception):
        read_key_from_elf(invalid_elf)


def test_read_key_from_invalid_pem(test_dir):
    """Test error handling for invalid PEM file."""
    invalid_pem = test_dir / "invalid.pem"
    with open(invalid_pem, "wb") as f:
        f.write(b"not a pem file")

    with pytest.raises(Exception):
        read_key_from_pem(invalid_pem)

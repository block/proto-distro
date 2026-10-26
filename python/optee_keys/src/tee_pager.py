"""OP-TEE Pager Binary Key Management - Modulus-Only Approach.

This module handles RSA modulus operations on TEE pager binaries
(tee-pager_v2.bin) after gen_tee_bin.py has processed the ELF file.
We only handle the modulus since the exponent is always 65537.
"""

import logging
import shutil
from pathlib import Path
from typing import Tuple

from cryptography.hazmat.backends import default_backend
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import rsa

from .core import read_key_from_pem
from .defaults import get_default_ta_key_path
from .types import KeyInfo

logger = logging.getLogger(__name__)

# Standard RSA exponent used by all OP-TEE keys
STANDARD_RSA_EXPONENT = 65537


class TeePagerKeyReader:
    """Read and manipulate modulus in TEE pager binaries."""

    # Key sizes
    MODULUS_SIZE = 256  # RSA-2048 modulus size in bytes

    def __init__(self, pager_path: Path):
        """Initialize with path to tee-pager_v2.bin.

        Args:
            pager_path: Path to the tee-pager_v2.bin file.
        """
        self.pager_path = Path(pager_path)
        if not self.pager_path.exists():
            raise FileNotFoundError(f"Pager binary not found: {pager_path}")

    def find_modulus(self, known_modulus: bytes) -> int:
        """Find modulus offset by searching for known modulus pattern.

        Args:
            known_modulus: The expected modulus bytes to search for.

        Returns:
            Offset of the modulus in the binary.

        Raises:
            ValueError: If the modulus pattern is not found.
        """
        with open(self.pager_path, "rb") as f:
            data = f.read()

        # Search for modulus pattern
        mod_offset = data.find(known_modulus)
        if mod_offset == -1:
            raise ValueError("Modulus not found in binary. " "This binary may use a different key or structure.")

        return mod_offset

    def read_key(self, known_modulus: bytes | None = None) -> Tuple[int, bytes]:
        """Read RSA public key from the binary (modulus only, exponent assumed 65537).

        Args:
            known_modulus: Optional known modulus to search for.
                         If not provided, tries to use default_ta.pub.pem.

        Returns:
            Tuple of (exponent, modulus) - exponent will always be 65537.
        """
        # If no modulus provided, try the bundled default public key.
        if known_modulus is None:
            try:
                default_key_path = get_default_ta_key_path()
                _, known_modulus = read_key_from_pem(default_key_path)
            except (ImportError, FileNotFoundError, ValueError):
                raise ValueError(
                    "No reference modulus provided and default key not available. " "Please provide a reference key."
                )

        # Find the modulus
        mod_offset = self.find_modulus(known_modulus)

        # Read the modulus
        with open(self.pager_path, "rb") as f:
            f.seek(mod_offset)
            modulus = f.read(self.MODULUS_SIZE)
            if len(modulus) != self.MODULUS_SIZE:
                raise ValueError(f"Could not read full modulus at offset 0x{mod_offset:x}")

        # Return standard exponent and the found modulus
        return STANDARD_RSA_EXPONENT, modulus

    def get_key_info(self, known_modulus: bytes | None = None) -> KeyInfo:
        """Get detailed key information.

        Args:
            known_modulus: Optional known modulus to search for.

        Returns:
            KeyInfo object with key details.
        """
        exponent, modulus = self.read_key(known_modulus)
        return KeyInfo(
            exponent=exponent,
            modulus=modulus,
            key_size=len(modulus) * 8,  # Convert bytes to bits
            source_path=self.pager_path,
        )

    def replace_modulus(
        self,
        reference_modulus: bytes,
        new_modulus: bytes,
        output_path: Path | None = None,
    ) -> None:
        """Replace the modulus with a new one.

        Args:
            reference_modulus: Current modulus to find.
            new_modulus: New modulus to write.
            output_path: Optional output path (modifies in-place if None).

        Raises:
            ValueError: If modulus not found or sizes don't match.
        """
        if len(new_modulus) != self.MODULUS_SIZE:
            raise ValueError(f"New modulus must be {self.MODULUS_SIZE} bytes, got {len(new_modulus)}")

        # Find current modulus location
        mod_offset = self.find_modulus(reference_modulus)
        logger.debug(f"Found modulus at offset 0x{mod_offset:x}")

        # Create backup if modifying in-place
        backup_path = None
        if output_path is None:
            backup_path = f"{self.pager_path}.backup"
            shutil.copy2(self.pager_path, backup_path)
            logger.info(f"Created backup: {backup_path}")

        try:
            # Determine output file
            out_file = output_path or self.pager_path

            # Read the entire file
            with open(self.pager_path, "rb") as f:
                data = bytearray(f.read())

            # Replace the modulus
            data[mod_offset : mod_offset + self.MODULUS_SIZE] = new_modulus

            # Write the modified data
            with open(out_file, "wb") as f:
                f.write(data)

            # Verify the replacement
            with open(out_file, "rb") as f:
                f.seek(mod_offset)
                written_modulus = f.read(self.MODULUS_SIZE)

            if written_modulus != new_modulus:
                raise ValueError("Modulus verification failed after writing")

            logger.info(f"✓ Modulus successfully replaced at offset 0x{mod_offset:x}")

        except Exception as e:
            # Restore backup on failure
            if output_path is None and backup_path:
                shutil.move(backup_path, self.pager_path)
                logger.error("Error occurred, restored backup")
            raise e

    def compare_key_with_pem(self, pem_path: Path) -> bool:
        """Compare the embedded modulus with a PEM key's modulus.

        Args:
            pem_path: Path to PEM file (public or private key).

        Returns:
            True if moduli match, False otherwise.
        """
        # Read key from PEM
        pem_exp, pem_mod = read_key_from_pem(pem_path)

        # Try to find this modulus in the binary
        try:
            self.find_modulus(pem_mod)
            return True  # Found it!
        except ValueError:
            return False  # Not found


# Convenience functions that mirror the ELF-based API
def read_key_from_pager(pager_path: Path) -> Tuple[int, bytes]:
    """Read RSA public key from a tee-pager_v2.bin file.

    Args:
        pager_path: Path to the pager binary.

    Returns:
        Tuple of (exponent, modulus) - exponent will be 65537.
    """
    reader = TeePagerKeyReader(pager_path)
    return reader.read_key()


def print_key_from_pager(pager_path: Path) -> Tuple[str, str]:
    """Print the public key components from a pager binary.

    Args:
        pager_path: Path to the pager binary.

    Returns:
        Tuple of (hex_exponent, hex_modulus).
    """
    exponent, modulus = read_key_from_pager(pager_path)
    return hex(exponent), modulus.hex()


def export_key_as_pem(pager_path: Path) -> str:
    """Export the public key from a pager binary as PEM format.

    Args:
        pager_path: Path to the pager binary.

    Returns:
        PEM-formatted public key string.
    """
    exponent, modulus = read_key_from_pager(pager_path)

    # Convert modulus bytes to integer
    modulus_int = int.from_bytes(modulus, byteorder="big")

    # Create RSA public key
    public_numbers = rsa.RSAPublicNumbers(exponent, modulus_int)
    public_key = public_numbers.public_key(default_backend())

    # Export as PEM
    pem = public_key.public_bytes(
        encoding=serialization.Encoding.PEM,
        format=serialization.PublicFormat.SubjectPublicKeyInfo,
    )

    return pem.decode("utf-8")


def compare_pager_with_pem(pager_path: Path, pem_path: Path) -> bool:
    """Compare pager binary key with a PEM key.

    Args:
        pager_path: Path to the pager binary.
        pem_path: Path to the PEM file.

    Returns:
        True if keys match, False otherwise.
    """
    reader = TeePagerKeyReader(pager_path)
    return reader.compare_key_with_pem(pem_path)


def replace_modulus_in_pager(
    pager_path: Path,
    reference_pem: Path,
    new_pem: Path,
    output_path: Path | None = None,
) -> None:
    """Replace modulus in a tee-pager_v2.bin file.

    Args:
        pager_path: Path to the pager binary.
        reference_pem: PEM file with current key (to find modulus).
        new_pem: PEM file with new key (source of new modulus).
        output_path: Optional output path (modifies in-place if None).
    """
    # Read reference modulus
    ref_exp, ref_mod = read_key_from_pem(reference_pem)
    if ref_exp != STANDARD_RSA_EXPONENT:
        logger.warning(f"Reference key has non-standard exponent {ref_exp}")

    # Read new modulus
    new_exp, new_mod = read_key_from_pem(new_pem)
    if new_exp != STANDARD_RSA_EXPONENT:
        raise ValueError(
            f"New key must have exponent {STANDARD_RSA_EXPONENT}, got {new_exp}. "
            "Only standard RSA keys are supported."
        )

    # Replace the modulus
    reader = TeePagerKeyReader(pager_path)
    reader.replace_modulus(ref_mod, new_mod, output_path)

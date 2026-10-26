"""Type definitions for OP-TEE key operations.

This module provides dataclass definitions for OP-TEE Trusted Application
headers and key information.
"""

from dataclasses import dataclass
from pathlib import Path
from typing import Optional


@dataclass
class TAHeader:
    """Parsed TA (Trusted Application) header information.

    Attributes:
        magic: Magic number identifying the TA format.
        img_type: Type of the image (bootstrap/primary/etc).
        img_size: Size of the image in bytes.
        algo: Algorithm code for signature verification.
        hash_size: Size of the hash in bytes.
        sig_size: Size of the signature in bytes.
        hash: Computed hash of the image.
        signature: Digital signature of the TA.
        ta_uuid: UUID of the Trusted Application.
        ta_version: Version number of the TA.
        image: The actual TA image data.
        shdr: The signed header data.
    """

    magic: int
    img_type: int
    img_size: int
    algo: int
    hash_size: int
    sig_size: int
    hash: bytes
    signature: bytes
    ta_uuid: bytes
    ta_version: bytes
    image: bytes
    shdr: bytes

    @property
    def uuid_str(self) -> str:
        """Get UUID as formatted string.

        Returns:
            UUID in standard format (e.g., "12345678-1234-1234-1234-123456789012").
        """
        if len(self.ta_uuid) != 16:
            return "invalid-uuid"

        # Format as standard UUID string
        uuid_hex = self.ta_uuid.hex()
        return f"{uuid_hex[:8]}-{uuid_hex[8:12]}-{uuid_hex[12:16]}-{uuid_hex[16:20]}-{uuid_hex[20:32]}"

    @property
    def algorithm_name(self) -> str:
        """Get human-readable algorithm name.

        Returns:
            Algorithm name as string.
        """
        return Algorithm.name(self.algo)


@dataclass
class TAInfo:
    """Information about a Trusted Application.

    Attributes:
        valid: Whether the TA is valid and properly signed.
        img_type: Type of the image (e.g., "primary", "bootstrap").
        img_size: Size of the image in bytes.
        algorithm: Signature algorithm used.
        hash_size: Size of the hash in bytes.
        sig_size: Size of the signature in bytes.
        img_hash: Computed hash of the image (hex string).
        stored_hash: Hash stored in the header (hex string).
        signature: Digital signature (hex string).
        uuid: UUID of the TA as string.
        error: Error message if validation failed.
    """

    valid: bool
    img_type: Optional[str] = None
    img_size: Optional[int] = None
    algorithm: Optional[str] = None
    hash_size: Optional[int] = None
    sig_size: Optional[int] = None
    img_hash: Optional[str] = None
    stored_hash: Optional[str] = None
    signature: Optional[str] = None
    uuid: Optional[str] = None
    error: Optional[str] = None


@dataclass
class KeyInfo:
    """Information about an RSA key.

    Attributes:
        exponent: RSA public exponent (typically 65537).
        modulus: RSA modulus as bytes.
        key_size: Key size in bits (e.g., 2048).
        source_path: Path to the source file containing the key.
    """

    exponent: int
    modulus: bytes
    key_size: int
    source_path: Path

    @property
    def modulus_hex(self) -> str:
        """Get modulus as hex string.

        Returns:
            Modulus encoded as hexadecimal string.
        """
        return self.modulus.hex()

    @property
    def fingerprint(self) -> str:
        """Get first 32 chars of modulus for identification.

        Returns:
            Uppercase hex string of first 16 bytes of modulus.
        """
        return self.modulus_hex[:32].upper()

    @property
    def exponent_hex(self) -> str:
        """Get exponent as hex string.

        Returns:
            Exponent encoded as hexadecimal string.
        """
        return hex(self.exponent)


class Algorithm:
    """OP-TEE algorithm constants and utilities.

    These constants are defined in the OP-TEE source code:
    - optee_os/lib/libutee/include/tee_api_defines.h
    - optee_os/ta/pkcs11/include/pkcs11_ta.h

    The values combine algorithm type and hash function:
    - 0x70004830: TEE_ALG_RSASSA_PKCS1_V1_5_SHA256
    - 0x70414930: TEE_ALG_RSASSA_PKCS1_PSS_MGF1_SHA256

    Reference: https://github.com/OP-TEE/optee_os/blob/master/lib/libutee/include/tee_api_defines.h
    """

    RSA_PKCS1_V15_SHA256 = 0x70004830  # TEE_ALG_RSASSA_PKCS1_V1_5_SHA256
    RSA_PSS_SHA256 = 0x70414930  # TEE_ALG_RSASSA_PKCS1_PSS_MGF1_SHA256

    @classmethod
    def name(cls, algo_code: int) -> str:
        """Get human-readable algorithm name.

        Args:
            algo_code: Algorithm code from TA header.

        Returns:
            Human-readable algorithm name or "Unknown" for unrecognized codes.
        """
        algo_map = {
            cls.RSA_PKCS1_V15_SHA256: "RSA-PKCS#1 v1.5 with SHA256",
            cls.RSA_PSS_SHA256: "RSA-PSS with SHA256",
        }
        return algo_map.get(algo_code, f"Unknown (0x{algo_code:08x})")


# File format constants
SHDR_MAGIC = 0x4F545348  # "SHTO" - SHdr Tagged Object
UUID_SIZE = 16
VERSION_SIZE = 4
HEADER_SIZE = 20
MIN_EXPONENT_SIZE_BYTES = 4

# Standard RSA exponent
RSA_F4 = 65537  # Standard RSA public exponent (Fermat F4)

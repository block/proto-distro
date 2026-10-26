import logging
import os
import re
import struct
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import List, Optional

import click
from Crypto.Hash import SHA256
from Crypto.PublicKey import ECC
from Crypto.Signature import DSS

logging.basicConfig(level=logging.INFO, format="%(asctime)s - %(levelname)s - %(message)s")
logger = logging.getLogger(__name__)

"""
A homegrown tool to sign FSBL images that replaces the STM32 Signing Tool
Warning: Only tested on the STM32MP131f

Resources:
https://wiki.st.com/stm32mpu/wiki/STM32_header_for_binary_files
https://wiki.st.com/stm32mpu/wiki/Signing_tool
"""

# Product-specific constants (for Control Board STM32MP13 series)
PRODUCT_C2 = "C2"
PRODUCT_C3 = "C3"
PRODUCTS = {
    PRODUCT_C2,
    PRODUCT_C3,
}

# FSBL Anti-rollback version numbers. WARNING: Changing this affects OTP and can brick devices.
IMAGE_VERSION_NUMBER = {
    PRODUCT_C2: 0,
    PRODUCT_C3: 0,
}

# FSBL Signing Key index. WARNING: Changing this affects OTP and can brick devices.
# This value should match X of the privateKey0X.pem used for signing.
SIGNING_KEY_INDEX = {
    PRODUCT_C2: 0,
    PRODUCT_C3: 1,
}

# STM32 Header constants for Control Board
HEADER_BASE_LEN = 128
HEADER_VERSION = bytes.fromhex("00020000")  # Version 2.0
AUTH_OPTION_FLAGS = 0x80000001  # Authentication + Padding b0=1, b31=1
IMAGE_ALIGNMENT = 32  # Image must be 32-byte aligned

# Magic
HEADER_BASE_MAGIC = bytes.fromhex("53544D32")  # "STM\x32"
HEADER_EXT_AUTH = bytes.fromhex("53540002")  # "ST\x00\x02" - Authentication
HEADER_EXT_PADDING = bytes.fromhex("5354FFFF")  # "ST\xFF\xFF" - Padding

# Sentinel placed at the start of the padding extension's random bytes so TF-A
# can distinguish a proto-signed image (padding prefix carries pk_idx) from a
# stock ST-signed one (padding is fully random). Adding a new extension type
# is rejected by the STM32MP13 BootROM, so proto metadata is stashed inside
# the padding extension which the ROM already knows about.
HEADER_PADDING_PROTO_MAGIC = b"PROT"
HEADER_PADDING_PROTO_PREFIX_LEN = 8  # magic(4) + pk_idx(4)

# Header field sizes
HEADER_SIGNATURE_LEN = 64
HEADER_AUTH_LEN = 340
HEADER_PADDING_LEN = 44
HEADER_SIGNING_OFFSET = 72  # Skip magic(4) + signature(64) + checksum(4)
HEADER_AUTH_METADATA_LEN = 84  # Size before public key hashes start
PADDING_METADATA_LEN = 8  # extension_type(4) + extension_length(4)

HEADER_EXT_TOTAL_LEN = HEADER_AUTH_LEN + HEADER_PADDING_LEN
HEADER_TOTAL_LEN = HEADER_BASE_LEN + HEADER_EXT_TOTAL_LEN
assert HEADER_TOTAL_LEN <= 512, f"STM32 header must not exceed 512 bytes (got {HEADER_TOTAL_LEN})"

PUBKEY_HASH_LEN = 32
PUBKEY_COUNT = 8  # Number of supported public keys. This should stay fixed even if we don't use all of them.
RAW_PUBKEY_LEN = 64
ECDSA_ALGO = 0x1  # ECDSA P-256

# File naming
PKHTH_NAME = "publicKeysHashHashes.bin"
PKH_NAME = "publicKeyHash0{}.bin"
PRIVATE_KEY_PATTERN = re.compile(r"^privateKey0([0-7])\.pem$")
PUBLIC_KEY_PATTERN = re.compile(r"^publicKey0([0-7])\.pem$")


@dataclass
class STM32BaseHeader:
    """STM32 Base Header (128 bytes total)"""

    _FMT = "<4s64sIIII12sIII20s"  # Format string for struct packing
    # fmt: off
    _FIELDS = ["magic", "image_signature", "image_checksum", "header_version",
               "image_length", "image_entry_point", "reserved", "version_number",
               "option_flags", "header_extensions_len", "padding"]

    magic: bytes            # 4 bytes - "STM\x32"
    image_signature: bytes  # 64 bytes
    image_checksum: int     # 4 bytes
    header_version: int     # 4 bytes
    image_length: int       # 4 bytes
    image_entry_point: int  # 4 bytes
    reserved: bytes         # 12 bytes
    version_number: int     # 4 bytes
    option_flags: int       # 4 bytes
    header_extensions_len: int  # 4 bytes
    padding: bytes          # 20 bytes
    # fmt: on

    def pack(self) -> bytes:
        """Pack header into bytes"""
        return struct.pack(self._FMT, *(getattr(self, field) for field in self._FIELDS))

    @classmethod
    def unpack(cls, data: bytes) -> "STM32BaseHeader":
        """Unpack header from bytes"""
        if len(data) != HEADER_BASE_LEN:
            raise ValueError(f"Base header must be {HEADER_BASE_LEN} bytes")
        return cls(**dict(zip(cls._FIELDS, struct.unpack(cls._FMT, data))))


@dataclass
class STM32AuthHeader:
    """STM32 Authentication Header (340 bytes total)"""

    _FMT = "<4sIIII64s"  # Format string for struct packing
    # fmt: off
    _FIELDS = ["extension_type", "extension_length", "public_key_index",
               "public_key_number", "ecdsa_algorithm", "ecdsa_public_key"]

    extension_type: bytes     # 4 bytes - "ST\x00\x02"
    extension_length: int     # 4 bytes
    public_key_index: int     # 4 bytes
    public_key_number: int    # 4 bytes
    ecdsa_algorithm: int      # 4 bytes
    ecdsa_public_key: bytes   # 64 bytes
    public_key_hashes: List[bytes]  # 8 * 32 = 256 bytes
    # fmt: on

    def pack(self) -> bytes:
        """Pack header into bytes"""
        result = struct.pack(self._FMT, *(getattr(self, field) for field in self._FIELDS))
        return result + b"".join(self.public_key_hashes)

    @classmethod
    def unpack(cls, data: bytes) -> "STM32AuthHeader":
        """Unpack header from bytes"""
        if len(data) != HEADER_AUTH_LEN:
            raise ValueError(f"Auth header must be {HEADER_AUTH_LEN} bytes")

        # Unpack main fields
        fields = dict(zip(cls._FIELDS, struct.unpack(cls._FMT, data[:HEADER_AUTH_METADATA_LEN])))

        # Extract public key hashes
        pk_hashes = [
            data[HEADER_AUTH_METADATA_LEN + i * PUBKEY_HASH_LEN : HEADER_AUTH_METADATA_LEN + (i + 1) * PUBKEY_HASH_LEN]
            for i in range(PUBKEY_COUNT)
        ]

        return cls(**fields, public_key_hashes=pk_hashes)


@dataclass
class STM32PaddingHeader:
    """STM32 Padding Header (variable size).

    The padding extension is a known extension type accepted by the STM32MP13
    BootROM. We stash proto-signer metadata in the first 8 bytes of the
    padding payload: 4-byte magic (HEADER_PADDING_PROTO_MAGIC) followed by a
    little-endian u32 public_key_index. Remaining bytes stay random.

    When the magic matches on unpack, `public_key_index` is parsed out and the
    `padding` field holds only the trailing random bytes. Otherwise the
    payload is treated as fully-random (stock ST layout) and
    `public_key_index` is None.
    """

    _FMT = "<4sI"
    _FIELDS = ["extension_type", "extension_length"]

    extension_type: bytes  # 4 bytes - "ST\xFF\xFF"
    extension_length: int  # 4 bytes
    padding: bytes         # Random bytes filling to extension_length
    public_key_index: Optional[int] = None

    def pack(self) -> bytes:
        result = struct.pack(self._FMT, self.extension_type, self.extension_length)
        if self.public_key_index is not None:
            result += HEADER_PADDING_PROTO_MAGIC + struct.pack("<I", self.public_key_index)
        result += self.padding
        if len(result) != self.extension_length:
            raise ValueError(
                f"Packed STM32 padding header length {len(result)} does not match "
                f"extension_length {self.extension_length}"
            )
        return result

    @classmethod
    def unpack(cls, data: bytes) -> "STM32PaddingHeader":
        fields = dict(zip(cls._FIELDS, struct.unpack(cls._FMT, data[:PADDING_METADATA_LEN])))
        payload = data[PADDING_METADATA_LEN : fields["extension_length"]]
        pk_idx: Optional[int] = None
        if payload[:4] == HEADER_PADDING_PROTO_MAGIC:
            if len(payload) < HEADER_PADDING_PROTO_PREFIX_LEN:
                raise ValueError(
                    f"Padding payload too short for proto prefix "
                    f"({len(payload)} < {HEADER_PADDING_PROTO_PREFIX_LEN})"
                )
            pk_idx = struct.unpack("<I", payload[4:HEADER_PADDING_PROTO_PREFIX_LEN])[0]
            payload = payload[HEADER_PADDING_PROTO_PREFIX_LEN:]
        return cls(**fields, padding=payload, public_key_index=pk_idx)


@dataclass
class STM32Header:
    """Complete STM32 Header (512 bytes total)"""

    base_header: STM32BaseHeader
    padding_header: STM32PaddingHeader
    auth_header: Optional[STM32AuthHeader] = None

    def pack(self) -> bytes:
        """Pack complete header into bytes"""
        result = self.base_header.pack()
        if self.auth_header:
            result += self.auth_header.pack()
        result += self.padding_header.pack()
        return result

    @classmethod
    def unpack(cls, data: bytes) -> "STM32Header":
        """Unpack complete header from bytes"""

        def get_ext_type(data: bytes, offset: int) -> bytes:
            return data[offset : offset + 4]

        def get_ext_len(data: bytes, offset: int) -> int:
            return struct.unpack("<I", data[offset + 4 : offset + 8])[0]

        if len(data) != HEADER_TOTAL_LEN:
            raise ValueError(f"Complete header must be {HEADER_TOTAL_LEN} bytes")

        # Parse base header first
        base = STM32BaseHeader.unpack(data[:HEADER_BASE_LEN])
        offset = HEADER_BASE_LEN
        auth = None

        ext_type = get_ext_type(data, offset)
        if ext_type == HEADER_EXT_AUTH:
            ext_len = get_ext_len(data, offset)
            auth = STM32AuthHeader.unpack(data[offset : offset + ext_len])
            offset += ext_len

        ext_type = get_ext_type(data, offset)
        if ext_type != HEADER_EXT_PADDING:
            raise ValueError(f"Expected padding header, got {ext_type.hex()}")

        padding = STM32PaddingHeader.unpack(data[offset:])

        return cls(base_header=base, auth_header=auth, padding_header=padding)


class FSBLSigner:
    @staticmethod
    def get_ecc_key_from_file(file_path: str | Path, password: Optional[str] = None) -> ECC.EccKey:
        """Load an ECC key from a PEM file.

        Args:
            file_path: Path to the PEM key file
            password: Optional password for encrypted keys. If None and key is encrypted,
                     ValueError will be raised. If provided for unencrypted key, it's ignored.

        Returns:
            Loaded ECC key
        """
        logger.debug(f"Loading ECC key from {file_path}")

        # Normalize empty password to None
        if password == "":
            password = None

        try:
            with open(file_path, "rb") as keyfile:
                key_data = keyfile.read()
                key = ECC.import_key(key_data, passphrase=password)

            # Check if password was provided but key wasn't encrypted
            # by trying to load again without password
            if password is not None:
                try:
                    ECC.import_key(key_data, passphrase=None)
                    logger.warning(f"Password provided for {file_path} but key is not encrypted (password ignored)")
                except ValueError:
                    # Key is encrypted, password was needed
                    pass

        except ValueError as e:
            # Check if the PEM content indicates encryption to provide accurate error message
            is_encrypted = b"ENCRYPTED" in key_data or b"Proc-Type: 4,ENCRYPTED" in key_data

            if password is None and is_encrypted:
                err_msg = f"Failed to load ECC key from {file_path}. Key appears to be encrypted but no password provided."
            elif password is not None and is_encrypted:
                err_msg = f"Failed to load ECC key from {file_path}. Is the password correct?"
            else:
                # Generic error for non-encryption issues (corrupt key, invalid format, etc.)
                err_msg = f"Failed to load ECC key from {file_path}: {str(e)}"
            logger.error(err_msg)
            raise ValueError(err_msg) from e
        return key

    @staticmethod
    def get_raw_pubkey(key: ECC.EccKey) -> bytes:
        """Get raw public key bytes from ECC key.

        Returns:
            64 bytes containing x|y coordinates (32 bytes each)
        """
        coord_x_bytes = key.pointQ.x.to_bytes(32, byteorder="big")
        coord_y_bytes = key.pointQ.y.to_bytes(32, byteorder="big")
        return coord_x_bytes + coord_y_bytes

    @staticmethod
    def hash_pubkey_with_ecc_alg(key: ECC.EccKey, ecdsa_algo: int) -> SHA256.SHA256Hash:
        """Hash a public key with ECDSA algorithm prefix."""
        raw_pubkey = FSBLSigner.get_raw_pubkey(key)
        ecdsa_algo_le = ecdsa_algo.to_bytes(4, byteorder="little")
        hash_input = ecdsa_algo_le + raw_pubkey
        return SHA256.new(data=hash_input)

    @staticmethod
    def verify_signature_with_raw_pubkey(raw_pubkey: bytes, signature: bytes, message: bytes) -> bool:
        """Verify a signature using a raw public key (x|y coordinates).

        Args:
            raw_pubkey: 64 bytes containing x|y coordinates (32 bytes each)
            signature: The signature to verify (r|s format)
            message: The original message that was signed

        Returns:
            bool: True if signature is valid
        """
        logger.debug("Verifying signature with raw public key")
        # Split the raw public key into x and y coordinates
        x_bytes = raw_pubkey[:32]
        y_bytes = raw_pubkey[32:]

        x = int.from_bytes(x_bytes, byteorder="big")
        y = int.from_bytes(y_bytes, byteorder="big")

        point = ECC.EccPoint(x, y, curve="P-256")
        public_key = ECC.EccKey(curve="P-256", point=point)
        verifier = DSS.new(public_key, "fips-186-3")
        message_hash = SHA256.new(message)

        try:
            verifier.verify(message_hash, signature)
            return True
        except ValueError:
            return False

    def __init__(
        self,
        input: Optional[Path],
        output: Optional[Path],
        sk: Optional[Path],
        pwd: Optional[str],
        pk: List[Path],
        product: Optional[str],
        pk_hashes_out: Optional[Path],
        fip_sk: Optional[Path] = None,
    ):
        self.input = Path(input) if input else None
        self.output = output
        self.sk = sk
        self.pwd = pwd
        self.pk = pk
        self.product = product
        self.pk_hashes_out = pk_hashes_out
        self.fip_sk = fip_sk

        self.pkh = []
        self.pkhth = None

        self.firmware = None
        self.signed = False

        if input:
            self.load_image()

    def load_image(self):
        """Load the binary image to avoid file changing during signing."""
        if not self.input or not self.input.exists():
            logger.error(f"Input file {self.input} does not exist")
            raise FileNotFoundError(f"Input file {self.input} does not exist")
        with open(self.input, "rb") as f:
            self.firmware = f.read()
            assert self.firmware is not None, "Firmware must be loaded before signing"
        logger.info(f"Loaded {len(self.firmware)} bytes from {self.input}")

    def save_image(self):
        """Save the signed binary image."""
        if not self.output or not self.firmware:
            logger.error("Output path is not set or firmware is not loaded")
            raise ValueError("Output path is not set or firmware is not loaded")
        if not self.signed:
            logger.warning("Saving unsigned image")
        with open(self.output, "wb") as f:
            f.write(self.firmware)
        logger.info(f"Saved {len(self.firmware)} bytes to {self.output}")

    def _read_header(self) -> STM32Header:
        """Read and parse the STM32 header from the binary file."""
        logger.debug("Reading header from firmware")
        if self.firmware is None:
            self.load_image()
            assert self.firmware is not None, "Firmware must be loaded before reading header"

        header_data = self.firmware[:HEADER_TOTAL_LEN]
        header = STM32Header.unpack(header_data)

        if header.pack() != header_data:
            logger.error("Header unpacked data does not match original data")
            raise ValueError("Header unpacked data does not match original data")

        return header

    def _write_header(self, header: STM32Header):
        """Update our stored STM32 header with a new one."""
        logger.debug("Writing header to firmware")
        if self.firmware is None:
            self.load_image()
        assert self.firmware is not None, "Firmware must be loaded before writing header"

        header_data = header.pack()
        self.firmware = header_data + self.firmware[HEADER_TOTAL_LEN:]

    def _validate_header(self, header: STM32Header):
        """Validate the header fields before signing the image."""
        logger.debug("Validating header")
        if not self.product:
            logger.error("Product identifier is not set")
            raise ValueError("Product identifier is not set")
        if not self.firmware:
            logger.error("Firmware is not loaded")
            raise ValueError("Firmware is not loaded")

        # Validate base header fields
        if header.base_header.magic != HEADER_BASE_MAGIC:
            raise ValueError(f"Invalid header magic: {header.base_header.magic.hex()} != {HEADER_BASE_MAGIC.hex()}")

        if header.base_header.header_version != int.from_bytes(HEADER_VERSION):
            raise ValueError(
                f"Invalid header version: {header.base_header.header_version} != {int.from_bytes(HEADER_VERSION)}"
            )

        expected_length = header.base_header.image_length + HEADER_TOTAL_LEN
        if len(self.firmware) != expected_length:
            raise ValueError(f"Invalid firmware length: {len(self.firmware)} != {expected_length}")

        if header.base_header.version_number != IMAGE_VERSION_NUMBER[self.product]:
            # This is not an error if we actually want to change the version number. This will increment the anti-rollback value in OTP
            # the raise ValueError can be commented out if we want to proceed
            logger.warning(
                f"Image version number mismatch: {header.base_header.version_number} != {IMAGE_VERSION_NUMBER[self.product]}"
            )
            raise ValueError(
                f"Image version number mismatch: {header.base_header.version_number} != {IMAGE_VERSION_NUMBER[self.product]}"
            )
        logger.info(f"Image version number: {IMAGE_VERSION_NUMBER[self.product]}")

        if header.base_header.option_flags != AUTH_OPTION_FLAGS:
            raise ValueError(
                f"Invalid option flags: 0x{header.base_header.option_flags:08X} != 0x{AUTH_OPTION_FLAGS:08X}"
            )

        if header.base_header.header_extensions_len != HEADER_EXT_TOTAL_LEN:
            raise ValueError(
                f"Invalid header extensions length: {header.base_header.header_extensions_len} != {HEADER_EXT_TOTAL_LEN}"
            )

        # Validate authentication header fields
        if header.auth_header:
            if header.auth_header.extension_type != HEADER_EXT_AUTH:
                raise ValueError(
                    f"Invalid auth extension type: {header.auth_header.extension_type.hex()} != {HEADER_EXT_AUTH.hex()}"
                )

            if header.auth_header.extension_length != HEADER_AUTH_LEN:
                raise ValueError(
                    f"Invalid auth extension length: {header.auth_header.extension_length} != {HEADER_AUTH_LEN}"
                )

            pk_index = self.get_public_signing_key_idx()
            if header.auth_header.public_key_index != pk_index:
                raise ValueError(f"Invalid public key index: {header.auth_header.public_key_index} != {pk_index}")

            if header.auth_header.public_key_index != SIGNING_KEY_INDEX[self.product]:
                # This is not an error if we actually want to change the key index. This will revoke all lower key indices in OTP
                # the raise ValueError can be commented out if we want to proceed
                logger.warning(
                    f"Signing key index mismatch: {header.auth_header.public_key_index} != {SIGNING_KEY_INDEX[self.product]}"
                )
                raise ValueError(
                    f"Signing key index mismatch: {header.auth_header.public_key_index} != {SIGNING_KEY_INDEX[self.product]}"
                )
            logger.info(f"Signing key index: {SIGNING_KEY_INDEX[self.product]}")

            if header.auth_header.public_key_number != PUBKEY_COUNT:
                raise ValueError(f"Invalid public key number: {header.auth_header.public_key_number} != {PUBKEY_COUNT}")

            if header.auth_header.ecdsa_algorithm != ECDSA_ALGO:
                raise ValueError(f"Invalid ECDSA algorithm: {header.auth_header.ecdsa_algorithm} != {ECDSA_ALGO}")

            # Validate public key matches
            pk = self.get_ecc_key_from_file(self.pk[pk_index])
            expected_pubkey = self.get_raw_pubkey(pk)
            if header.auth_header.ecdsa_public_key != expected_pubkey:
                raise ValueError("Public key in header does not match provided key")

            if len(header.auth_header.public_key_hashes) != PUBKEY_COUNT:
                raise ValueError(
                    f"Invalid number of public key hashes: {len(header.auth_header.public_key_hashes)} != {PUBKEY_COUNT}"
                )

        # Validate padding header fields
        if header.padding_header.extension_type != HEADER_EXT_PADDING:
            raise ValueError(
                f"Invalid padding extension type: {header.padding_header.extension_type.hex()} != {HEADER_EXT_PADDING.hex()}"
            )

        if header.padding_header.extension_length != HEADER_PADDING_LEN:
            raise ValueError(
                f"Invalid padding extension length: {header.padding_header.extension_length} != {HEADER_PADDING_LEN}"
            )

        # Additional checks
        if len(header.pack()) != HEADER_TOTAL_LEN:
            raise ValueError(f"Invalid total header length: {len(header.pack())} != {HEADER_TOTAL_LEN}")

        if header.base_header.image_length % IMAGE_ALIGNMENT != 0:
            raise ValueError(f"Image length {header.base_header.image_length} is not {IMAGE_ALIGNMENT}-byte aligned")

        logger.info("Header validation successful")

    def gen_signing_header(self, pk_index: int, header: STM32Header) -> None:
        """Generate the signing header."""
        logger.info(f"Generating authentication header with key index {pk_index}")

        if not self.product or not self.firmware:
            logger.error("Product identifier or firmware is not set")
            raise ValueError("Product identifier or firmware is not set")

        pk_index = self.get_public_signing_key_idx()
        public_key = self.get_ecc_key_from_file(self.pk[pk_index])
        public_key_raw = self.get_raw_pubkey(public_key)
        logger.debug(f"Using public key: {public_key_raw.hex()}")

        # Generate pkh and pkht
        self.gen_pkht()

        auth_header = STM32AuthHeader(
            extension_type=HEADER_EXT_AUTH,
            extension_length=HEADER_AUTH_LEN,
            public_key_index=pk_index,
            public_key_number=PUBKEY_COUNT,
            ecdsa_algorithm=ECDSA_ALGO,
            ecdsa_public_key=public_key_raw,
            public_key_hashes=self.pkh,
        )

        # Padding optionally carries a proto-signer prefix (magic + FIP pk_idx)
        # so TF-A can pick the canonical ROTPK out of the auth pubkey-hash
        # table without introducing a new extension type that the BootROM
        # would reject. Omit the prefix when no FIP key was provided so the
        # image remains byte-identical to stock ST signing.
        fip_pk_idx = self.get_fip_signing_key_idx()
        prefix_len = HEADER_PADDING_PROTO_PREFIX_LEN if fip_pk_idx is not None else 0
        padding_header = STM32PaddingHeader(
            extension_type=HEADER_EXT_PADDING,
            extension_length=HEADER_PADDING_LEN,
            padding=os.urandom(HEADER_PADDING_LEN - PADDING_METADATA_LEN - prefix_len),
            public_key_index=fip_pk_idx,
        )

        header.auth_header = auth_header
        header.padding_header = padding_header

        # Update base header fields
        header.base_header.option_flags = AUTH_OPTION_FLAGS
        header.base_header.version_number = IMAGE_VERSION_NUMBER[self.product]
        header.base_header.header_extensions_len = HEADER_EXT_TOTAL_LEN

        # Image length must be 32-byte aligned. Pad with 0s if not.
        if header.base_header.image_length % IMAGE_ALIGNMENT != 0:
            padding_len = IMAGE_ALIGNMENT - (header.base_header.image_length % IMAGE_ALIGNMENT)
            logger.debug(f"Padding {padding_len} bytes to image length")
            self.firmware += bytes(padding_len)
            header.base_header.image_length += padding_len

        self._validate_header(header)
        self._write_header(header)
        logger.info("Authentication header generated successfully")

    def pre_signing_operations(self):
        """Update header fields before signing."""
        logger.info("Starting pre-signing operations")

        header = self._read_header()

        self.gen_signing_header(self.get_public_signing_key_idx(), header)

        logger.info("Pre-signing operations completed")

    @staticmethod
    def _key_idx_from_filename(key_path: Path, pattern: re.Pattern) -> int:
        """Extract the key index from a privateKey0X.pem or publicKey0X.pem filename."""
        m = pattern.match(Path(key_path).name)
        if not m:
            raise ValueError(f"Key file must match pattern: {pattern.pattern}")
        return int(m.group(1))

    def get_public_signing_key_idx(self) -> int:
        """Public key index for the FSBL signing key (self.sk)."""
        if not self.sk:
            logger.error("Signing key is not set")
            raise ValueError("Signing key is not set")
        idx = self._key_idx_from_filename(self.sk, PRIVATE_KEY_PATTERN)
        logger.debug(f"Extracted FSBL signing key index: {idx}")
        return idx

    def get_fip_signing_key_idx(self) -> Optional[int]:
        """Public key index for the FIP signing key (self.fip_sk), if provided."""
        if not self.fip_sk:
            return None
        idx = self._key_idx_from_filename(self.fip_sk, PRIVATE_KEY_PATTERN)
        logger.debug(f"Extracted FIP signing key index: {idx}")
        return idx

    def post_signing_operations(self) -> None:
        """Final sanity checks after signing."""
        pass

    def sign_image(self, signature: bytes) -> None:
        """Sign the image in memory"""
        logger.info("Writing signature to image")
        header = self._read_header()
        header.base_header.image_signature = signature
        self._write_header(header)
        self.signed = True
        logger.info("Image signed successfully")

    def compute_signature(self) -> bytes:
        """Compute the ECDSA signature for the binary.
        Signature is calculated from first byte of header version field to last byte of image given by image length field.

        This happens last after all other header fields are set.
        """
        logger.info("Computing signature")
        header = self._read_header()

        if not self.firmware or not header.auth_header:
            logger.error("Firmware is not loaded or authentication header is missing")
            raise ValueError("Firmware is not loaded or authentication header is missing")
        if not self.sk:
            logger.error("Signing key is not set")
            raise ValueError("Signing key is not set")

        data_to_sign = self.firmware[HEADER_SIGNING_OFFSET:]

        if len(self.firmware) != header.base_header.image_length + HEADER_TOTAL_LEN:
            logger.error(
                f"Firmware length: {len(self.firmware)} does not match header image length {header.base_header.image_length + HEADER_TOTAL_LEN}"
            )
            raise ValueError(
                f"Firmware length {len(self.firmware)} does not match header image length {header.base_header.image_length + HEADER_TOTAL_LEN}"
            )

        sha256_hash = SHA256.new(data=data_to_sign)

        # Load the signing key
        signing_key = self.get_ecc_key_from_file(self.sk, self.pwd)

        # Sign the hash
        signer = DSS.new(signing_key, mode="fips-186-3")
        signature = signer.sign(sha256_hash)

        # Verify the signature
        public_signing_key_raw = header.auth_header.ecdsa_public_key
        if self.verify_signature_with_raw_pubkey(public_signing_key_raw, signature, data_to_sign):
            logger.info("Signature verified successfully")
        else:
            logger.error("Signature verification failed")
            raise ValueError("Signature verification failed")

        return signature

    def gen_pkht(self) -> None:
        """Generate the Public Key Hash Table (PKHT) and Public Key Hashes Table Hash (PKHTH) from public key input."""
        logger.info("Generating public key hash table")
        self.pkh = []

        if len(self.pk) > PUBKEY_COUNT:
            logger.error(f"Expected at most {PUBKEY_COUNT} public keys, got {len(self.pk)}")
            raise ValueError(f"Expected at most {PUBKEY_COUNT} public keys, got {len(self.pk)}")

        logger.debug(f"Processing {len(self.pk)} public keys")

        for pk_file in self.pk:
            key = self.get_ecc_key_from_file(pk_file)
            pubkey_hash = self.hash_pubkey_with_ecc_alg(key, ECDSA_ALGO)
            self.pkh.append(pubkey_hash.digest())

        # If we have less than 8 public keys, extend the last one to fill the table
        if len(self.pkh) < PUBKEY_COUNT:
            last_hash = self.pkh[-1]
            logger.debug(f"Extending last hash {last_hash.hex()} to fill the table")
            for _ in range(PUBKEY_COUNT - len(self.pkh)):
                self.pkh.append(last_hash)

        pkht = b"".join(self.pkh)
        self.pkhth = SHA256.new(data=pkht).digest()
        logger.info("Public key hash table generated successfully")

    def gen_hash_files(self):
        """Generate the public key hash files."""

        if not self.pk_hashes_out:
            logger.error("Public key hashes output directory is not set")
            raise ValueError("Public key hashes output directory is not set")
        if not self.pkh or not self.pkhth:
            self.gen_pkht()
            self.gen_hash_files()
            assert self.pkh and self.pkhth, "Public key hashes and hash table must be generated before saving"

        for i, pkh in enumerate(self.pkh):
            pkh_file = self.pk_hashes_out / PKH_NAME.format(i)
            logger.info(f"Writing public key hashes to {pkh_file}")
            with open(pkh_file, "wb") as f:
                f.write(pkh)
            logger.debug(f"Wrote hash {i} to {pkh_file}")

        pkhth_file = self.pk_hashes_out / PKHTH_NAME
        logger.info(f"Writing public key hashes hash to {pkhth_file}")
        with open(pkhth_file, "wb") as f:
            f.write(self.pkhth)


class OrderedGroup(click.Group):
    def __init__(self, name=None, commands=None, **attrs):
        super(OrderedGroup, self).__init__(name, commands, **attrs)
        # Commands will be shown in this order
        self.cmd_order = ["sign", "verify", "gen-pk-hashes", "dump"]

    def list_commands(self, ctx):
        """Return commands in specified order"""
        return self.cmd_order


@click.group(cls=OrderedGroup)
@click.option("--verbose", "-v", is_flag=True, default=False, help="Enable verbose logs")
def cli(verbose):
    """CLI for FSBL Signer for STM32MP1 series images"""
    if verbose:
        logger.setLevel(logging.DEBUG)
    else:
        logger.setLevel(logging.INFO)


# fmt: off
@cli.command()
@click.option("--input", "-i", type=click.Path(exists=True), required=True, help="Path to FSBL image")
@click.option("--output", "-o", type=click.Path(), required=True, help="Path to output signed FSBL image")
@click.option("--sk", type=click.Path(exists=True, readable=True), required=True, help="Path to signing key PEM")
@click.option("--pwd", "-p", type=click.STRING, default=None, hide_input=True, prompt_required=False,
              help="Password for signing key (optional if key is unencrypted)")
@click.option("--pk", multiple=True, type=click.Path(exists=True, readable=True), required=True,
              help="Paths to public keys PEM (1-8). Order is important. Use multiple --pk options to specify multiple keys.")
@click.option("--product", type=click.Choice(sorted(PRODUCTS)), required=True,
              help=f"Product identifier ({', '.join(sorted(PRODUCTS))})")
@click.option("--pk-hashes-out", type=click.Path(path_type=Path, exists=True),
              help=f"Directory for hash file output (will create {PKH_NAME} files and {PKHTH_NAME})")
@click.option("--fip-sk", type=click.Path(exists=False, readable=True, path_type=Path),
              help="Path to the FIP signing key PEM (privateKey0X.pem).")
def sign(input: Path, output: Path, sk: Path, pwd: Optional[str], pk: List[Path], product: str,
         pk_hashes_out: Optional[Path], fip_sk: Optional[Path]):
# fmt: on
    """Sign the FSBL image with the provided keys. Assumes an existing header."""
    # Normalize empty string to None for consistent passwordless handling
    if pwd == "":
        pwd = None

    logger.info(f"Signing {input} -> {output}")
    logger.info(f"Using signing key: {sk}")
    logger.info(f"Using {len(pk)} public keys")
    logger.info(f"Using product: {product}")
    if fip_sk:
        logger.info(f"Using FIP signing key: {fip_sk}")
    if pk_hashes_out:
        logger.info(f"Generated hashes will be saved to {pk_hashes_out}")

    signer = FSBLSigner(input, output, sk, pwd, pk, product, pk_hashes_out, fip_sk=fip_sk)

    # Parameter validation
    if len(pk) > PUBKEY_COUNT:
        logger.error(f"Expected at most {PUBKEY_COUNT} public keys, got {len(pk)}")
        sys.exit(1)

    signer.pre_signing_operations()
    signature = signer.compute_signature()
    signer.sign_image(signature)
    signer.save_image()
    if pk_hashes_out:
        signer.gen_hash_files()
    if signer.signed:
        logger.info("Signing completed successfully")
    else:
        logger.error("Signing failed")

# fmt: off
@cli.command()
@click.option("--input", "-i", type=click.Path(exists=True), required=True, help="Path to FSBL image")
@click.option("--pk", multiple=True, type=click.Path(exists=True, readable=True), required=True,
              help="Paths to public keys PEM (1-8). Order is important. Use multiple --pk options to specify multiple keys.")
@click.option("--product", type=click.Choice(sorted(PRODUCTS)), required=True,
              help=f"Product identifier ({', '.join(sorted(PRODUCTS))})")
@click.option("--pk-hashes-in", type=click.Path(path_type=Path, exists=True, readable=True),
              help=f"Input the {PKHTH_NAME} file path to optionally verify")
# fmt: on
def verify(input: Path, pk: List[Path], product: str, pk_hashes_in: Optional[Path]):
    """Verify the FSBL image signature with the provided keys."""
    logger.info(f"Verifying {input} signature")
    logger.info(f"Using product: {product}")

    signer = FSBLSigner(input, None, None, None, pk, product, None)
    header = signer._read_header()
    if not signer.firmware or not header.auth_header:
        logger.error("Firmware is not loaded or no authentication header found")
        sys.exit(1)

    signature = header.base_header.image_signature
    pk_index = header.auth_header.public_key_index
    header_public_key = header.auth_header.ecdsa_public_key
    header_pkh = header.auth_header.public_key_hashes

    if pk_index < 0 or pk_index >= len(pk):
        logger.error(f"Public key index {pk_index} is out of range for provided keys")
        sys.exit(1)

    # Verify the signature
    public_key = signer.get_ecc_key_from_file(pk[pk_index])
    raw_pubkey = signer.get_raw_pubkey(public_key)

    if header_public_key != raw_pubkey:
        logger.error(f"Public key in header does not match key at index {pk_index}")
        sys.exit(1)

    if signer.verify_signature_with_raw_pubkey(raw_pubkey, signature, signer.firmware[HEADER_SIGNING_OFFSET:]):
        logger.info(f"Signature verified successfully with public key {pk_index} ({pk[pk_index]})")
    else:
        logger.error(f"Signature verification failed with public key {pk_index}. Used {pk[pk_index]}")
        sys.exit(1)

    # Verify the public key hashes
    signer.gen_pkht()
    for i, pkh in enumerate(signer.pkh):
        if pkh != header_pkh[i]:
            logger.error(f"Public key hash {i} does not match header")
            sys.exit(1)
    logger.info("Public key hashes from public keys verified successfully")

    if pk_hashes_in:
        with open(pk_hashes_in, "rb") as f:
            pk_hashes_table = f.read()
        if pk_hashes_table != signer.pkhth:
            logger.error(f"Public key hashes table hash in {pk_hashes_in} does not match header")
            sys.exit(1)
        logger.info(f"Public key hashes table hash in {pk_hashes_in} verified successfully")


# fmt: off
@cli.command()
@click.option("--pk", multiple=True, type=click.Path(exists=True, readable=True), required=True,
              help="Paths to public keys PEM (1-8). Order is important. Use multiple --pk options to specify multiple keys.")
@click.option("--pk-hashes-out", type=click.Path(path_type=Path, exists=True), default=".",
              help=f"Directory for hash file output (will create {PKH_NAME} files and {PKHTH_NAME})")
# fmt: on
def gen_pk_hashes(pk: List[Path], pk_hashes_out: Path):
    """Generate public key hashes and table hash for the provided keys."""
    logger.info(f"Generating hashes for {len(pk)} public keys in {pk_hashes_out}")

    signer = FSBLSigner(input=None, output=None, sk=None, pwd=None, pk=pk, product=None, pk_hashes_out=pk_hashes_out)
    signer.gen_hash_files()
    logger.info("Hash generation completed successfully")


@cli.command()
@click.option("--input", "-i", type=click.Path(exists=True), required=True, help="Path to FSBL image")
def dump(input: Path):
    """Dump STM32 FSBL header contents as plaintext."""
    with open(input, "rb") as f:
        header_data = f.read(HEADER_TOTAL_LEN)
    header = STM32Header.unpack(header_data)

    # Base Header
    click.echo("=== Base Header ===")
    click.echo(f"Magic:                {header.base_header.magic.hex().upper()}")
    click.echo(f"Image Signature:      {header.base_header.image_signature.hex().upper()}")
    click.echo(f"Image Checksum:       0x{header.base_header.image_checksum:08X}")
    click.echo(f"Header Version:       0x{header.base_header.header_version:08X}")
    click.echo(f"Image Length:         {header.base_header.image_length} (0x{header.base_header.image_length:08X})")
    click.echo(f"Image Entry Point:    0x{header.base_header.image_entry_point:08X}")
    click.echo(f"Reserved:            {header.base_header.reserved.hex().upper()}")
    click.echo(f"Version Number:       {header.base_header.version_number} (0x{header.base_header.version_number:08X})")
    click.echo(f"Option Flags:         0x{header.base_header.option_flags:08X}")
    click.echo(
        f"Header Ext Length:    {header.base_header.header_extensions_len} (0x{header.base_header.header_extensions_len:08X})"
    )
    click.echo(f"Padding:             {header.base_header.padding.hex().upper()}")

    # Auth Header
    if header.auth_header:
        click.echo("\n=== Authentication Header ===")
        click.echo(f"Extension Type:       {header.auth_header.extension_type.hex().upper()}")
        click.echo(f"Extension Length:     {header.auth_header.extension_length}")
        click.echo(f"Public Key Index:     {header.auth_header.public_key_index}")
        click.echo(f"Public Key Number:    {header.auth_header.public_key_number}")
        click.echo(f"ECDSA Algorithm:      {header.auth_header.ecdsa_algorithm}")
        click.echo(f"ECDSA Public Key:    {header.auth_header.ecdsa_public_key.hex().upper()}")

        click.echo("\nPublic Key Hashes:")
        for i, pkh in enumerate(header.auth_header.public_key_hashes):
            click.echo(f"  {i + 1}: {pkh.hex().upper()}")

    # Padding Header
    click.echo("\n=== Padding Header ===")
    click.echo(f"Extension Type:       {header.padding_header.extension_type.hex().upper()}")
    click.echo(f"Extension Length:     {header.padding_header.extension_length}")
    if header.padding_header.public_key_index is not None:
        click.echo(f"Proto PK Index:       {header.padding_header.public_key_index}")
    click.echo(f"Padding:             {header.padding_header.padding.hex().upper()}")


if __name__ == "__main__":
    cli()

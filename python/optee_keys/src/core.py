import hashlib
import logging
import shutil
import struct
from pathlib import Path
from typing import Tuple

from Crypto.PublicKey import RSA
from cryptography.exceptions import InvalidSignature
from cryptography.hazmat.backends import default_backend
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import padding, rsa
from elftools.elf.elffile import ELFFile
from elftools.elf.sections import SymbolTableSection

from .types import (
    SHDR_MAGIC,
    Algorithm,
    TAHeader,
    TAInfo,
)

logger = logging.getLogger(__name__)

MIN_EXPONENT_SIZE_BYTES = 4

# Additional OP-TEE constants
SHDR_BOOTSTRAP_TA = 0x1  # TA flag for bootstrap TA


def find_symbol_offset(elffile: ELFFile, symbol_name: str) -> Tuple[int, int]:
    """Find the file offset and size of a symbol in an ELF file.

    Args:
        elffile: An ELFFile object representing the ELF file
        symbol_name: Name of the symbol to locate

    Returns:
        Tuple containing (file_offset, symbol_size)

    Raises:
        RuntimeError: If symbol table is not found or symbol is not found
    """
    # Find the symbol table section
    symtab = next((s for s in elffile.iter_sections() if isinstance(s, SymbolTableSection)), None)
    if symtab is None:
        raise RuntimeError("No symbol table found")

    # Search for the symbol and calculate its file offset
    for symbol in symtab.iter_symbols():
        if symbol.name == symbol_name:
            sym_addr = symbol["st_value"]
            sym_size = symbol["st_size"]

            # Find the section that contains the symbol address
            for section in elffile.iter_sections():
                sec_start = section["sh_addr"]
                sec_end = sec_start + section["sh_size"]

                # Check if the symbol address is within the section
                if sec_start <= sym_addr < sec_end:
                    # Calculate the file offset based on the section's offset and symbol address
                    file_offset = section["sh_offset"] + (sym_addr - sec_start)
                    return file_offset, sym_size

    raise RuntimeError(f"Symbol {symbol_name} not found")


def read_key_from_elf(path: Path) -> Tuple[int, bytes]:
    """Read the RSA public key components from an ELF file.

    Args:
        path: Path to the ELF file.

    Returns:
        Tuple containing (exponent, modulus) where exponent is an integer
        and modulus is bytes.

    Raises:
        RuntimeError: If required symbols are not found in the ELF file.
    """
    with open(path, "rb") as f:
        elf = ELFFile(f)
        # Get offsets for modulus and exponent
        mod_off, mod_size = find_symbol_offset(elf, "ta_pub_key_modulus")
        exp_off, exp_size = find_symbol_offset(elf, "ta_pub_key_exponent")

        # Read modulus and exponent from file
        f.seek(mod_off)
        modulus = f.read(mod_size)
        f.seek(exp_off)
        exponent = f.read(exp_size)

        # Convert exponent bytes to integer (little-endian)
        exponent = struct.unpack("<I", exponent[:4])[0]
        return exponent, modulus


def read_key_from_pem(pem_path: Path) -> Tuple[int, bytes]:
    """Read the RSA public key components from a PEM file.

    Args:
        pem_path: Path to the PEM public key file.

    Returns:
        Tuple containing (exponent, modulus) where exponent is an integer
        and modulus is bytes.
    """
    with open(pem_path, "rb") as f:
        key = RSA.import_key(f.read())

    # Extract exponent and modulus
    modulus_length = (key.n.bit_length() + 7) // 8
    modulus_bytes = key.n.to_bytes(modulus_length, byteorder="big")
    return key.e, modulus_bytes


def print_key_do(elf_path: Path) -> Tuple[str, str]:
    """Print the public key components from an ELF file.

    Args:
        elf_path: Path to the ELF file.

    Returns:
        Tuple containing (hex_exponent, hex_modulus).
    """
    exponent, modulus = read_key_from_elf(elf_path)
    return hex(exponent), modulus.hex()


def compare_key_do(elf_path: Path, pem_path: Path) -> bool:
    """Compare embedded ELF key with a PEM public key.

    Args:
        elf_path: Path to the ELF file.
        pem_path: Path to the PEM file.

    Returns:
        True if keys match, False otherwise.
    """
    exp_elf, mod_elf = read_key_from_elf(elf_path)
    exp_pem, mod_pem = read_key_from_pem(pem_path)
    return exp_elf == exp_pem and mod_elf == mod_pem


def inject_key_do(elf_path: Path, pem_path: Path, output_path: Path) -> None:
    """Inject new public key from PEM into tee.elf and write to output ELF.

    Args:
        elf_path: Path to input ELF file.
        pem_path: Path to PEM file containing new public key.
        output_path: Path to write modified ELF file.

    Creates a new ELF file with the public key from the PEM file replacing
    the existing key in the input ELF file.
    """
    # Create a copy of the original ELF file
    shutil.copyfile(elf_path, output_path)

    with open(output_path, "rb+") as f:
        elf = ELFFile(f)
        # Read the new key from PEM file
        exp_new, mod_new = read_key_from_pem(pem_path)

        # Get offsets for key components in ELF
        mod_off, mod_size = find_symbol_offset(elf, "ta_pub_key_modulus")
        exp_off, exp_size = find_symbol_offset(elf, "ta_pub_key_exponent")

        # Validate key sizes
        if len(mod_new) != mod_size:
            raise ValueError("Modulus size mismatch")
        if exp_size < MIN_EXPONENT_SIZE_BYTES:
            raise ValueError("Exponent area too small")

        # Convert exponent to little-endian bytes
        exp_bytes = struct.pack("<I", exp_new)

        # Write new key components
        f.seek(mod_off)
        f.write(mod_new)

        f.seek(exp_off)
        f.write(exp_bytes.ljust(exp_size, b"\x00"))  # Pad exponent with zeros if needed


def parse_ta_header(ta_path: Path) -> TAHeader:
    """Parse the header of an OP-TEE TA file.

    Args:
        ta_path: Path to the TA file.

    Returns:
        TAHeader dataclass containing parsed header information.

    Raises:
        ValueError: If the TA file format is invalid.
    """
    with open(ta_path, "rb") as f:
        # Read the signed header structure
        # struct shdr {
        #     uint32_t magic;
        #     uint32_t img_type;
        #     uint32_t img_size;
        #     uint32_t algo;
        #     uint16_t hash_size;
        #     uint16_t sig_size;
        # }
        header_data = f.read(20)  # Size of shdr structure
        if len(header_data) < 20:
            raise ValueError("TA file too small to contain valid header")

        magic, img_type, img_size, algo, hash_size, sig_size = struct.unpack("<IIIIHH", header_data)

        if magic != SHDR_MAGIC:
            raise ValueError(f"Invalid TA magic number: {hex(magic)}")

        # Read hash and signature
        hash_data = f.read(hash_size)
        sig_data = f.read(sig_size)

        # UUID and version come AFTER the signature (per OP-TEE format)
        ta_uuid = f.read(16)  # UUID is 16 bytes
        ta_version = f.read(4)  # Version is 4 bytes (uint32_t)

        # The actual TA image starts after UUID and version
        img_data = f.read(img_size)

        if len(img_data) != img_size:
            raise ValueError("TA image size doesn't match header")

        # Also save the full header for verification
        f.seek(0)
        shdr = f.read(20)

        return TAHeader(
            magic=magic,
            img_type=img_type,
            img_size=img_size,
            algo=algo,
            hash_size=hash_size,
            sig_size=sig_size,
            hash=hash_data,
            signature=sig_data,
            ta_uuid=ta_uuid,
            ta_version=ta_version,
            image=img_data,
            shdr=shdr,
        )


def get_ta_info(ta_path: Path) -> TAInfo:
    """Get detailed information about a TA file.

    Args:
        ta_path: Path to the TA file.

    Returns:
        TAInfo dataclass containing TA information.
    """
    try:
        header = parse_ta_header(ta_path)

        # Calculate hash of the image for display
        img_hash = hashlib.sha256(header.image).hexdigest()

        # Get algorithm name using the Algorithm class
        algo_name = Algorithm.name(header.algo)

        # Determine image type
        img_type_str = "Bootstrap TA" if header.img_type & SHDR_BOOTSTRAP_TA else "Regular TA"

        return TAInfo(
            valid=True,
            img_type=img_type_str,
            img_size=header.img_size,
            algorithm=algo_name,
            hash_size=header.hash_size,
            sig_size=header.sig_size,
            img_hash=img_hash,
            stored_hash=header.hash.hex(),
            signature=header.signature.hex()[:64] + "...",  # First 32 bytes for display
            uuid=header.uuid_str,
            error=None,
        )
    except Exception as e:
        return TAInfo(
            valid=False,
            error=str(e),
        )


def verify_ta_signature(ta_path: Path, optee_elf_path: Path) -> bool:
    """Verify a TA's signature using the public key from OP-TEE ELF.

    Based on OP-TEE's sign_encrypt.py, the digest is calculated over:
    - shdr (signed header)
    - ta_uuid (if present)
    - ta_version (if present)
    - img (the actual TA binary)

    Args:
        ta_path: Path to the TA file to verify.
        optee_elf_path: Path to the OP-TEE ELF containing the public key.

    Returns:
        True if signature is valid, False otherwise.
    """
    try:
        # Parse the TA file
        header = parse_ta_header(ta_path)

        # Read the public key from OP-TEE ELF
        exponent, modulus = read_key_from_elf(optee_elf_path)

        # Reconstruct RSA public key
        n = int.from_bytes(modulus, byteorder="big")
        public_numbers = rsa.RSAPublicNumbers(e=exponent, n=n)
        key = public_numbers.public_key(backend=default_backend())

        # Determine padding based on algorithm
        if header.algo == Algorithm.RSA_PSS_SHA256:
            # RSA-PSS padding
            # OP-TEE uses digest length (32 bytes for SHA-256) as the standard salt length
            pad = padding.PSS(
                mgf=padding.MGF1(hashes.SHA256()),
                salt_length=padding.PSS.DIGEST_LENGTH,
            )
        else:  # Default to PKCS#1 v1.5
            pad = padding.PKCS1v15()

        # Build the data that was signed according to OP-TEE format
        # The signature is over: shdr || ta_uuid || ta_version || img
        data_to_verify = header.shdr + header.ta_uuid + header.ta_version + header.image

        # Verify the hash matches what's stored
        calculated_hash = hashlib.sha256(data_to_verify).digest()
        if calculated_hash != header.hash:
            return False

        # Verify the signature
        try:
            key.verify(header.signature, data_to_verify, pad, hashes.SHA256())
            return True
        except InvalidSignature:
            logger.debug(f"Signature verification failed for algo {header.algo}")
            # For RSA-PSS, some implementations may use MAX_LENGTH instead of DIGEST_LENGTH
            # Try MAX_LENGTH as a fallback for compatibility
            if header.algo == Algorithm.RSA_PSS_SHA256:
                try:
                    alt_pad = padding.PSS(mgf=padding.MGF1(hashes.SHA256()), salt_length=padding.PSS.MAX_LENGTH)
                    key.verify(header.signature, data_to_verify, alt_pad, hashes.SHA256())
                    logger.debug("Signature verification succeeded with salt_length=MAX_LENGTH")
                    return True
                except InvalidSignature:
                    logger.debug("PSS verification failed with MAX_LENGTH salt")
            return False

    except Exception as e:
        logger.error(f"Error during TA verification: {e}")
        return False


def verify_ta_with_pem(ta_path: Path, pem_path: Path) -> bool:
    """Verify a TA's signature using a PEM key file.

    Based on OP-TEE's sign_encrypt.py, the digest is calculated over:
    - shdr (signed header)
    - ta_uuid (if present)
    - ta_version (if present)
    - img (the actual TA binary)

    Args:
        ta_path: Path to the TA file to verify.
        pem_path: Path to the PEM key file (can be public or private key).

    Returns:
        True if signature is valid, False otherwise.
    """
    try:
        # Parse the TA file
        header = parse_ta_header(ta_path)

        # Read the key from PEM file (handle both public and private keys)
        with open(pem_path, "rb") as f:
            key_data = f.read()

        # Try loading as public key first
        key = None
        try:
            key = serialization.load_pem_public_key(key_data, backend=default_backend())
        except Exception:
            # Try loading as private key and extract public part
            try:
                private_key = serialization.load_pem_private_key(key_data, password=None, backend=default_backend())
                key = private_key.public_key()
            except Exception as e:
                raise ValueError(f"Could not load key from {pem_path}: {e}")

        if not isinstance(key, rsa.RSAPublicKey):
            raise ValueError("Key is not an RSA key")

        # Determine padding based on algorithm
        if header.algo == Algorithm.RSA_PSS_SHA256:
            # RSA-PSS padding
            # OP-TEE uses digest length (32 bytes for SHA-256) as the standard salt length
            pad = padding.PSS(
                mgf=padding.MGF1(hashes.SHA256()),
                salt_length=padding.PSS.DIGEST_LENGTH,
            )
        else:  # Default to PKCS#1 v1.5
            pad = padding.PKCS1v15()

        # Build the data that was signed according to OP-TEE format
        # The signature is over: shdr || ta_uuid || ta_version || img
        data_to_verify = header.shdr + header.ta_uuid + header.ta_version + header.image

        # Verify the hash matches what's stored
        calculated_hash = hashlib.sha256(data_to_verify).digest()
        if calculated_hash != header.hash:
            return False

        # Verify the signature
        try:
            key.verify(header.signature, data_to_verify, pad, hashes.SHA256())
            return True
        except InvalidSignature:
            logger.debug(f"Signature verification failed for algo {header.algo}")
            # For RSA-PSS, some implementations may use MAX_LENGTH instead of DIGEST_LENGTH
            # Try MAX_LENGTH as a fallback for compatibility
            if header.algo == Algorithm.RSA_PSS_SHA256:
                try:
                    alt_pad = padding.PSS(mgf=padding.MGF1(hashes.SHA256()), salt_length=padding.PSS.MAX_LENGTH)
                    key.verify(header.signature, data_to_verify, alt_pad, hashes.SHA256())
                    logger.debug("Signature verification succeeded with salt_length=MAX_LENGTH")
                    return True
                except InvalidSignature:
                    logger.debug("PSS verification failed with MAX_LENGTH salt")
            return False

    except Exception as e:
        logger.error(f"Error during TA verification: {e}")
        return False

import base64
import logging
import tempfile
from pathlib import Path
from typing import Dict

import click
from asn1crypto import x509 as asn1_x509
from cryptography import x509
from cryptography.hazmat.backends import default_backend
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.hazmat.primitives.asymmetric.utils import decode_dss_signature, encode_dss_signature
from pyasn1.codec.der import decoder, encoder
from pyasn1.codec.der.decoder import decode
from pyasn1.type.univ import BitString, Integer, Null, ObjectIdentifier, OctetString, Sequence
from pyasn1_modules import rfc2459

from ..constants import (
    CERT_MAPPING,
    EC_PUBLIC_KEY_OID,
    NON_TRUSTED_NV_COUNTER,
    OID_TO_EXTENSION_TYPE,
    OID_TO_STRING,
    PRIME256V1_CURVE_OID,
    TRUSTED_NV_COUNTER,
    Environment,
    ExtensionType,
    Product,
    SecurityWorld,
    SigningProvider,
)
from .auth import check_auth

logger = logging.getLogger(__name__)


class ChainOfTrustCertManager:
    @staticmethod
    def der_to_raw_signature(der_signature: bytes) -> bytes:
        """Convert ASN.1 DER encoded signature to raw 64-byte signature.

        Args:
            der_signature: ASN.1 DER encoded signature bytes
        Returns:
            bytes: Raw 64-byte signature (32 bytes r + 32 bytes s)
        """
        # Parse the DER signature to get r and s values
        r, s = decode_dss_signature(der_signature)
        r_bytes = r.to_bytes(32, "big")
        s_bytes = s.to_bytes(32, "big")
        return r_bytes + s_bytes

    @staticmethod
    def raw_to_der_signature(raw_signature: bytes) -> bytes:
        """Convert raw 64-byte signature to ASN.1 DER format.

        Args:
            raw_signature: Raw 64-byte signature (32 bytes r + 32 bytes s)
        Returns:
            bytes: ASN.1 DER encoded signature
        Raises:
            ValueError: If raw_signature is unexpected size
        """
        if len(raw_signature) != 64:
            raise ValueError("Raw signature must be exactly 64 bytes")
        r_bytes = raw_signature[:32]
        s_bytes = raw_signature[32:]
        r = int.from_bytes(r_bytes, "big")
        s = int.from_bytes(s_bytes, "big")
        # Encode to DER format
        return encode_dss_signature(r, s)

    @staticmethod
    def get_file_hash(file_path: Path) -> bytes:
        """Calculate the SHA-256 hash of a file.

        Args:
            file_path: Path to the file to hash

        Returns:
            bytes: The 32-byte SHA-256 hash of the file
        """
        sha256_hash = hashes.Hash(hashes.SHA256())
        with open(file_path, "rb") as f:
            for byte_block in iter(lambda: f.read(4096), b""):
                sha256_hash.update(byte_block)
        return sha256_hash.finalize()

    @staticmethod
    def read_certificate(cert_path: Path) -> x509.Certificate:
        """Read and parse an X.509 certificate from a DER-encoded file.

        Args:
            cert_path: Path to the DER-encoded certificate file

        Returns:
            x509.Certificate: The parsed certificate object

        Raises:
            Exception: If certificate cannot be read or parsed
        """
        try:
            with open(cert_path, "rb") as cert_file:
                cert_data = cert_file.read()
                cert = x509.load_der_x509_certificate(cert_data, default_backend())
                logger.info(f"Successfully read certificate from {cert_path}")
                return cert
        except Exception as e:
            logger.error(f"Failed to read certificate from {cert_path}: {str(e)}")
            raise

    @staticmethod
    def write_certificate(cert: x509.Certificate, cert_path: Path):
        """Write an X.509 certificate to a file in DER format.

        Args:
            cert: The certificate object to write
            cert_path: Path where the certificate should be written

        Raises:
            Exception: If certificate cannot be serialized or written to file
        """
        try:
            # Determine the encoding format
            cert_bytes = cert.public_bytes(encoding=serialization.Encoding.DER)

            # Write the certificate to the file
            with open(cert_path, "wb") as cert_file:
                cert_file.write(cert_bytes)
            logger.info(f"Certificate successfully written to {cert_path}")
        except Exception as e:
            logger.error(f"Failed to write certificate to {cert_path}: {str(e)}")
            raise

    @staticmethod
    def get_ecc_public_key(key_path: Path) -> ec.EllipticCurvePublicKey:
        """Read a prime256v1 ECC public key from a file using the cryptography library.

        Args:
            key_path: Path to the public key file in PEM format
        Returns:
            EllipticCurvePublicKey: The loaded public key
        Raises:
            ValueError: If the key cannot be loaded or is not a prime256v1 ECC key
        """
        try:
            with key_path.open("rb") as key_file:
                key_data = key_file.read()
                public_key = serialization.load_pem_public_key(key_data, backend=default_backend())

                # Verify it's an EC key using prime256v1 curve
                if not isinstance(public_key, ec.EllipticCurvePublicKey):
                    raise ValueError("Key is not an ECC public key")
                if not isinstance(public_key.curve, ec.SECP256R1):
                    raise ValueError("Key is not using prime256v1 curve")

                return public_key
        except Exception as e:
            logger.error(f"Failed to load public key from {key_path}: {str(e)}")
            raise

    @staticmethod
    def is_key_encrypted(key_path: str) -> bool:
        """Check if a private key is encrypted.

        Args:
            key_path: Path to the private key file in PEM format

        Returns:
            bool: True if the key is encrypted, False otherwise
        """
        try:
            with open(key_path, "rb") as key_file:
                key_data = key_file.read()
                # Try loading without password
                serialization.load_pem_private_key(
                    key_data,
                    password=None,
                    backend=default_backend(),
                )
                return False
        except Exception as e:
            if "private key is encrypted" in str(e).lower():
                return True
            raise

    @staticmethod
    def get_ecc_private_key(key_path: Path, password: str | None = None) -> ec.EllipticCurvePrivateKey:
        """Read an ECC private key from a file using the cryptography library.

        Args:
            key_path: Path to the private key file in PEM format
            password: Optional password if the key is encrypted
        Returns:
            EllipticCurvePrivateKey: The loaded private key
        Raises:
            ValueError: If the key cannot be loaded or is not a prime256v1 ECC key
        """
        try:
            with key_path.open("rb") as key_file:
                key_data = key_file.read()
                private_key = serialization.load_pem_private_key(
                    key_data,
                    password=password.encode() if password else None,
                    backend=default_backend(),
                )

                # Verify it's an EC key using prime256v1 curve
                if not isinstance(private_key, ec.EllipticCurvePrivateKey):
                    raise ValueError("Key is not an ECC private key")
                if not isinstance(private_key.curve, ec.SECP256R1):
                    raise ValueError("Key is not using prime256v1 curve")

                return private_key
        except Exception as e:
            logger.error(f"Failed to load private key from {key_path}: {str(e)}")
            raise

    @staticmethod
    def get_raw_public_key_bytes(public_key: ec.EllipticCurvePublicKey) -> bytes:
        """Extract the raw bytes of an ECC public key.

        Args:
            public_key: The ECC public key object

        Returns:
            bytes: Raw 64-byte public key (32 bytes x + 32 bytes y coordinates)

        Note:
            Strips the leading 0x04 byte from the uncompressed point format
        """
        # Serialize the public key to raw bytes
        raw_bytes = public_key.public_bytes(
            encoding=serialization.Encoding.X962,
            format=serialization.PublicFormat.UncompressedPoint,
        )
        return raw_bytes[1:]  # Skip the first byte (0x04)

    @staticmethod
    def load_public_key_from_bytes(public_key_bytes: bytes) -> ec.EllipticCurvePublicKey:
        """Load an ECC prime256v1 public key from raw bytes.

        Args:
            public_key_bytes: Raw 64-byte public key (32 bytes x + 32 bytes y coordinates)

        Returns:
            EllipticCurvePublicKey: The loaded public key object

        Raises:
            ValueError: If the public key bytes are not valid
        """
        if len(public_key_bytes) != 64:
            raise ValueError("Public key must be exactly 64 bytes")

        # Create a public key from the raw bytes
        return ec.EllipticCurvePublicKey.from_encoded_point(ec.SECP256R1(), b"\x04" + public_key_bytes)

    def __init__(
        self,
        product: Product,
        security: SecurityWorld,
        cert_path: Path,
        sk: Path | None,
        pwd: str | None,
        pk: Path | bytes | None,
        contents: Dict[str, Path] | None = None,
        out_dir: Path | None = None,
        skip_logging: bool = False,
    ) -> None:
        """Initialize the Chain of Trust Certificate Manager.

        Args:
            product: Product identifier (e.g. 'C2', 'C3')
            security: Security world (TRUSTED or NON_TRUSTED)
            cert_path: Path to the certificate file
            sk: Optional path to the signing key file
            pwd: Optional password for the signing key
            pk: Optional path to the public key file or raw public key bytes
            contents: Optional dictionary mapping OIDs to file paths for certificate contents
            out_dir: Optional output directory for signed certificates (defaults to temp dir)
            skip_logging: Optional flag to disable logging certificate details

        Raises:
            ValueError: If output directory is same as input directory or security world is invalid
        """
        self.cert_path = cert_path
        self.sk = sk
        self.pwd = pwd

        self.pk_bytes = None
        self.pk_obj = None

        if isinstance(pk, Path):
            self.pk_obj = self.get_ecc_public_key(pk)
            self.pk_bytes = self.get_raw_public_key_bytes(self.pk_obj)
        elif isinstance(pk, bytes):
            self.pk_bytes = pk
            self.pk_obj = self.load_public_key_from_bytes(pk)

        self.contents = contents
        self.out_dir = out_dir or Path(tempfile.mkdtemp())

        # Ensure output directory is not the same as input directory
        if self.out_dir.resolve() == Path(cert_path).parent.resolve():
            logger.error("Output directory cannot be the same as input directory")
            raise ValueError("Output directory cannot be the same as input directory")

        if security == SecurityWorld.TRUSTED:
            self.nv_counter = TRUSTED_NV_COUNTER[product]
        elif security == SecurityWorld.NON_TRUSTED:
            self.nv_counter = NON_TRUSTED_NV_COUNTER[product]
        else:
            raise ValueError(f"Invalid security world: {security}")

        self.cert = self.read_certificate(cert_path)

        # Log certificate details if not in print_cert mode
        if not skip_logging:
            cert_name = Path(cert_path).name
            logger.info("Certificate Details:")
            logger.info(f"Certificate Name: {cert_name}")
            if sk:
                logger.info(f"Signing key: {sk}")
            if pk:
                logger.info(f"Public key: {pk}")

            # Log certificate contents
            if contents:
                logger.info("Certificate Contents:")
                for oid, path in contents.items():
                    logger.info(f"{OID_TO_STRING[oid]}: {path}")

            # Log extensions from current certificate
            ext_data = self.parse_extensions()
            if ext_data:
                logger.debug("Current Certificate Extensions:")
                for oid, data in ext_data.items():
                    if isinstance(data, bytes):
                        logger.debug(f"{OID_TO_STRING[oid]}: {data.hex()}")
                    else:
                        logger.debug(f"{OID_TO_STRING[oid]}: {data}")

    def verify_self_signed(self, cert: x509.Certificate, cli_public_key: bytes | None = None) -> bool:
        """Verify that a certificate is self-signed by checking its signature.

        Args:
            cert: The X.509 certificate to verify
            cli_public_key: Optional raw public key bytes to verify against certificate's public key

        Returns:
            bool: True if signature verification checks succeed, False otherwise

        Note:
            If cli_public_key is provided, verifies both the signature and that the
            certificate's public key matches the provided key.
        """
        try:
            public_key = cert.public_key()
            signature = cert.signature
            tbs_cert = cert.tbs_certificate_bytes

            if not isinstance(public_key, ec.EllipticCurvePublicKey):
                logger.error("Certificate public key is not an ECC key")
                return False

            public_key.verify(signature, tbs_cert, ec.ECDSA(hashes.SHA256()))

            if cli_public_key:
                # Compare the raw public key bytes with the provided public key
                raw_public_key = self.get_raw_public_key_bytes(public_key)
                if raw_public_key != cli_public_key:
                    logger.error("Public key mismatch")
                    return False
            logger.info("Certificate signature verification successful")
            return True
        except Exception as e:
            logger.error(f"Certificate signature verification failed: {str(e)}")
            return False

    def build_certificate(self, new_cert_data):
        """Build a new certificate from an existing one with updated extensions.

        Args:
            new_cert_data (dict): Dictionary mapping OIDs to their new values
        """
        if not self.pk_obj:
            logger.error("Public key object is not initialized")
            raise ValueError("Public key object is not initialized")

        existing_cert = self.cert

        # Build a new certificate from the existing one
        builder = x509.CertificateBuilder()
        builder = builder.subject_name(existing_cert.subject)
        builder = builder.issuer_name(existing_cert.issuer)
        builder = builder.not_valid_before(existing_cert.not_valid_before_utc)
        builder = builder.not_valid_after(existing_cert.not_valid_after_utc)
        builder = builder.serial_number(existing_cert.serial_number)

        builder = builder.public_key(self.pk_obj)

        # Process each extension from the existing certificate
        for extension in existing_cert.extensions:
            oid = extension.oid.dotted_string
            critical = extension.critical

            # If this extension needs to be updated (exists in new_cert_data)
            if oid in new_cert_data:
                if OID_TO_EXTENSION_TYPE[oid] == ExtensionType.NV_COUNTER:
                    value = self.nv_counter_to_asn1(new_cert_data[oid])
                elif OID_TO_EXTENSION_TYPE[oid] == ExtensionType.PUBLIC_KEY:
                    value = self.public_key_to_asn1(new_cert_data[oid])
                elif OID_TO_EXTENSION_TYPE[oid] == ExtensionType.HASH:
                    value = self.hash_to_asn1(new_cert_data[oid])
                else:
                    raise ValueError(f"Unknown extension type for OID: {oid}")

                # Create new x509.Extension with the updated value
                new_ext = x509.Extension(
                    oid=x509.ObjectIdentifier(oid),
                    critical=critical,
                    value=x509.UnrecognizedExtension(x509.ObjectIdentifier(oid), value),
                )
                builder = builder.add_extension(new_ext.value, critical=critical)
            else:
                # Keep the existing extension as is
                builder = builder.add_extension(extension.value, critical=critical)

        return builder

    @staticmethod
    def nv_counter_from_asn1(raw_data: bytes) -> int:
        """Convert ASN.1 encoded NV counter value to an integer.

        Args:
            raw_data: ASN.1 DER encoded integer bytes

        Returns:
            int: The decoded NV counter value
        """
        asn1_integer, _ = decode(raw_data, asn1Spec=Integer())
        integer_value = int(asn1_integer)
        logger.debug(f"Decoded ASN.1 NV counter value: {integer_value}")
        return integer_value

    @staticmethod
    def nv_counter_to_asn1(counter: int) -> bytes:
        """Convert an integer NV counter value to ASN.1 DER format.

        Args:
            counter: The NV counter value to encode

        Returns:
            bytes: ASN.1 DER encoded integer
        """
        asn1_integer = Integer(counter)
        encoded_data = encoder.encode(asn1_integer)
        logger.debug(f"Encoded NV counter to ASN.1: {encoded_data.hex()}")
        return encoded_data

    @staticmethod
    def public_key_from_asn1(raw_data: bytes) -> bytes:
        """Extract raw public key bytes from ASN.1 SubjectPublicKeyInfo structure.

        Args:
            raw_data: ASN.1 DER encoded SubjectPublicKeyInfo

        Returns:
            bytes: Raw 64-byte public key (32 bytes x + 32 bytes y coordinates)

        Note:
            Strips the leading 0x04 byte
        """
        decoded_key, _ = decoder.decode(raw_data, asn1Spec=rfc2459.SubjectPublicKeyInfo())

        # Extract the algorithm identifier and the public key data
        algorithm = decoded_key.getComponentByName("algorithm")
        public_key_data = decoded_key.getComponentByName("subjectPublicKey").asOctets()
        public_key_data = public_key_data[1:]  # Skip the first byte (0x04)

        logger.debug(f"Decoded public key algorithm: {algorithm}")
        logger.debug(f"Decoded public key data: {public_key_data.hex()}")

        return public_key_data

    @staticmethod
    def public_key_to_asn1(public_key_data: bytes) -> bytes:
        """Convert an ECDSA public key to ASN.1 format"""
        # Create the AlgorithmIdentifier sequence
        algorithm = Sequence()
        algorithm.setComponentByPosition(0, ObjectIdentifier(EC_PUBLIC_KEY_OID))
        algorithm.setComponentByPosition(1, ObjectIdentifier(PRIME256V1_CURVE_OID))

        # Create the SubjectPublicKeyInfo sequence
        subject_public_key_info = Sequence()
        subject_public_key_info.setComponentByPosition(0, algorithm)

        # Encode the public key as a BIT STRING
        bit_string = BitString.fromOctetString(b"\x04" + public_key_data)
        subject_public_key_info.setComponentByPosition(1, bit_string)

        # Encode the whole structure
        asn1_encoded = encoder.encode(subject_public_key_info)

        return asn1_encoded

    @staticmethod
    def hash_from_asn1(raw_data: bytes) -> bytes:
        """Extract raw hash value from ASN.1 encoded structure.

        Args:
            raw_data: ASN.1 DER encoded hash structure

        Returns:
            bytes: Raw hash value
        """
        hash_data, _ = decoder.decode(raw_data)
        hash_data = hash_data[1].asOctets()
        logger.debug(f"Decoded hash value: {hash_data.hex()}")
        return hash_data

    @staticmethod
    def hash_to_asn1(hash_data: bytes) -> bytes:
        """Encode a raw hash value in ASN.1 DER format with SHA-256 algorithm identifier.

        Args:
            hash_data: Raw hash value to encode

        Returns:
            bytes: ASN.1 DER encoded structure containing algorithm ID and hash value
        """
        # Define the OID for SHA-256
        sha256_oid = ObjectIdentifier("2.16.840.1.101.3.4.2.1")

        # Create the inner sequence for the OID and NULL
        oid_sequence = Sequence()
        oid_sequence.setComponentByPosition(0, sha256_oid)
        oid_sequence.setComponentByPosition(1, Null(""))

        # Create the outer sequence that includes the OID sequence and the hash
        asn1_sequence = Sequence()
        asn1_sequence.setComponentByPosition(0, oid_sequence)
        asn1_sequence.setComponentByPosition(1, OctetString(hash_data))

        asn1_encoded = encoder.encode(asn1_sequence)

        return asn1_encoded

    def parse_contents(self, existing_cert_data: dict):
        """Parse certificate contents into a dictionary of OID-to-value mappings.

        Returns:
            Dict[str, Union[int, bytes]]: Dictionary mapping OIDs to their values:
                - NV_COUNTER OIDs map to integers
                - PUBLIC_KEY OIDs map to 64-byte raw key data
                - HASH OIDs map to 32-byte hash values

        Raises:
            ValueError: If certificate type is unknown

        Note:
            Uses dummy hash values (32 zero bytes) for missing hash files
        """
        cert_name = Path(self.cert_path).name
        if cert_name not in CERT_MAPPING:
            logger.error(f"Unknown certificate type: {cert_name}")
            raise ValueError(f"Unknown certificate type: {cert_name}")
        if not self.contents:
            logger.error("No contents provided for certificate parsing")
            raise ValueError("No contents provided for certificate parsing")

        logger.debug(f"Parsing contents for certificate: {cert_name}")
        relevant_oids = CERT_MAPPING[cert_name]
        logger.debug(f"Relevant OIDs for {cert_name}: {relevant_oids}")

        data = {}
        for oid in relevant_oids:
            logger.debug(f"Processing OID: {oid} ({OID_TO_STRING[oid]})")
            if OID_TO_EXTENSION_TYPE[oid] == ExtensionType.NV_COUNTER:
                data[oid] = self.nv_counter
                logger.debug(f"NV counter value: {self.nv_counter}")
            elif OID_TO_EXTENSION_TYPE[oid] == ExtensionType.PUBLIC_KEY:
                ecc_key = self.get_ecc_public_key(self.contents[oid])
                data[oid] = self.get_raw_public_key_bytes(ecc_key)
                logger.debug(f"Public key bytes length: {len(data[oid])}")
            elif OID_TO_EXTENSION_TYPE[oid] == ExtensionType.HASH:
                if oid in self.contents:
                    data[oid] = self.get_file_hash(self.contents[oid])
                else:
                    # If no file is provided, copy it from the existing certificate
                    data[oid] = existing_cert_data[oid]
                logger.debug(f"Hash value: {data[oid].hex()}")
        return data

    def parse_extensions(self):
        """Parse certificate extensions into a dictionary of OID-to-value mappings.

        Returns:
            Dict[str, Union[int, bytes]]: Dictionary mapping OIDs to their values:
                - NV_COUNTER OIDs map to integers
                - PUBLIC_KEY OIDs map to 64-byte raw key data
                - HASH OIDs map to 32-byte hash values

        Note:
            Only processes extensions that are relevant for the certificate type
        """
        cert_name = Path(self.cert_path).name
        logger.debug(f"Parsing extensions for certificate: {cert_name}")

        data = {}
        for ext in self.cert.extensions:
            oid = ext.oid.dotted_string
            if oid in CERT_MAPPING[cert_name]:
                logger.debug(f"Processing extension OID: {oid} ({OID_TO_STRING[oid]})")
                if OID_TO_EXTENSION_TYPE[oid] == ExtensionType.NV_COUNTER:
                    data[oid] = self.nv_counter_from_asn1(ext.value.value)
                    logger.debug(f"Extracted NV counter value: {data[oid]}")
                elif OID_TO_EXTENSION_TYPE[oid] == ExtensionType.PUBLIC_KEY:
                    data[oid] = self.public_key_from_asn1(ext.value.value)
                    logger.debug(f"Extracted public key bytes length: {len(data[oid])}")
                elif OID_TO_EXTENSION_TYPE[oid] == ExtensionType.HASH:
                    data[oid] = self.hash_from_asn1(ext.value.value)
                    logger.debug(f"Extracted hash value: {data[oid].hex()}")
        return data

    def print_certificate_info(self, cert: x509.Certificate, cert_name: str):
        """Print detailed information about a certificate.

        Args:
            cert: The X.509 certificate object to analyze
            cert_name: Name of the certificate for logging purposes

        Note:
            Prints:
            - Basic certificate fields (subject, issuer, validity, etc.)
            - Public key information
            - Extension details
            - Raw signature data
            - Self-signed verification status

        Raises:
            Exception: Logs error if certificate parsing fails
        """
        extension_data = self.parse_extensions()

        try:
            print(f"\nCertificate: {cert_name}")
            print("=" * 50)
            print(f"Subject: {cert.subject}")
            print(f"Issuer: {cert.issuer}")
            print(f"Serial Number: {cert.serial_number}")
            print(f"Not Valid Before: {cert.not_valid_before_utc}")
            print(f"Not Valid After: {cert.not_valid_after_utc}")
            print(f"Version: {cert.version}")
            print(f"Signature Algorithm: {cert.signature_algorithm_oid._name}")

            # Print Public Key Info
            public_key = cert.public_key()
            if isinstance(public_key, ec.EllipticCurvePublicKey):
                print("\nPublic Key Info:")
                print("Algorithm: Elliptic Curve")
                print(f"Curve: {public_key.curve.name}")

                # Get and print raw public key coordinates

                raw_public_key = self.get_raw_public_key_bytes(public_key)
                print(f"\nRaw Public Key (64 bytes): {raw_public_key.hex()}")

            # Print Extensions
            print("\nExtensions:")
            for ext in cert.extensions:
                print(f"OID: {ext.oid._name} ({ext.oid.dotted_string})")
                print(f"Critical: {ext.critical}")

                # Handle different extension types
                if isinstance(ext.value, x509.BasicConstraints):
                    print(f"CA: {ext.value.ca}")
                    if ext.value.ca:
                        print(f"Path Length: {ext.value.path_length}")
                elif isinstance(ext.value, x509.SubjectKeyIdentifier):
                    print(f"Digest: {ext.value.digest.hex()}")
                elif isinstance(ext.value, x509.AuthorityKeyIdentifier):
                    if ext.value.key_identifier:
                        print(f"Key Identifier: {ext.value.key_identifier.hex()}")
                else:
                    data = extension_data[ext.oid.dotted_string]
                    if type(data) is bytes:
                        data = data.hex()
                    print(f"{OID_TO_STRING[ext.oid.dotted_string]}: {data}")
                print()

            # Print Signature
            print("Signature:")
            signature = cert.signature
            print(f"DER Length: {len(signature)} bytes")

            # Convert to raw signature and print
            raw_sig = self.der_to_raw_signature(signature)
            print(f"\nRaw Signature (64 bytes): {raw_sig.hex()}")

            # Verify self-signed status
            print("\nSelf-signed verification:")
            if self.verify_self_signed(cert):
                print("Certificate is properly self-signed")
            else:
                print("Certificate is not properly self-signed")
            print("=" * 50)

        except Exception as e:
            logger.error(f"Error parsing certificate info: {str(e)}")

    def validate_signing_info(self, cli_public_key: bytes | None = None):
        """Validate certificate signing information before signing.

        Args:
            cli_public_key: Optional raw public key bytes to verify against certificate

        Raises:
            ValueError: If public key is missing or certificate is not loaded

        Note:
            Verifies:
            - Certificate is loaded
            - Certificate signature is valid
            - Public key matches if provided
        """
        logger.info("Validating certificate signing information")
        if not self.cert:
            logger.error("No certificate loaded")
            raise ValueError("No certificate loaded")

        logger.info("Verifying existing certificate signature")
        if not self.verify_self_signed(self.cert, cli_public_key):
            logger.error("Certificate signature verification failed")
            raise ValueError("Certificate signature verification failed")
        logger.info("Certificate validation complete")

    def update_cert(self):
        """Update certificate with new public key and content.

        Returns:
            x509.CertificateBuilder: Builder configured with updated certificate data

        Raises:
            ValueError: If there is a mismatch between existing and new certificate OIDs

        Note:
            Compares existing certificate extensions with new content to ensure
            all required OIDs are present and updates their values
        """
        logger.info("Starting certificate update process")

        existing_cert_data = self.parse_extensions()
        logger.debug("Parsed existing certificate extensions")

        new_cert_data = self.parse_contents(existing_cert_data)
        logger.debug("Parsed new certificate contents")

        if existing_cert_data.keys() != new_cert_data.keys():
            logger.error("Mismatch between existing and new certificate OIDs")
            logger.error(f"Existing OIDs: {sorted(existing_cert_data.keys())}")
            logger.error(f"New OIDs: {sorted(new_cert_data.keys())}")
            raise ValueError("Certificate OID mismatch")

        logger.info("Building new certificate")
        return self.build_certificate(new_cert_data)

    def sign_leaf_kms(
        self, cert_builder: x509.CertificateBuilder, env: Environment, trusted_state: SecurityWorld
    ) -> x509.Certificate:
        """Sign a leaf certificate with the private key and verify the signature using KMS.

        Args:
            cert_builder: x509.CertificateBuilder configured with certificate data
            env: The environment to use for signing
            trusted_state: The trusted state of the certificate (trusted or non-trusted)

        Returns:
            x509.Certificate: The signed certificate

        Raises:
            ValueError: If signature verification fails
        """
        logger.error("Unsupported signing provider")
        raise NotImplementedError("Unsupported signing provider")

    def sign_leaf_hcv(
        self, cert_builder: x509.CertificateBuilder, env: Environment, trusted_state: SecurityWorld
    ) -> x509.Certificate:
        """Sign a leaf certificate with the private key and verify the signature using HashiCorp Vault.

        Args:
            cert_builder: x509.CertificateBuilder configured with certificate data
            env: The environment to use for signing
            trusted_state: The trusted state of the certificate (trusted or non-trusted)

        Returns:
            x509.Certificate: The signed certificate

        Raises:
            ValueError: If signature verification fails
        """
        # TODO: Populate these values once we have a working HashiCorp Vault setup
        HCV_MAP = {
            Environment.LOCAL: "http://host.docker.internal:8200",
            Environment.STAGING: "https://52.86.141.60:8200",
            Environment.PROD: "https://54.197.16.69:8200",
        }

        if env in [Environment.LOCAL, Environment.STAGING]:
            key_type = "dev"
        elif env == Environment.PROD:
            key_type = "prod"
        else:
            logger.error("Invalid environment for HashiCorp Vault")
            raise ValueError("Invalid environment for HashiCorp Vault")

        KEY_NAME_MAP = {
            SecurityWorld.TRUSTED: f"c3-trusted-os-firmware-key-{key_type}",
            SecurityWorld.NON_TRUSTED: f"c3-nontrusted-os-firmware-key-{key_type}",
        }

        endpoint = HCV_MAP.get(env)
        if endpoint is None:
            logger.error("Invalid environment for HashiCorp Vault")
            raise ValueError("Invalid environment for HashiCorp Vault")

        key_name = KEY_NAME_MAP.get(trusted_state)
        if key_name is None:
            logger.error("Invalid signing provider or trusted state")
            raise ValueError("Invalid signing provider or trusted state")

        username = click.prompt(f"Enter your Hashicorp Vault username for {env.name}", hide_input=False)
        client = check_auth(env, username, SigningProvider.HASHICORP_VAULT)
        if not client:
            logger.error("Failed to authenticate with HashiCorp Vault")
            raise ValueError("Failed to authenticate with HashiCorp Vault")

        try:
            # Generate a dummy private key to get the to-be-signed certificate bytes
            dummy_key = ec.generate_private_key(ec.SECP256R1())
            partial_cert = cert_builder.sign(dummy_key, hashes.SHA256())
            tbs_cert = partial_cert.tbs_certificate_bytes

            # Sign the base64-encoded message using HashiCorp Vault
            b64_message = base64.b64encode(tbs_cert).decode()
            sign_response = client.secrets.transit.sign_data(
                name=key_name,
                hash_input=b64_message,
                hash_algorithm="sha2-256",
                signature_algorithm="pkcs1v15",
            )
            vault_signature = sign_response["data"]["signature"]
            signature_bytes = base64.b64decode(vault_signature.split(":")[-1])

            # Convert to DER for asn1_x509 consumption, and replace the signature
            dummy_der = partial_cert.public_bytes(serialization.Encoding.DER)
            parsed_cert = asn1_x509.Certificate.load(dummy_der)
            parsed_cert["signature_value"] = signature_bytes
            final_der = parsed_cert.dump()
            certificate = x509.load_der_x509_certificate(final_der)

            logger.debug("Verifying new certificate signature")
            if self.verify_self_signed(certificate):
                logger.info("Certificate successfully signed and verified")
            else:
                logger.error("Certificate verification failed after signing")
                raise ValueError("Certificate verification failed after signing")

            return certificate

        except Exception as e:
            logger.error(f"Certificate signing failed: {str(e)}")
            raise ValueError(f"Certificate signing failed: {str(e)}")

    def sign_leaf_external(
        self,
        cert_builder: x509.CertificateBuilder,
        env: Environment,
        sign_provider: SigningProvider,
        trusted_state: SecurityWorld,
    ) -> x509.Certificate:
        """
        Sign a leaf certificate with the private key and verify the signature using an external provider.
        Leaf certificates are the Trusted/Non-Trusted OS Content certificates.

        Args:
            cert_builder: x509.CertificateBuilder configured with certificate data
            env: The environment to use for signing
            sign_provider: The signing provider to use
            trusted_state: The trusted state of the certificate (trusted or non-trusted)

        Returns:
            x509.Certificate: The signed certificate

        Raises:
            ValueError: If signature verification fails
        """
        logger.info(f"Signing certificate with {sign_provider.name} in {env.name}")

        if sign_provider == SigningProvider.HASHICORP_VAULT:
            certificate = self.sign_leaf_hcv(cert_builder, env, trusted_state)
        elif sign_provider == SigningProvider.AWS_KMS:
            certificate = self.sign_leaf_kms(cert_builder, env, trusted_state)
        else:
            logger.error("Unsupported signing provider")
            raise NotImplementedError("Unsupported signing provider")

        return certificate

    def sign(self, cert_builder: x509.CertificateBuilder) -> x509.Certificate:
        """Sign a certificate with the private key and verify the signature.

        Args:
            cert_builder: x509.CertificateBuilder configured with certificate data

        Returns:
            x509.Certificate: The signed certificate

        Raises:
            ValueError: If signature verification fails
        """
        logger.info("Starting certificate signing process")

        try:
            logger.debug("Loading private key")
            if not self.sk:
                logger.error("No signing key provided")
                raise ValueError("No signing key provided")
            private_key = self.get_ecc_private_key(self.sk, self.pwd)

            logger.debug("Signing certificate")
            certificate = cert_builder.sign(private_key=private_key, algorithm=hashes.SHA256())

            logger.debug("Verifying new certificate signature")
            if self.verify_self_signed(certificate):
                logger.info("Certificate successfully signed and verified")
            else:
                logger.error("Certificate verification failed after signing")
                raise ValueError("Certificate verification failed after signing")

            return certificate

        except Exception as e:
            logger.error(f"Certificate signing failed: {str(e)}")
            raise ValueError(f"Certificate signing failed: {str(e)}")

    def finalize_cert(self, certificate: x509.Certificate):
        """Write the signed certificate to the output directory.

        Args:
            certificate: The signed x509.Certificate object to write

        Note:
            Uses the same filename as the input certificate but writes to
            the configured output directory
        """
        cert_name = Path(self.cert_path).name
        out_path = self.out_dir / cert_name
        logger.info(f"Writing certificate to {out_path}")
        self.write_certificate(certificate, Path(out_path))

    def cert_signing_workflow(self):
        """Helper to generate, validate, and sign a CoT certificate"""

        self.validate_signing_info()
        cert_builder = self.update_cert()
        certificate = self.sign(cert_builder)
        self.finalize_cert(certificate)

    def leaf_cert_signing_workflow_external(
        self, env: Environment, sign_provider: SigningProvider, trusted_state: SecurityWorld
    ):
        """
        Helper to generate, validate, and sign a CoT leaf certificate using an external provider.

        Args:
            environment: The environment to use for signing
            sign_provider: The signing provider to use
            trusted_state: The trusted state of the certificate (trusted or non-trusted)
        """
        self.validate_signing_info()
        cert_builder = self.update_cert()
        certificate = self.sign_leaf_external(cert_builder, env, sign_provider, trusted_state)
        self.finalize_cert(certificate)

import os
import shutil
import tempfile
import unittest
from pathlib import Path

from cryptography import x509
from cryptography.hazmat.backends import default_backend
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec

from cot_signer.constants import TRUSTED_KEY_CERT_NAME, Product, SecurityWorld
from cot_signer.utils.certificate_manager import ChainOfTrustCertManager


class TestChainOfTrustCertManager(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        """Set up test files and create working directories."""
        # Get the directory containing the test files
        cls.base_dir = Path(os.path.dirname(os.path.abspath(__file__)))

        # Create separate directories for test files and output
        cls.test_dir = Path(tempfile.mkdtemp())  # For test files
        cls.out_dir = Path(tempfile.mkdtemp())  # For output files

        # Define paths to existing test files
        cls.original_cert_path = cls.base_dir / "test.crt"
        cls.sk_path = cls.base_dir / "test_privatekey.pem"
        cls.pk_path = cls.base_dir / "test_publickey.pem"

        # Copy test.crt to trusted_key.crt in the test directory
        cls.cert_path = cls.test_dir / TRUSTED_KEY_CERT_NAME
        shutil.copy2(cls.original_cert_path, cls.cert_path)

        # Load the test certificate
        with open(cls.cert_path, "rb") as f:
            cls.certificate = x509.load_der_x509_certificate(f.read())

        # Load the public key
        with open(cls.pk_path, "rb") as f:
            public_key = serialization.load_pem_public_key(f.read(), backend=default_backend())

        # Ensure the public key is an ECC key
        if not isinstance(public_key, ec.EllipticCurvePublicKey):
            raise ValueError("Public key is not an ECC key")
        if not isinstance(public_key.curve, ec.SECP256R1):
            raise ValueError("Public key is not using prime256v1 curve")

        cls.public_key = public_key

        # Load the private key
        with open(cls.sk_path, "rb") as f:
            cls.private_key = serialization.load_pem_private_key(f.read(), password=b"qwert1")

    def setUp(self):
        """Create a fresh ChainOfTrustCertManager instance for each test."""
        self.cot_manager = ChainOfTrustCertManager(
            product=Product.C2,
            security=SecurityWorld.TRUSTED,
            cert_path=self.cert_path,
            sk=self.sk_path,
            pwd="qwert1",
            pk=self.pk_path,
            out_dir=self.out_dir,
        )

    @classmethod
    def tearDownClass(cls):
        """Clean up test and output directories after all tests."""
        shutil.rmtree(cls.test_dir)
        shutil.rmtree(cls.out_dir)

    def test_verify_self_signed(self):
        """Test verify_self_signed function."""
        # Test with valid certificate
        self.assertTrue(self.cot_manager.verify_self_signed(self.certificate))

        # Test with public key verification
        raw_public_key = self.cot_manager.get_raw_public_key_bytes(self.public_key)
        self.assertTrue(self.cot_manager.verify_self_signed(self.certificate, raw_public_key))

        # Test with wrong public key
        wrong_public_key = ec.generate_private_key(ec.SECP256R1()).public_key()
        wrong_raw_key = self.cot_manager.get_raw_public_key_bytes(wrong_public_key)
        self.assertFalse(self.cot_manager.verify_self_signed(self.certificate, wrong_raw_key))

    def test_get_file_hash(self):
        """Test get_file_hash function."""
        # Create test file with known content
        test_file = self.test_dir / "test_file.txt"
        test_content = b"test content"
        with open(test_file, "wb") as f:
            f.write(test_content)

        # Calculate expected hash
        expected_hash = hashes.Hash(hashes.SHA256())
        expected_hash.update(test_content)
        expected_digest = expected_hash.finalize()

        # Test the function
        result = self.cot_manager.get_file_hash(test_file)
        self.assertEqual(result, expected_digest)

    def test_nv_counter_asn1_roundtrip(self):
        """Test NV counter ASN.1 encoding/decoding round trip."""
        test_values = [0, 1, 255, 65535, 4294967295]

        for value in test_values:
            encoded = self.cot_manager.nv_counter_to_asn1(value)
            decoded = self.cot_manager.nv_counter_from_asn1(encoded)
            self.assertEqual(decoded, value)

    def test_public_key_asn1_roundtrip(self):
        """Test public key ASN.1 encoding/decoding round trip."""
        raw_key = self.cot_manager.get_raw_public_key_bytes(self.public_key)

        encoded = self.cot_manager.public_key_to_asn1(raw_key)
        decoded = self.cot_manager.public_key_from_asn1(encoded)

        self.assertEqual(decoded, raw_key)

    def test_hash_asn1_roundtrip(self):
        """Test hash ASN.1 encoding/decoding round trip."""
        test_hash = os.urandom(32)  # Generate random 32-byte hash

        encoded = self.cot_manager.hash_to_asn1(test_hash)
        decoded = self.cot_manager.hash_from_asn1(encoded)

        self.assertEqual(decoded, test_hash)

    def test_parse_extensions(self):
        """Test parse_extensions function."""
        # Parse extensions
        result = self.cot_manager.parse_extensions()
        extension_oids = [x.oid.dotted_string for x in self.certificate.extensions if x.oid._name == "Unknown OID"]

        self.assertEqual(result.keys(), set(extension_oids))

    def test_validate_signing_info(self):
        """Test validate_signing_info function."""
        # Test with correct public key
        raw_public_key = self.cot_manager.get_raw_public_key_bytes(self.public_key)
        try:
            self.cot_manager.validate_signing_info(raw_public_key)
        except Exception as e:
            self.fail(f"validate_signing_info raised unexpected exception: {e}")

        # Test with wrong public key
        wrong_public_key = ec.generate_private_key(ec.SECP256R1()).public_key()
        wrong_raw_key = self.cot_manager.get_raw_public_key_bytes(wrong_public_key)
        with self.assertRaises(ValueError):
            self.cot_manager.validate_signing_info(wrong_raw_key)

    def test_load_public_key_from_bytes(self):
        """Test load_public_key_from_bytes function."""
        # Test with valid public key bytes
        raw_public_key = self.cot_manager.get_raw_public_key_bytes(self.public_key)
        loaded_key = self.cot_manager.load_public_key_from_bytes(raw_public_key)
        self.assertIsInstance(loaded_key, ec.EllipticCurvePublicKey)

        # Test with invalid length public key
        invalid_bytes = b"invalid public key"
        with self.assertRaises(ValueError):
            self.cot_manager.load_public_key_from_bytes(invalid_bytes)

        # Test with invalid public key
        invalid_key = b"\x00" * 64
        with self.assertRaises(ValueError):
            self.cot_manager.load_public_key_from_bytes(invalid_key)

    def test_public_key_roundtrip(self):
        """Test public key round trip."""
        # Get raw public key bytes
        raw_public_key = self.cot_manager.get_raw_public_key_bytes(self.public_key)

        # Load public key from bytes
        loaded_key = self.cot_manager.load_public_key_from_bytes(raw_public_key)

        # Check if the loaded key matches the original public key
        self.assertEqual(
            loaded_key.public_bytes(
                encoding=serialization.Encoding.PEM,
                format=serialization.PublicFormat.SubjectPublicKeyInfo,
            ),
            self.public_key.public_bytes(
                encoding=serialization.Encoding.PEM,
                format=serialization.PublicFormat.SubjectPublicKeyInfo,
            ),
        )


if __name__ == "__main__":
    unittest.main()

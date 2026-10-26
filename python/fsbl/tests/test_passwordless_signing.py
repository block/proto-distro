#!/usr/bin/env python3

"""
Integration tests for passwordless signing with real ECC key fixtures.

These tests complement the mocked unit tests in test_fsbl_signer.py by:
- Using real PyCryptodome operations (no mocking)
- Testing with actual key files on disk
- Providing end-to-end validation of crypto operations

If you need to reduce test suite size, keep these 3 core integration tests
and rely on test_fsbl_signer.py for detailed unit test coverage.
"""

import sys
import unittest
from pathlib import Path

from Crypto.Hash import SHA256
from Crypto.Signature import DSS

sys.path.insert(0, str(Path(__file__).parent.parent))
from fsbl_signer import ECDSA_ALGO, FSBLSigner


class TestPasswordlessSigningIntegration(unittest.TestCase):
    """Integration tests for passwordless signing with real keys (no mocks)."""

    @classmethod
    def setUpClass(cls):
        """Set up test fixtures for the entire test class."""
        cls.fixtures_dir = Path(__file__).parent / "fixtures"
        cls.unencrypted_private_key = cls.fixtures_dir / "privateKey00_unencrypted.pem"
        cls.encrypted_private_key = cls.fixtures_dir / "privateKey00_encrypted.pem"
        cls.test_password = "testpassword"

        # Verify required files exist
        if not cls.unencrypted_private_key.exists():
            raise FileNotFoundError(f"Unencrypted private key not found: {cls.unencrypted_private_key}")
        if not cls.encrypted_private_key.exists():
            raise FileNotFoundError(f"Encrypted private key not found: {cls.encrypted_private_key}")

    def test_passwordless_signing_end_to_end(self):
        """Integration test: Load unencrypted key, sign, and verify."""
        # Load unencrypted key without password
        private_key = FSBLSigner.get_ecc_key_from_file(self.unencrypted_private_key, None)
        self.assertIsNotNone(private_key)
        self.assertTrue(private_key.has_private())

        # Extract raw public key
        raw_pubkey = FSBLSigner.get_raw_pubkey(private_key)
        self.assertEqual(len(raw_pubkey), 64)

        # Sign a test message
        test_message = b"This is a test message for passwordless signing"
        message_hash = SHA256.new(test_message)
        signer = DSS.new(private_key, "fips-186-3")
        signature = signer.sign(message_hash)

        # Verify signature
        verified = FSBLSigner.verify_signature_with_raw_pubkey(raw_pubkey, signature, test_message)
        self.assertTrue(verified, "Signature verification failed with real unencrypted key")

    def test_encrypted_key_still_requires_password(self):
        """Integration test: Encrypted keys must fail without password."""
        with self.assertRaisesRegex(ValueError, "Key appears to be encrypted but no password provided"):
            FSBLSigner.get_ecc_key_from_file(self.encrypted_private_key, None)

        # But works with correct password
        key = FSBLSigner.get_ecc_key_from_file(self.encrypted_private_key, self.test_password)
        self.assertIsNotNone(key)
        self.assertTrue(key.has_private())

    def test_deterministic_pubkey_hashing(self):
        """Integration test: Public key hashing produces consistent results."""
        # Load same key twice
        key1 = FSBLSigner.get_ecc_key_from_file(self.unencrypted_private_key, None)
        key2 = FSBLSigner.get_ecc_key_from_file(self.unencrypted_private_key, None)

        # Hash should be identical (deterministic test vector)
        hash1 = FSBLSigner.hash_pubkey_with_ecc_alg(key1, ECDSA_ALGO)
        hash2 = FSBLSigner.hash_pubkey_with_ecc_alg(key2, ECDSA_ALGO)

        self.assertEqual(hash1.digest(), hash2.digest(), "Public key hashing should be deterministic")


if __name__ == "__main__":
    unittest.main()

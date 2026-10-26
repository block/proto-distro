#!/usr/bin/env python3

import glob
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent))
from fsbl_signer import (
    AUTH_OPTION_FLAGS,
    HEADER_BASE_MAGIC,
    HEADER_EXT_AUTH,
    HEADER_EXT_PADDING,
    HEADER_EXT_TOTAL_LEN,
    HEADER_TOTAL_LEN,
    IMAGE_ALIGNMENT,
    PUBKEY_COUNT,
    STM32Header,
)


class TestFSBLSignerIntegration(unittest.TestCase):
    """Integration test suite for fsbl_signer script. The Signed artifact is known to be good when compared against ST's STM32 Signer Tool."""

    @classmethod
    def setUpClass(cls):
        """Set up test fixtures for the entire test class."""
        cls.root_dir = Path(__file__).parent.parent.parent.parent  # repo root
        cls.fsbl_dir = cls.root_dir / "python/fsbl"
        cls.test_dir = cls.fsbl_dir / "tests"

        # Path to test artifacts
        cls.test_artifacts_dir = cls.fsbl_dir / "test_artifacts"
        cls.unsigned_binary = cls.test_artifacts_dir / "tf-a-stm32mp131f-c3p1-emmc.stm32"
        cls.signed_binary_expected = cls.test_artifacts_dir / "tf-a-stm32mp131f-c3p1-emmc_Signed.stm32"

        # Path to signing keys
        cls.key_dir = cls.root_dir / "sources/meta-miner/recipes-dev/miner-signing-keys/files/stm32mp13_dev_keys"
        cls.private_key = cls.key_dir / "privateKey01.pem"

        # Verify required files exist
        if not cls.unsigned_binary.exists():
            raise FileNotFoundError(f"Unsigned binary not found: {cls.unsigned_binary}")
        if not cls.signed_binary_expected.exists():
            raise FileNotFoundError(f"Expected signed binary not found: {cls.signed_binary_expected}")
        if not cls.private_key.exists():
            raise FileNotFoundError(f"Private key not found: {cls.private_key}")
        if not cls.key_dir.exists():
            raise FileNotFoundError(f"Key directory not found: {cls.key_dir}")

    def setUp(self):
        """Set up test fixtures for each test."""
        # Create temporary directory for test outputs
        self.temp_dir = Path(tempfile.mkdtemp())
        self.signed_binary_output = self.temp_dir / "tf-a-stm32mp131f-c3p1-emmc_test_signed.stm32"
        self.pk_hashes_dir = self.temp_dir / "pk_hashes"
        self.pk_hashes_dir.mkdir()

    def tearDown(self):
        """Clean up test fixtures after each test."""
        import shutil

        shutil.rmtree(self.temp_dir)

    def test_fsbl_signer_integration(self):
        """Test fsbl_signer script against unsigned binary and compare with expected signed binary."""

        # Build fsbl_signer command arguments as shown in test_integration.txt
        fsbl_signer_args = [
            "--product",
            "C3",
            "-i",
            Path(self.unsigned_binary),
            "-o",
            Path(self.signed_binary_output),
            "--sk",
            Path(self.private_key),
            "--pwd",
            "qwert2",
            "--pk-hashes-out",
            Path(self.pk_hashes_dir),
        ]

        # Add all public keys from the key directory (sorted order is important)
        public_key_pattern = str(self.key_dir / "publicKey*.pem")
        for pubkey in sorted(glob.glob(public_key_pattern)):
            fsbl_signer_args += ["--pk", pubkey]

        # Run fsbl_signer command
        cmd = ["fsbl_signer", "sign"] + fsbl_signer_args

        try:
            result = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
        except subprocess.TimeoutExpired:
            self.fail("fsbl_signer command timed out after 30 seconds")
        except FileNotFoundError:
            self.fail("fsbl_signer command not found. Make sure it's installed and in PATH.")

        # Check that the command succeeded
        if result.returncode != 0:
            self.fail(
                f"fsbl_signer command failed with return code {result.returncode}.\n"
                f"STDOUT: {result.stdout}\n"
                f"STDERR: {result.stderr}"
            )

        # Verify the signed binary was created
        self.assertTrue(self.signed_binary_output.exists(), "Signed binary output file was not created")

        # Signature is randomised per run and the padding payload now carries
        # proto-signer metadata, so byte-compare against a fixed fixture is
        # impossible; assert structural fields and image-body equality instead.
        with open(self.signed_binary_output, "rb") as f:
            generated_signed_data = f.read()
        with open(self.unsigned_binary, "rb") as f:
            unsigned_data = f.read()

        # STM32 signing zero-pads the payload to the required 32-byte boundary.
        body = unsigned_data[HEADER_TOTAL_LEN:]
        expected_body = body + bytes(-len(body) % IMAGE_ALIGNMENT)
        self.assertEqual(generated_signed_data[HEADER_TOTAL_LEN:], expected_body)

        header = STM32Header.unpack(generated_signed_data[:HEADER_TOTAL_LEN])
        self.assertEqual(header.base_header.magic, HEADER_BASE_MAGIC)
        self.assertEqual(header.base_header.option_flags, AUTH_OPTION_FLAGS)
        self.assertEqual(header.base_header.header_extensions_len, HEADER_EXT_TOTAL_LEN)
        self.assertIsNotNone(header.auth_header)
        self.assertEqual(header.auth_header.extension_type, HEADER_EXT_AUTH)
        self.assertEqual(header.auth_header.public_key_number, PUBKEY_COUNT)
        self.assertEqual(header.padding_header.extension_type, HEADER_EXT_PADDING)
        # No --fip-sk passed in this test, so no proto marker is expected.
        self.assertIsNone(header.padding_header.public_key_index)

        # Verify that public key hash files were generated and match the contents of key_dir
        expected_hash_files = [
            "publicKeyHash00.bin",
            "publicKeyHash01.bin",
            "publicKeyHash02.bin",
            "publicKeyHash03.bin",
            "publicKeyHash04.bin",
            "publicKeyHash05.bin",
            "publicKeyHash06.bin",
            "publicKeyHash07.bin",
            "publicKeysHashHashes.bin",
        ]

        for hash_file in expected_hash_files:
            hash_file_path = self.pk_hashes_dir / hash_file
            expected_hash_file_path = self.key_dir / hash_file
            self.assertTrue(
                hash_file_path.exists() and expected_hash_file_path.exists(),
                f"Expected hash file {hash_file} was not created",
            )
            self.assertGreater(hash_file_path.stat().st_size, 0, f"Hash file {hash_file} is empty")

            # Compare the contents of the generated hash file with the expected one
            with open(hash_file_path, "rb") as f1, open(expected_hash_file_path, "rb") as f2:
                self.assertEqual(f1.read(), f2.read(), f"Hash file {hash_file} contents do not match expected contents")

    def test_fsbl_signer_verify_integration(self):
        """Test fsbl_signer verify command against the expected signed binary to test against regressions to the verify command."""

        # Build fsbl_signer verify command arguments
        fsbl_signer_args = ["--product", "C3", "-i", Path(self.signed_binary_expected)]

        # Add all public keys from the key directory (sorted order is important)
        public_key_pattern = str(self.key_dir / "publicKey*.pem")
        for pubkey in sorted(glob.glob(public_key_pattern)):
            fsbl_signer_args += ["--pk", pubkey]

        # Run fsbl_signer verify command
        cmd = ["fsbl_signer", "verify"] + fsbl_signer_args

        try:
            result = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
        except subprocess.TimeoutExpired:
            self.fail("fsbl_signer verify command timed out after 30 seconds")
        except FileNotFoundError:
            self.fail("fsbl_signer command not found. Make sure it's installed and in PATH.")

        # Check that the verification succeeded
        if result.returncode != 0:
            self.fail(
                f"fsbl_signer verify command failed with return code {result.returncode}.\n"
                f"STDOUT: {result.stdout}\n"
                f"STDERR: {result.stderr}"
            )

        # Verify that the output contains success messages
        result_msg = result.stdout.lower() + result.stderr.lower()
        self.assertIn("verified successfully", result_msg, "Verification output does not contain success message")

    def test_fsbl_signer_verify_generated_binary(self):
        """Test fsbl_signer verify command against a freshly generated signed binary."""

        # First, generate a signed binary
        self.test_fsbl_signer_integration()

        # Now verify the generated binary
        fsbl_signer_args = ["--product", "C3", "-i", Path(self.signed_binary_output)]

        # Add all public keys from the key directory (sorted order is important)
        public_key_pattern = str(self.key_dir / "publicKey*.pem")
        for pubkey in sorted(glob.glob(public_key_pattern)):
            fsbl_signer_args += ["--pk", pubkey]

        # Run fsbl_signer verify command
        cmd = ["fsbl_signer", "verify"] + fsbl_signer_args

        try:
            result = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
        except subprocess.TimeoutExpired:
            self.fail("fsbl_signer verify command timed out after 30 seconds")
        except FileNotFoundError:
            self.fail("fsbl_signer command not found. Make sure it's installed and in PATH.")

        # Check that the verification succeeded
        if result.returncode != 0:
            self.fail(
                f"fsbl_signer verify command failed with return code {result.returncode}.\n"
                f"STDOUT: {result.stdout}\n"
                f"STDERR: {result.stderr}"
            )

        # Verify that the output contains success messages
        result_msg = result.stdout.lower() + result.stderr.lower()
        self.assertIn("verified successfully", result_msg, "Verification output does not contain success message")


if __name__ == "__main__":
    unittest.main()

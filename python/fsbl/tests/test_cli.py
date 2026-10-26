#!/usr/bin/env python3

# Import the CLI and related classes
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

from click.testing import CliRunner

sys.path.insert(0, str(Path(__file__).parent.parent))
from fsbl_signer import cli


class TestFSBLSignerCLI(unittest.TestCase):
    """Test suite for FSBL Signer CLI commands."""

    def setUp(self):
        """Set up test fixtures."""
        self.runner = CliRunner()
        self.temp_dir = Path(tempfile.mkdtemp())

        # Create mock files
        self.mock_input_file = self.temp_dir / "test_fsbl.bin"
        self.mock_output_file = self.temp_dir / "test_fsbl_signed.bin"
        self.mock_private_key = self.temp_dir / "privateKey00.pem"
        self.mock_public_key1 = self.temp_dir / "publicKey00.pem"
        self.mock_public_key2 = self.temp_dir / "publicKey01.pem"
        self.mock_hash_dir = self.temp_dir / "hashes"

        # Create mock file contents
        self.mock_input_file.write_bytes(b"mock_fsbl_binary_data" * 100)
        self.mock_private_key.write_text("mock_private_key_pem_data")
        self.mock_public_key1.write_text("mock_public_key_pem_data_1")
        self.mock_public_key2.write_text("mock_public_key_pem_data_2")
        self.mock_hash_dir.mkdir()

    def tearDown(self):
        """Clean up test fixtures."""
        import shutil

        shutil.rmtree(self.temp_dir)

    def test_cli_help(self):
        """Test that CLI help works."""
        result = self.runner.invoke(cli, ["--help"])
        self.assertEqual(result.exit_code, 0)
        self.assertIn("CLI for FSBL Signer for STM32MP1 series images", result.output)

    def test_cli_verbose_flag(self):
        """Test that verbose flag is recognized."""
        result = self.runner.invoke(cli, ["--verbose", "--help"])
        self.assertEqual(result.exit_code, 0)

    def test_sign_command_help(self):
        """Test sign command help."""
        result = self.runner.invoke(cli, ["sign", "--help"])
        self.assertEqual(result.exit_code, 0)
        self.assertIn("Sign the FSBL image with the provided keys", result.output)

    def test_verify_command_help(self):
        """Test verify command help."""
        result = self.runner.invoke(cli, ["verify", "--help"])
        self.assertEqual(result.exit_code, 0)
        self.assertIn("Verify the FSBL image signature", result.output)

    def test_gen_pk_hashes_command_help(self):
        """Test gen-pk-hashes command help."""
        result = self.runner.invoke(cli, ["gen-pk-hashes", "--help"])
        self.assertEqual(result.exit_code, 0)
        self.assertIn("Generate public key hashes", result.output)

    def test_dump_command_help(self):
        """Test dump command help."""
        result = self.runner.invoke(cli, ["dump", "--help"])
        self.assertEqual(result.exit_code, 0)
        self.assertIn("Dump STM32 FSBL header contents", result.output)

    def test_sign_missing_required_args(self):
        """Test sign command with missing required arguments."""
        result = self.runner.invoke(cli, ["sign"])
        self.assertNotEqual(result.exit_code, 0)
        self.assertIn("Missing option", result.output)

    def test_sign_missing_input_file(self):
        """Test sign command with non-existent input file."""
        result = self.runner.invoke(
            cli,
            [
                "sign",
                "--input",
                "/nonexistent/file.bin",
                "--output",
                str(self.mock_output_file),
                "--sk",
                str(self.mock_private_key),
                "--pk",
                str(self.mock_public_key1),
                "--product",
                "C2",
            ],
            input="password\n",
        )
        self.assertNotEqual(result.exit_code, 0)

    def test_sign_missing_private_key(self):
        """Test sign command with non-existent private key."""
        result = self.runner.invoke(
            cli,
            [
                "sign",
                "--input",
                str(self.mock_input_file),
                "--output",
                str(self.mock_output_file),
                "--sk",
                "/nonexistent/key.pem",
                "--pk",
                str(self.mock_public_key1),
                "--product",
                "C2",
            ],
            input="password\n",
        )
        self.assertNotEqual(result.exit_code, 0)

    def test_sign_missing_public_key(self):
        """Test sign command with non-existent public key."""
        result = self.runner.invoke(
            cli,
            [
                "sign",
                "--input",
                str(self.mock_input_file),
                "--output",
                str(self.mock_output_file),
                "--sk",
                str(self.mock_private_key),
                "--pk",
                "/nonexistent/pubkey.pem",
                "--product",
                "C2",
            ],
            input="password\n",
        )
        self.assertNotEqual(result.exit_code, 0)

    def test_sign_invalid_product(self):
        """Test sign command with invalid product identifier."""
        result = self.runner.invoke(
            cli,
            [
                "sign",
                "--input",
                str(self.mock_input_file),
                "--output",
                str(self.mock_output_file),
                "--sk",
                str(self.mock_private_key),
                "--pk",
                str(self.mock_public_key1),
                "--product",
                "INVALID",
            ],
            input="password\n",
        )
        self.assertNotEqual(result.exit_code, 0)

    @patch("fsbl_signer.FSBLSigner")
    def test_sign_command_success_mock(self, mock_signer_class):
        """Test successful sign command execution with mocked FSBLSigner."""
        # Mock the FSBLSigner instance
        mock_signer = MagicMock()
        mock_signer_class.return_value = mock_signer
        mock_signer.signed = True

        result = self.runner.invoke(
            cli,
            [
                "sign",
                "--input",
                str(self.mock_input_file),
                "--output",
                str(self.mock_output_file),
                "--sk",
                str(self.mock_private_key),
                "--pk",
                str(self.mock_public_key1),
                "--product",
                "C2",
            ],
            input="password\n",
        )

        # Verify the command completed successfully
        self.assertEqual(result.exit_code, 0)

        # Verify FSBLSigner was called with correct parameters
        mock_signer_class.assert_called_once()
        mock_signer.pre_signing_operations.assert_called_once()
        mock_signer.compute_signature.assert_called_once()
        mock_signer.sign_image.assert_called_once()
        mock_signer.save_image.assert_called_once()

    @patch("fsbl_signer.FSBLSigner")
    def test_sign_command_with_multiple_public_keys(self, mock_signer_class):
        """Test sign command with multiple public keys."""
        mock_signer = MagicMock()
        mock_signer_class.return_value = mock_signer
        mock_signer.signed = True

        result = self.runner.invoke(
            cli,
            [
                "sign",
                "--input",
                str(self.mock_input_file),
                "--output",
                str(self.mock_output_file),
                "--sk",
                str(self.mock_private_key),
                "--pk",
                str(self.mock_public_key1),
                "--pk",
                str(self.mock_public_key2),
                "--product",
                "C3",
            ],
            input="password\n",
        )

        self.assertEqual(result.exit_code, 0)
        # Verify FSBLSigner was initialized with multiple public keys
        args, kwargs = mock_signer_class.call_args
        self.assertEqual(len(args[4]), 2)  # pk parameter should have 2 keys

    @patch("fsbl_signer.FSBLSigner")
    def test_sign_command_with_hash_output(self, mock_signer_class):
        """Test sign command with hash file output."""
        mock_signer = MagicMock()
        mock_signer_class.return_value = mock_signer
        mock_signer.signed = True

        result = self.runner.invoke(
            cli,
            [
                "sign",
                "--input",
                str(self.mock_input_file),
                "--output",
                str(self.mock_output_file),
                "--sk",
                str(self.mock_private_key),
                "--pk",
                str(self.mock_public_key1),
                "--product",
                "C2",
                "--pk-hashes-out",
                str(self.mock_hash_dir),
            ],
            input="password\n",
        )

        self.assertEqual(result.exit_code, 0)
        mock_signer.gen_hash_files.assert_called_once()

    def test_verify_missing_required_args(self):
        """Test verify command with missing required arguments."""
        result = self.runner.invoke(cli, ["verify"])
        self.assertNotEqual(result.exit_code, 0)
        self.assertIn("Missing option", result.output)

    def test_verify_missing_input_file(self):
        """Test verify command with non-existent input file."""
        result = self.runner.invoke(
            cli, ["verify", "--input", "/nonexistent/file.bin", "--pk", str(self.mock_public_key1), "--product", "C2"]
        )
        self.assertNotEqual(result.exit_code, 0)

    @patch("fsbl_signer.FSBLSigner")
    def test_verify_command_success(self, mock_signer_class):
        """Test successful verify command execution."""
        mock_signer = MagicMock()
        mock_signer_class.return_value = mock_signer

        # Mock the header and verification process
        mock_header = MagicMock()
        mock_header.auth_header = MagicMock()
        mock_header.base_header.image_signature = b"mock_signature"
        mock_header.auth_header.public_key_index = 0
        mock_header.auth_header.ecdsa_public_key = b"mock_public_key"
        mock_header.auth_header.public_key_hashes = [b"mock_hash"] * 8

        mock_signer._read_header.return_value = mock_header
        mock_signer.firmware = b"mock_firmware_data"
        mock_signer.get_raw_pubkey.return_value = b"mock_public_key"
        mock_signer.verify_signature_with_raw_pubkey.return_value = True
        mock_signer.pkh = [b"mock_hash"] * 8

        with self.assertLogs("fsbl_signer", level="INFO") as log:
            result = self.runner.invoke(
                cli,
                ["verify", "--input", str(self.mock_input_file), "--pk", str(self.mock_public_key1), "--product", "C2"],
            )

        self.assertEqual(result.exit_code, 0)
        # Check that success message was logged
        self.assertTrue(any("Signature verified successfully" in message for message in log.output))

    def test_gen_pk_hashes_missing_required_args(self):
        """Test gen-pk-hashes command with missing required arguments."""
        result = self.runner.invoke(cli, ["gen-pk-hashes"])
        self.assertNotEqual(result.exit_code, 0)
        self.assertIn("Missing option", result.output)

    @patch("fsbl_signer.FSBLSigner")
    def test_gen_pk_hashes_command_success(self, mock_signer_class):
        """Test successful gen-pk-hashes command execution."""
        mock_signer = MagicMock()
        mock_signer_class.return_value = mock_signer

        with self.assertLogs("fsbl_signer", level="INFO") as log:
            result = self.runner.invoke(
                cli,
                [
                    "gen-pk-hashes",
                    "--pk",
                    str(self.mock_public_key1),
                    "--pk",
                    str(self.mock_public_key2),
                    "--pk-hashes-out",
                    str(self.mock_hash_dir),
                ],
            )

        self.assertEqual(result.exit_code, 0)
        mock_signer.gen_hash_files.assert_called_once()
        # Check that success message was logged
        self.assertTrue(any("Hash generation completed successfully" in message for message in log.output))

    def test_dump_missing_required_args(self):
        """Test dump command with missing required arguments."""
        result = self.runner.invoke(cli, ["dump"])
        self.assertNotEqual(result.exit_code, 0)
        self.assertIn("Missing option", result.output)

    def test_dump_missing_input_file(self):
        """Test dump command with non-existent input file."""
        result = self.runner.invoke(cli, ["dump", "--input", "/nonexistent/file.bin"])
        self.assertNotEqual(result.exit_code, 0)

    @patch("fsbl_signer.STM32Header")
    @patch("builtins.open")
    def test_dump_command_success(self, mock_open, mock_header_class):
        """Test successful dump command execution."""
        # Mock file reading
        mock_file = MagicMock()
        mock_file.read.return_value = b"mock_header_data" * 100
        mock_open.return_value.__enter__.return_value = mock_file

        # Mock header unpacking
        mock_header = MagicMock()
        mock_header_class.unpack.return_value = mock_header

        # Mock header components
        mock_base_header = MagicMock()
        mock_base_header.magic = b"STM\x32"
        mock_base_header.image_signature = b"A" * 64
        mock_base_header.image_checksum = 0x12345678
        mock_base_header.header_version = 0x00020000
        mock_base_header.image_length = 1024
        mock_base_header.image_entry_point = 0x2FFC0000
        mock_base_header.reserved = b"\x00" * 12
        mock_base_header.version_number = 0
        mock_base_header.option_flags = 0x80000001
        mock_base_header.header_extensions_len = 384
        mock_base_header.padding = b"\x00" * 20

        mock_auth_header = MagicMock()
        mock_auth_header.extension_type = b"ST\x00\x02"
        mock_auth_header.extension_length = 340
        mock_auth_header.public_key_index = 0
        mock_auth_header.public_key_number = 8
        mock_auth_header.ecdsa_algorithm = 1
        mock_auth_header.ecdsa_public_key = b"B" * 64
        mock_auth_header.public_key_hashes = [b"C" * 32] * 8

        mock_padding_header = MagicMock()
        mock_padding_header.extension_type = b"ST\xff\xff"
        mock_padding_header.extension_length = 44
        mock_padding_header.padding = b"D" * 36

        mock_header.base_header = mock_base_header
        mock_header.auth_header = mock_auth_header
        mock_header.padding_header = mock_padding_header

        result = self.runner.invoke(cli, ["dump", "--input", str(self.mock_input_file)])

        self.assertEqual(result.exit_code, 0)
        self.assertIn("=== Base Header ===", result.output)
        self.assertIn("=== Authentication Header ===", result.output)
        self.assertIn("=== Padding Header ===", result.output)

    def test_command_order_in_help(self):
        """Test that commands appear in the specified order in help."""
        result = self.runner.invoke(cli, ["--help"])
        self.assertEqual(result.exit_code, 0)

        # Find positions of each command in the help output
        sign_pos = result.output.find("sign")
        verify_pos = result.output.find("verify")
        gen_pk_hashes_pos = result.output.find("gen-pk-hashes")
        dump_pos = result.output.find("dump")

        # Verify they appear in the expected order
        self.assertLess(sign_pos, verify_pos)
        self.assertLess(verify_pos, gen_pk_hashes_pos)
        self.assertLess(gen_pk_hashes_pos, dump_pos)

    def test_product_choices(self):
        """Test that product parameter only accepts valid choices."""
        # Test that help shows valid product choices
        result = self.runner.invoke(cli, ["sign", "--help"])
        self.assertEqual(result.exit_code, 0)
        self.assertIn("C2", result.output)
        self.assertIn("C3", result.output)

        # Invalid product choice is handled by Click automatically
        result = self.runner.invoke(
            cli,
            [
                "sign",
                "--input",
                str(self.mock_input_file),
                "--output",
                str(self.mock_output_file),
                "--sk",
                str(self.mock_private_key),
                "--pk",
                str(self.mock_public_key1),
                "--product",
                "INVALID_PRODUCT",
            ],
            input="password\n",
        )
        self.assertNotEqual(result.exit_code, 0)

    @patch("fsbl_signer.FSBLSigner")
    def test_sign_command_failure_handling(self, mock_signer_class):
        """Test sign command handles failures gracefully."""
        mock_signer = MagicMock()
        mock_signer_class.return_value = mock_signer
        mock_signer.signed = False  # Simulate signing failure

        with self.assertLogs("fsbl_signer", level="ERROR") as log:
            result = self.runner.invoke(
                cli,
                [
                    "sign",
                    "--input",
                    str(self.mock_input_file),
                    "--output",
                    str(self.mock_output_file),
                    "--sk",
                    str(self.mock_private_key),
                    "--pk",
                    str(self.mock_public_key1),
                    "--product",
                    "C2",
                ],
                input="password\n",
            )

        self.assertEqual(result.exit_code, 0)  # CLI doesn't exit with error, but logs failure
        # Check that failure message was logged
        self.assertTrue(any("Signing failed" in message for message in log.output))

    @patch("fsbl_signer.FSBLSigner")
    def test_sign_command_passwordless(self, mock_signer_class):
        """Test sign command without password for unencrypted keys."""
        mock_signer = MagicMock()
        mock_signer_class.return_value = mock_signer
        mock_signer.signed = True

        result = self.runner.invoke(
            cli,
            [
                "sign",
                "--input",
                str(self.mock_input_file),
                "--output",
                str(self.mock_output_file),
                "--sk",
                str(self.mock_private_key),
                "--pk",
                str(self.mock_public_key1),
                "--product",
                "C2",
            ],
            # No password input
        )

        self.assertEqual(result.exit_code, 0)
        # Verify FSBLSigner was called with pwd=None
        args, kwargs = mock_signer_class.call_args
        self.assertIsNone(args[3])  # pwd parameter should be None

    @patch("fsbl_signer.FSBLSigner")
    def test_sign_command_explicit_no_password(self, mock_signer_class):
        """Test sign command with empty password string (normalized to None)."""
        mock_signer = MagicMock()
        mock_signer_class.return_value = mock_signer
        mock_signer.signed = True

        result = self.runner.invoke(
            cli,
            [
                "sign",
                "--input",
                str(self.mock_input_file),
                "--output",
                str(self.mock_output_file),
                "--sk",
                str(self.mock_private_key),
                "--pk",
                str(self.mock_public_key1),
                "--product",
                "C2",
                "--pwd",
                "",  # Explicit empty password
            ],
        )

        self.assertEqual(result.exit_code, 0)
        # Verify FSBLSigner was called with pwd=None (empty string normalized)
        args, kwargs = mock_signer_class.call_args
        self.assertIsNone(args[3], "Empty password string should be normalized to None")

    @patch("fsbl_signer.FSBLSigner")
    def test_verify_signature_failure(self, mock_signer_class):
        """Test verify command when signature verification fails."""
        mock_signer = MagicMock()
        mock_signer_class.return_value = mock_signer

        # Mock header with auth header but failed verification
        mock_header = MagicMock()
        mock_header.auth_header = MagicMock()
        mock_header.base_header.image_signature = b"mock_signature"
        mock_header.auth_header.public_key_index = 0
        mock_header.auth_header.ecdsa_public_key = b"mock_public_key"

        mock_signer._read_header.return_value = mock_header
        mock_signer.firmware = b"mock_firmware_data"
        mock_signer.get_raw_pubkey.return_value = b"mock_public_key"
        mock_signer.verify_signature_with_raw_pubkey.return_value = False  # Verification fails

        with self.assertLogs("fsbl_signer", level="ERROR") as log:
            result = self.runner.invoke(
                cli,
                ["verify", "--input", str(self.mock_input_file), "--pk", str(self.mock_public_key1), "--product", "C2"],
            )

        self.assertEqual(result.exit_code, 1)
        # Check that failure message was logged
        self.assertTrue(any("Signature verification failed" in message for message in log.output))

    @patch("fsbl_signer.FSBLSigner")
    def test_verify_no_auth_header(self, mock_signer_class):
        """Test verify command when no authentication header is found."""
        mock_signer = MagicMock()
        mock_signer_class.return_value = mock_signer

        # Mock header without auth header
        mock_header = MagicMock()
        mock_header.auth_header = None
        mock_signer._read_header.return_value = mock_header
        mock_signer.firmware = b"mock_firmware_data"

        with self.assertLogs("fsbl_signer", level="ERROR") as log:
            result = self.runner.invoke(
                cli,
                ["verify", "--input", str(self.mock_input_file), "--pk", str(self.mock_public_key1), "--product", "C2"],
            )

        self.assertEqual(result.exit_code, 1)
        # Check that error message was logged
        self.assertTrue(any("no authentication header found" in message for message in log.output))


if __name__ == "__main__":
    unittest.main()

import tempfile
import unittest
from pathlib import Path
from unittest.mock import Mock, patch

from click.testing import CliRunner

from cot_signer.cli.cli import cli


class TestCLICommands(unittest.TestCase):
    """Test suite for cot_signer CLI commands."""

    def setUp(self):
        """Set up test fixtures."""
        self.runner = CliRunner()
        self.test_dir = Path(tempfile.mkdtemp())

        # Create test files
        self.test_cert = self.test_dir / "test.crt"
        self.test_pk = self.test_dir / "test_pk.pem"
        self.test_sk = self.test_dir / "test_sk.pem"
        self.test_fw_config = self.test_dir / "fw_config.bin"
        self.test_hw_config = self.test_dir / "hw_config.bin"
        self.test_firmware = self.test_dir / "firmware.bin"

        # Create dummy files
        for file_path in [
            self.test_cert,
            self.test_pk,
            self.test_sk,
            self.test_fw_config,
            self.test_hw_config,
            self.test_firmware,
        ]:
            file_path.write_bytes(b"dummy content")

    def tearDown(self):
        """Clean up test fixtures."""
        import shutil

        shutil.rmtree(self.test_dir)

    def test_cli_help(self):
        """Test main CLI help command."""
        result = self.runner.invoke(cli, ["--help"])
        self.assertEqual(result.exit_code, 0)
        self.assertIn("Chain of Trust re-signer", result.output)
        self.assertIn("print-cert", result.output)
        self.assertIn("sign-root-certs", result.output)
        self.assertIn("sign-intermediate-certs", result.output)
        self.assertIn("sign-leaf-certs", result.output)
        self.assertIn("verify-cot", result.output)

    def test_cli_verbose_flag(self):
        """Test verbose flag functionality."""
        # Test that verbose flag is accepted and help works
        result = self.runner.invoke(cli, ["--verbose", "--help"])
        self.assertEqual(result.exit_code, 0)
        self.assertIn("Chain of Trust re-signer", result.output)

        # Test that verbose flag is properly defined
        result = self.runner.invoke(cli, ["--help"])
        self.assertEqual(result.exit_code, 0)
        self.assertIn("--verbose", result.output)
        self.assertIn("-v", result.output)
        self.assertIn("Enable verbose logs", result.output)

    def test_command_ordering(self):
        """Test that commands are displayed in the correct order."""
        result = self.runner.invoke(cli, ["--help"])
        output_lines = result.output.split("\n")
        commands_section = False
        command_order = []

        for line in output_lines:
            if "Commands:" in line:
                commands_section = True
                continue
            if commands_section and line.strip():
                if line.startswith("  "):
                    command_name = line.strip().split()[0]
                    command_order.append(command_name)

        expected_order = [
            "print-cert",
            "sign-root-certs",
            "sign-intermediate-certs",
            "sign-leaf-certs",
            "sign-leaf-certs-external",
            "verify-cot",
        ]

        # Check that the first few commands match expected order
        for i, expected_cmd in enumerate(expected_order):
            if i < len(command_order):
                self.assertEqual(command_order[i], expected_cmd)


class TestPrintCertCommand(unittest.TestCase):
    """Test suite for print-cert command."""

    def setUp(self):
        """Set up test fixtures."""
        self.runner = CliRunner()
        self.test_dir = Path(tempfile.mkdtemp())
        self.test_cert = self.test_dir / "test.crt"
        self.test_pk = self.test_dir / "test_pk.pem"

        # Create dummy files
        self.test_cert.write_bytes(b"dummy cert content")
        self.test_pk.write_bytes(b"dummy pk content")

    def tearDown(self):
        """Clean up test fixtures."""
        import shutil

        shutil.rmtree(self.test_dir)

    def test_print_cert_help(self):
        """Test print-cert help command."""
        result = self.runner.invoke(cli, ["print-cert", "--help"])
        self.assertEqual(result.exit_code, 0)
        self.assertIn("Print certificate contents", result.output)
        self.assertIn("--cert", result.output)
        self.assertIn("--cert-pk", result.output)
        self.assertIn("--security", result.output)

    def test_print_cert_missing_required_args(self):
        """Test print-cert with missing required arguments."""
        result = self.runner.invoke(cli, ["print-cert"])
        self.assertNotEqual(result.exit_code, 0)
        self.assertIn("Missing option", result.output)

    def test_print_cert_invalid_security_world(self):
        """Test print-cert with invalid security world."""
        result = self.runner.invoke(cli, ["print-cert", "--cert", str(self.test_cert), "--security", "INVALID"])
        self.assertNotEqual(result.exit_code, 0)

    def test_print_cert_nonexistent_file(self):
        """Test print-cert with non-existent certificate file."""
        result = self.runner.invoke(cli, ["print-cert", "--cert", "nonexistent.crt", "--security", "TRUSTED"])
        self.assertNotEqual(result.exit_code, 0)

    @patch("cot_signer.cli.cli.ChainOfTrustCertManager")
    def test_print_cert_valid_args(self, mock_cert_manager):
        """Test print-cert with valid arguments."""
        mock_instance = Mock()
        mock_cert_manager.return_value = mock_instance
        mock_instance.cert = Mock()
        mock_instance.get_ecc_public_key.return_value = Mock()
        mock_instance.get_raw_public_key_bytes.return_value = b"x" * 64
        mock_instance.verify_self_signed.return_value = True
        mock_instance.print_certificate_info = Mock()

        result = self.runner.invoke(
            cli, ["print-cert", "--cert", str(self.test_cert), "--cert-pk", str(self.test_pk), "--security", "TRUSTED"]
        )

        self.assertEqual(result.exit_code, 0)
        mock_cert_manager.assert_called_once()
        mock_instance.print_certificate_info.assert_called_once()


class TestSignRootCertsCommand(unittest.TestCase):
    """Test suite for sign-root-certs command."""

    def setUp(self):
        """Set up test fixtures."""
        self.runner = CliRunner()
        self.test_dir = Path(tempfile.mkdtemp())

        # Create test files
        self.test_files = {
            "rot_key_pk": self.test_dir / "rot_key.pub",
            "rot_key_sk": self.test_dir / "rot_key.pem",
            "fw_config": self.test_dir / "fw_config.bin",
            "hw_config": self.test_dir / "hw_config.bin",
            "trusted_key_cert": self.test_dir / "trusted_key.crt",
            "stm32mp_cfg_cert": self.test_dir / "stm32mp_cfg.crt",
            "tb_fw_cert": self.test_dir / "tb_fw.crt",
            "trusted_world_key_pk": self.test_dir / "trusted_world.pub",
            "non_trusted_world_key_pk": self.test_dir / "non_trusted_world.pub",
        }

        for file_path in self.test_files.values():
            file_path.write_bytes(b"dummy content")

    def tearDown(self):
        """Clean up test fixtures."""
        import shutil

        shutil.rmtree(self.test_dir)

    def test_sign_root_certs_help(self):
        """Test sign-root-certs help command."""
        result = self.runner.invoke(cli, ["sign-root-certs", "--help"])
        self.assertEqual(result.exit_code, 0)
        self.assertIn("Sign root level certificates", result.output)
        self.assertIn("--rot-key-pk", result.output)
        self.assertIn("--product", result.output)

    def test_sign_root_certs_missing_product(self):
        """Test sign-root-certs with missing product argument."""
        result = self.runner.invoke(
            cli,
            [
                "sign-root-certs",
                "--rot-key-pk",
                str(self.test_files["rot_key_pk"]),
                "--rot-key-sk",
                str(self.test_files["rot_key_sk"]),
            ],
        )
        self.assertNotEqual(result.exit_code, 0)
        self.assertIn("Missing option", result.output)

    def test_sign_root_certs_invalid_product(self):
        """Test sign-root-certs with invalid product."""
        result = self.runner.invoke(
            cli,
            [
                "sign-root-certs",
                "--rot-key-pk",
                str(self.test_files["rot_key_pk"]),
                "--rot-key-sk",
                str(self.test_files["rot_key_sk"]),
                "--product",
                "INVALID",
            ],
        )
        self.assertNotEqual(result.exit_code, 0)

    def test_sign_root_certs_nonexistent_files(self):
        """Test sign-root-certs with non-existent files."""
        result = self.runner.invoke(
            cli,
            [
                "sign-root-certs",
                "--rot-key-pk",
                "nonexistent.pub",
                "--rot-key-sk",
                "nonexistent.pem",
                "--product",
                "C2",
            ],
        )
        self.assertNotEqual(result.exit_code, 0)

    @patch("cot_signer.cli.cli.ChainOfTrustCertManager")
    def test_sign_root_certs_stm32mp_cfg_partial_args(self, mock_cert_manager):
        """Test sign-root-certs with partial STM32MP CFG cert arguments."""
        result = self.runner.invoke(
            cli,
            [
                "sign-root-certs",
                "--rot-key-pk",
                str(self.test_files["rot_key_pk"]),
                "--rot-key-sk",
                str(self.test_files["rot_key_sk"]),
                "--fw-config",
                str(self.test_files["fw_config"]),
                # Missing hw-config and stm32mp-cfg-cert
                "--product",
                "C2",
            ],
            input="testpass\n",
        )

        self.assertNotEqual(result.exit_code, 0)
        self.assertIn("Missing a required argument", str(result.exception))

    @patch("cot_signer.cli.cli.ChainOfTrustCertManager")
    def test_sign_root_certs_trusted_key_partial_args(self, mock_cert_manager):
        """Test sign-root-certs with partial trusted key cert arguments."""
        result = self.runner.invoke(
            cli,
            [
                "sign-root-certs",
                "--rot-key-pk",
                str(self.test_files["rot_key_pk"]),
                "--rot-key-sk",
                str(self.test_files["rot_key_sk"]),
                "--trusted-world-key-pk",
                str(self.test_files["trusted_world_key_pk"]),
                # Missing non-trusted-world-key-pk and trusted-key-cert
                "--product",
                "C2",
            ],
            input="testpass\n",
        )

        self.assertNotEqual(result.exit_code, 0)
        self.assertIn("Missing a required argument", str(result.exception))


class TestSignIntermediateCertsCommand(unittest.TestCase):
    """Test suite for sign-intermediate-certs command."""

    def setUp(self):
        """Set up test fixtures."""
        self.runner = CliRunner()
        self.test_dir = Path(tempfile.mkdtemp())

        # Create test files
        self.test_files = {
            "trusted_world_key_pk": self.test_dir / "trusted_world.pub",
            "trusted_world_key_sk": self.test_dir / "trusted_world.pem",
            "non_trusted_world_key_pk": self.test_dir / "non_trusted_world.pub",
            "non_trusted_world_key_sk": self.test_dir / "non_trusted_world.pem",
            "tos_fw_key_pk": self.test_dir / "tos_fw_key.pub",
            "nt_fw_key_pk": self.test_dir / "nt_fw_key.pub",
            "tos_fw_key_cert": self.test_dir / "tos_fw_key.crt",
            "nt_fw_key_cert": self.test_dir / "nt_fw_key.crt",
        }

        for file_path in self.test_files.values():
            file_path.write_bytes(b"dummy content")

    def tearDown(self):
        """Clean up test fixtures."""
        import shutil

        shutil.rmtree(self.test_dir)

    def test_sign_intermediate_certs_help(self):
        """Test sign-intermediate-certs help command."""
        result = self.runner.invoke(cli, ["sign-intermediate-certs", "--help"])
        self.assertEqual(result.exit_code, 0)
        self.assertIn("Sign intermediate level certificates", result.output)
        self.assertIn("--trusted-world-key-pk", result.output)
        self.assertIn("--product", result.output)

    def test_sign_intermediate_certs_missing_product(self):
        """Test sign-intermediate-certs with missing product."""
        result = self.runner.invoke(cli, ["sign-intermediate-certs"])
        self.assertNotEqual(result.exit_code, 0)
        self.assertIn("Missing option", result.output)

    @patch("cot_signer.cli.cli.ChainOfTrustCertManager")
    def test_sign_intermediate_certs_trusted_partial_args(self, mock_cert_manager):
        """Test sign-intermediate-certs with partial trusted world arguments."""
        result = self.runner.invoke(
            cli,
            [
                "sign-intermediate-certs",
                "--trusted-world-key-pk",
                str(self.test_files["trusted_world_key_pk"]),
                "--trusted-world-key-sk",
                str(self.test_files["trusted_world_key_sk"]),
                "--tos-fw-key-pk",
                str(self.test_files["tos_fw_key_pk"]),
                # Missing tos-fw-key-cert
                "--product",
                "C2",
            ],
            input="testpass\n",
        )

        self.assertNotEqual(result.exit_code, 0)
        self.assertIn("Missing a required argument", str(result.exception))


class TestSignLeafCertsCommand(unittest.TestCase):
    """Test suite for sign-leaf-certs command."""

    def setUp(self):
        """Set up test fixtures."""
        self.runner = CliRunner()
        self.test_dir = Path(tempfile.mkdtemp())

        # Create test files
        self.test_files = {
            "tos_fw_key_pk": self.test_dir / "tos_fw_key.pub",
            "tos_fw_key_sk": self.test_dir / "tos_fw_key.pem",
            "nt_fw_key_pk": self.test_dir / "nt_fw_key.pub",
            "nt_fw_key_sk": self.test_dir / "nt_fw_key.pem",
            "nt_fw": self.test_dir / "nt_fw.bin",
            "tos_fw": self.test_dir / "tos_fw.bin",
            "tos_fw_extra1": self.test_dir / "tos_fw_extra1.bin",
            "tos_fw_extra2": self.test_dir / "tos_fw_extra2.bin",
            "tos_fw_cert": self.test_dir / "tos_fw_content.crt",
            "nt_fw_cert": self.test_dir / "nt_fw_content.crt",
        }

        for file_path in self.test_files.values():
            file_path.write_bytes(b"dummy content")

    def tearDown(self):
        """Clean up test fixtures."""
        import shutil

        shutil.rmtree(self.test_dir)

    def test_sign_leaf_certs_help(self):
        """Test sign-leaf-certs help command."""
        result = self.runner.invoke(cli, ["sign-leaf-certs", "--help"])
        self.assertEqual(result.exit_code, 0)
        self.assertIn("Sign leaf level certificates", result.output)
        self.assertIn("--tos-fw-key-pk", result.output)
        self.assertIn("--product", result.output)

    def test_sign_leaf_certs_missing_product(self):
        """Test sign-leaf-certs with missing product."""
        result = self.runner.invoke(cli, ["sign-leaf-certs"])
        self.assertNotEqual(result.exit_code, 0)
        self.assertIn("Missing option", result.output)

    @patch("cot_signer.cli.cli.ChainOfTrustCertManager.is_key_encrypted")
    @patch("cot_signer.cli.cli.ChainOfTrustCertManager")
    def test_sign_leaf_certs_tos_fw_partial_args(self, mock_cert_manager, mock_is_encrypted):
        """Test sign-leaf-certs with partial TOS FW arguments."""
        mock_is_encrypted.return_value = False

        result = self.runner.invoke(
            cli,
            [
                "sign-leaf-certs",
                "--tos-fw-key-pk",
                str(self.test_files["tos_fw_key_pk"]),
                "--tos-fw-key-sk",
                str(self.test_files["tos_fw_key_sk"]),
                "--tos-fw",
                str(self.test_files["tos_fw"]),
                "--tos-fw-extra1",
                str(self.test_files["tos_fw_extra1"]),
                # Missing tos-fw-extra2 and tos-fw-cert
                "--product",
                "C2",
            ],
        )

        self.assertNotEqual(result.exit_code, 0)
        self.assertIn("Missing a required argument", str(result.exception))


class TestSignLeafCertsExternalCommand(unittest.TestCase):
    """Test suite for sign-leaf-certs-external command."""

    def setUp(self):
        """Set up test fixtures."""
        self.runner = CliRunner()
        self.test_dir = Path(tempfile.mkdtemp())

        # Create test files
        self.test_files = {
            "tos_fw_key_pk": self.test_dir / "tos_fw_key.pub",
            "nt_fw_key_pk": self.test_dir / "nt_fw_key.pub",
            "nt_fw": self.test_dir / "nt_fw.bin",
            "tos_fw": self.test_dir / "tos_fw.bin",
            "tos_fw_extra1": self.test_dir / "tos_fw_extra1.bin",
            "tos_fw_extra2": self.test_dir / "tos_fw_extra2.bin",
            "tos_fw_cert": self.test_dir / "tos_fw_content.crt",
            "nt_fw_cert": self.test_dir / "nt_fw_content.crt",
        }

        for file_path in self.test_files.values():
            file_path.write_bytes(b"dummy content")

    def tearDown(self):
        """Clean up test fixtures."""
        import shutil

        shutil.rmtree(self.test_dir)

    def test_sign_leaf_certs_external_help(self):
        """Test sign-leaf-certs-external help command."""
        result = self.runner.invoke(cli, ["sign-leaf-certs-external", "--help"])
        self.assertEqual(result.exit_code, 0)
        self.assertIn("Sign leaf level certificates with an external", result.output)
        self.assertIn("--env", result.output)
        self.assertIn("--sign-provider", result.output)

    def test_sign_leaf_certs_external_missing_required_args(self):
        """Test sign-leaf-certs-external with missing required arguments."""
        result = self.runner.invoke(cli, ["sign-leaf-certs-external"])
        self.assertNotEqual(result.exit_code, 0)
        self.assertIn("Missing option", result.output)

    def test_sign_leaf_certs_external_invalid_env(self):
        """Test sign-leaf-certs-external with invalid environment."""
        result = self.runner.invoke(
            cli,
            ["sign-leaf-certs-external", "--product", "C2", "--env", "INVALID", "--sign-provider", "HASHICORP_VAULT"],
        )
        self.assertNotEqual(result.exit_code, 0)

    def test_sign_leaf_certs_external_invalid_provider(self):
        """Test sign-leaf-certs-external with invalid signing provider."""
        result = self.runner.invoke(
            cli, ["sign-leaf-certs-external", "--product", "C2", "--env", "LOCAL", "--sign-provider", "INVALID"]
        )
        self.assertNotEqual(result.exit_code, 0)


class TestVerifyCotCommand(unittest.TestCase):
    """Test suite for verify-cot command."""

    def setUp(self):
        """Set up test fixtures."""
        self.runner = CliRunner()
        self.test_dir = Path(tempfile.mkdtemp())
        self.certs_dir = self.test_dir / "certs"
        self.certs_dir.mkdir()

        # Create test files
        self.test_files = {
            "rot_key_pk": self.test_dir / "rot_key.pub",
            "fw_config": self.test_dir / "fw_config.bin",
            "hw_config": self.test_dir / "hw_config.bin",
            "nt_fw": self.test_dir / "nt_fw.bin",
            "tos_fw": self.test_dir / "tos_fw.bin",
            "tos_fw_extra1": self.test_dir / "tos_fw_extra1.bin",
            "tos_fw_extra2": self.test_dir / "tos_fw_extra2.bin",
        }

        for file_path in self.test_files.values():
            file_path.write_bytes(b"dummy content")

    def tearDown(self):
        """Clean up test fixtures."""
        import shutil

        shutil.rmtree(self.test_dir)

    def test_verify_cot_help(self):
        """Test verify-cot help command."""
        result = self.runner.invoke(cli, ["verify-cot", "--help"])
        self.assertEqual(result.exit_code, 0)
        self.assertIn("Verify the CoT signatures", result.output)
        self.assertIn("--rot-key-pk", result.output)
        self.assertIn("--certs-dir", result.output)

    def test_verify_cot_missing_required_args(self):
        """Test verify-cot with missing required arguments."""
        result = self.runner.invoke(cli, ["verify-cot"])
        self.assertNotEqual(result.exit_code, 0)
        self.assertIn("Missing option", result.output)

    def test_verify_cot_nonexistent_certs_dir(self):
        """Test verify-cot with non-existent certificates directory."""
        result = self.runner.invoke(
            cli,
            [
                "verify-cot",
                "--rot-key-pk",
                str(self.test_files["rot_key_pk"]),
                "--fw-config",
                str(self.test_files["fw_config"]),
                "--hw-config",
                str(self.test_files["hw_config"]),
                "--nt-fw",
                str(self.test_files["nt_fw"]),
                "--tos-fw",
                str(self.test_files["tos_fw"]),
                "--tos-fw-extra1",
                str(self.test_files["tos_fw_extra1"]),
                "--tos-fw-extra2",
                str(self.test_files["tos_fw_extra2"]),
                "--certs-dir",
                "nonexistent_dir",
                "--product",
                "C2",
            ],
        )
        self.assertNotEqual(result.exit_code, 0)

    @patch("cot_signer.cli.cli.verify_cot_without_leaf")
    @patch("cot_signer.cli.cli.verify_tos_fw_content_cert")
    @patch("cot_signer.cli.cli.verify_nt_fw_content_cert")
    def test_verify_cot_valid_args(self, mock_verify_nt, mock_verify_tos, mock_verify_without_leaf):
        """Test verify-cot with valid arguments."""
        mock_verify_without_leaf.return_value = (b"trusted_key", b"non_trusted_key")
        mock_verify_tos.return_value = None
        mock_verify_nt.return_value = None

        result = self.runner.invoke(
            cli,
            [
                "verify-cot",
                "--rot-key-pk",
                str(self.test_files["rot_key_pk"]),
                "--fw-config",
                str(self.test_files["fw_config"]),
                "--hw-config",
                str(self.test_files["hw_config"]),
                "--nt-fw",
                str(self.test_files["nt_fw"]),
                "--tos-fw",
                str(self.test_files["tos_fw"]),
                "--tos-fw-extra1",
                str(self.test_files["tos_fw_extra1"]),
                "--tos-fw-extra2",
                str(self.test_files["tos_fw_extra2"]),
                "--certs-dir",
                str(self.certs_dir),
                "--product",
                "C2",
            ],
        )

        self.assertEqual(result.exit_code, 0)
        mock_verify_without_leaf.assert_called_once()
        mock_verify_tos.assert_called_once()
        mock_verify_nt.assert_called_once()


class TestCLIIntegration(unittest.TestCase):
    """Integration tests for CLI commands."""

    def setUp(self):
        """Set up test fixtures."""
        self.runner = CliRunner()

    def test_all_commands_have_help(self):
        """Test that all commands have help text."""
        # Get list of commands
        result = self.runner.invoke(cli, ["--help"])
        self.assertEqual(result.exit_code, 0)

        # Extract command names
        lines = result.output.split("\n")
        commands = []
        in_commands_section = False

        for line in lines:
            if "Commands:" in line:
                in_commands_section = True
                continue
            if in_commands_section and line.strip():
                if line.startswith("  "):
                    command_name = line.strip().split()[0]
                    commands.append(command_name)

        # Test help for each command
        for command in commands:
            result = self.runner.invoke(cli, [command, "--help"])
            self.assertEqual(result.exit_code, 0, f"Help failed for command: {command}")
            self.assertIn("help", result.output.lower(), f"No help text for command: {command}")

    def test_product_enum_validation(self):
        """Test that Product enum validation works across commands."""
        commands_with_product = [
            "sign-root-certs",
            "sign-intermediate-certs",
            "sign-leaf-certs",
            "sign-leaf-certs-external",
            "verify-cot",
        ]

        for command in commands_with_product:
            result = self.runner.invoke(cli, [command, "--product", "INVALID"])
            self.assertNotEqual(result.exit_code, 0, f"Invalid product accepted for command: {command}")

    def test_environment_enum_validation(self):
        """Test that Environment enum validation works for external signing."""
        result = self.runner.invoke(
            cli,
            ["sign-leaf-certs-external", "--env", "INVALID", "--product", "C2", "--sign-provider", "HASHICORP_VAULT"],
        )
        self.assertNotEqual(result.exit_code, 0)

    def test_signing_provider_enum_validation(self):
        """Test that SigningProvider enum validation works for external signing."""
        result = self.runner.invoke(
            cli, ["sign-leaf-certs-external", "--env", "LOCAL", "--product", "C2", "--sign-provider", "INVALID"]
        )
        self.assertNotEqual(result.exit_code, 0)


class TestValidateNvCounter(unittest.TestCase):
    """Test suite for validate_nv_counter function."""

    def test_validate_nv_counter_trusted_match(self):
        """Test NV counter validation passes when trusted certificate matches expected value."""
        from cot_signer.cli.cli import validate_nv_counter
        from cot_signer.constants import TRUSTED_NVCOUNTER_OID, TRUSTED_NV_COUNTER, Product, SecurityWorld

        # C3 expects nv_counter=2
        extension_data = {TRUSTED_NVCOUNTER_OID: 2}

        # Should not raise
        validate_nv_counter("test_cert.crt", extension_data, SecurityWorld.TRUSTED, Product.C3)

    def test_validate_nv_counter_non_trusted_match(self):
        """Test NV counter validation passes when non-trusted certificate matches expected value."""
        from cot_signer.cli.cli import validate_nv_counter
        from cot_signer.constants import NON_TRUSTED_NVCOUNTER_OID, NON_TRUSTED_NV_COUNTER, Product, SecurityWorld

        # C3 expects nv_counter=2
        extension_data = {NON_TRUSTED_NVCOUNTER_OID: 2}

        # Should not raise
        validate_nv_counter("test_cert.crt", extension_data, SecurityWorld.NON_TRUSTED, Product.C3)

    def test_validate_nv_counter_trusted_mismatch(self):
        """Test NV counter validation fails when trusted certificate has wrong value."""
        from cot_signer.cli.cli import validate_nv_counter
        from cot_signer.constants import TRUSTED_NVCOUNTER_OID, Product, SecurityWorld

        # C3 expects nv_counter=2, but cert has 0
        extension_data = {TRUSTED_NVCOUNTER_OID: 0}

        with self.assertRaises(ValueError) as ctx:
            validate_nv_counter("test_cert.crt", extension_data, SecurityWorld.TRUSTED, Product.C3)

        error_msg = str(ctx.exception)
        self.assertIn("NV Counter mismatch", error_msg)
        self.assertIn("nv_counter=0", error_msg)
        self.assertIn("expected nv_counter=2", error_msg)
        self.assertIn("C3", error_msg)

    def test_validate_nv_counter_non_trusted_mismatch(self):
        """Test NV counter validation fails when non-trusted certificate has wrong value."""
        from cot_signer.cli.cli import validate_nv_counter
        from cot_signer.constants import NON_TRUSTED_NVCOUNTER_OID, Product, SecurityWorld

        # C3 expects nv_counter=2, but cert has 0
        extension_data = {NON_TRUSTED_NVCOUNTER_OID: 0}

        with self.assertRaises(ValueError) as ctx:
            validate_nv_counter("test_cert.crt", extension_data, SecurityWorld.NON_TRUSTED, Product.C3)

        error_msg = str(ctx.exception)
        self.assertIn("NV Counter mismatch", error_msg)
        self.assertIn("nv_counter=0", error_msg)
        self.assertIn("expected nv_counter=2", error_msg)

    def test_validate_nv_counter_missing_trusted(self):
        """Test NV counter validation fails when trusted certificate is missing NV counter."""
        from cot_signer.cli.cli import validate_nv_counter
        from cot_signer.constants import Product, SecurityWorld

        # Empty extension data (missing NV counter)
        extension_data = {}

        with self.assertRaises(ValueError) as ctx:
            validate_nv_counter("test_cert.crt", extension_data, SecurityWorld.TRUSTED, Product.C3)

        error_msg = str(ctx.exception)
        self.assertIn("Missing", error_msg)
        self.assertIn("Trusted NV Counter", error_msg)
        self.assertIn("rollback protection", error_msg)

    def test_validate_nv_counter_missing_non_trusted(self):
        """Test NV counter validation fails when non-trusted certificate is missing NV counter."""
        from cot_signer.cli.cli import validate_nv_counter
        from cot_signer.constants import Product, SecurityWorld

        # Empty extension data (missing NV counter)
        extension_data = {}

        with self.assertRaises(ValueError) as ctx:
            validate_nv_counter("test_cert.crt", extension_data, SecurityWorld.NON_TRUSTED, Product.C3)

        error_msg = str(ctx.exception)
        self.assertIn("Missing", error_msg)
        self.assertIn("Non-Trusted NV Counter", error_msg)
        self.assertIn("rollback protection", error_msg)

    def test_validate_nv_counter_c2_product(self):
        """Test NV counter validation works for C2 product (expects nv_counter=0)."""
        from cot_signer.cli.cli import validate_nv_counter
        from cot_signer.constants import TRUSTED_NVCOUNTER_OID, Product, SecurityWorld

        # C2 expects nv_counter=0
        extension_data = {TRUSTED_NVCOUNTER_OID: 0}

        # Should not raise
        validate_nv_counter("test_cert.crt", extension_data, SecurityWorld.TRUSTED, Product.C2)

    def test_validate_nv_counter_unknown_security_world(self):
        """Test NV counter validation fails with unknown security world value."""
        from cot_signer.cli.cli import validate_nv_counter
        from cot_signer.constants import TRUSTED_NVCOUNTER_OID, Product

        extension_data = {TRUSTED_NVCOUNTER_OID: 2}

        # Pass an invalid security world value (simulating a bug or future enum value)
        with self.assertRaises(ValueError) as ctx:
            validate_nv_counter("test_cert.crt", extension_data, "INVALID", Product.C3)

        error_msg = str(ctx.exception)
        self.assertIn("Unknown security world", error_msg)


if __name__ == "__main__":
    unittest.main()

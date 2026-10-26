#!/usr/bin/env python3


# Import the classes to test
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

sys.path.insert(0, str(Path(__file__).parent.parent))
from fsbl_signer import (
    ECDSA_ALGO,
    HEADER_AUTH_LEN,
    HEADER_BASE_LEN,
    HEADER_EXT_TOTAL_LEN,
    HEADER_PADDING_LEN,
    HEADER_PADDING_PROTO_PREFIX_LEN,
    HEADER_TOTAL_LEN,
    PADDING_METADATA_LEN,
    PUBKEY_COUNT,
    FSBLSigner,
    STM32AuthHeader,
    STM32BaseHeader,
    STM32Header,
    STM32PaddingHeader,
)


class TestSTM32Headers(unittest.TestCase):
    """Test suite for STM32 header classes."""

    def test_base_header_pack_unpack(self):
        """Test STM32BaseHeader pack/unpack functionality."""
        # Create a base header
        header = STM32BaseHeader(
            magic=b"STM\x32",
            image_signature=b"A" * 64,  # Exactly 64 bytes
            image_checksum=0x12345678,
            header_version=0x00020000,
            image_length=1024,
            image_entry_point=0x2FFC0000,
            reserved=b"\x00" * 12,
            version_number=0,
            option_flags=0x80000001,
            header_extensions_len=384,
            padding=b"\x00" * 20,
        )

        # Pack and unpack
        packed = header.pack()
        self.assertEqual(len(packed), HEADER_BASE_LEN)

        unpacked = STM32BaseHeader.unpack(packed)

        # Verify all fields match
        self.assertEqual(unpacked.magic, header.magic)
        self.assertEqual(unpacked.image_signature, header.image_signature)
        self.assertEqual(unpacked.image_checksum, header.image_checksum)
        self.assertEqual(unpacked.header_version, header.header_version)
        self.assertEqual(unpacked.image_length, header.image_length)
        self.assertEqual(unpacked.image_entry_point, header.image_entry_point)
        self.assertEqual(unpacked.reserved, header.reserved)
        self.assertEqual(unpacked.version_number, header.version_number)
        self.assertEqual(unpacked.option_flags, header.option_flags)
        self.assertEqual(unpacked.header_extensions_len, header.header_extensions_len)
        self.assertEqual(unpacked.padding, header.padding)

    def test_base_header_invalid_length(self):
        """Test STM32BaseHeader with invalid data length."""
        with self.assertRaisesRegex(ValueError, "Base header must be 128 bytes"):
            STM32BaseHeader.unpack(b"too_short")

    def test_auth_header_pack_unpack(self):
        """Test STM32AuthHeader pack/unpack functionality."""
        # Create an auth header
        header = STM32AuthHeader(
            extension_type=b"ST\x00\x02",
            extension_length=340,
            public_key_index=0,
            public_key_number=8,
            ecdsa_algorithm=1,
            ecdsa_public_key=b"B" * 64,  # Exactly 64 bytes
            public_key_hashes=[b"C" * 32] * 8,  # 8 * 32 bytes each
        )

        # Pack and unpack
        packed = header.pack()
        self.assertEqual(len(packed), HEADER_AUTH_LEN)

        unpacked = STM32AuthHeader.unpack(packed)

        # Verify all fields match
        self.assertEqual(unpacked.extension_type, header.extension_type)
        self.assertEqual(unpacked.extension_length, header.extension_length)
        self.assertEqual(unpacked.public_key_index, header.public_key_index)
        self.assertEqual(unpacked.public_key_number, header.public_key_number)
        self.assertEqual(unpacked.ecdsa_algorithm, header.ecdsa_algorithm)
        self.assertEqual(unpacked.ecdsa_public_key, header.ecdsa_public_key)
        self.assertEqual(len(unpacked.public_key_hashes), len(header.public_key_hashes))
        for i, hash_val in enumerate(unpacked.public_key_hashes):
            self.assertEqual(hash_val, header.public_key_hashes[i])

    def test_auth_header_invalid_length(self):
        """Test STM32AuthHeader with invalid data length."""
        with self.assertRaisesRegex(ValueError, "Auth header must be 340 bytes"):
            STM32AuthHeader.unpack(b"too_short")

    def test_padding_header_pack_unpack(self):
        """Test STM32PaddingHeader pack/unpack functionality."""
        # Create a padding header
        padding_data = b"padding_data" * 5  # 60 bytes
        header = STM32PaddingHeader(
            extension_type=b"ST\xff\xff",
            extension_length=44,
            padding=padding_data[:36],  # Adjust to fit extension_length - 8
        )

        # Pack and unpack
        packed = header.pack()
        unpacked = STM32PaddingHeader.unpack(packed)

        # Verify all fields match
        self.assertEqual(unpacked.extension_type, header.extension_type)
        self.assertEqual(unpacked.extension_length, header.extension_length)
        self.assertEqual(unpacked.padding, header.padding)

    def test_complete_header_pack_unpack(self):
        """Test complete STM32Header pack/unpack functionality."""
        # Create complete header components
        base_header = STM32BaseHeader(
            magic=b"STM\x32",
            image_signature=b"A" * 64,
            image_checksum=0x12345678,
            header_version=0x00020000,
            image_length=1024,
            image_entry_point=0x2FFC0000,
            reserved=b"\x00" * 12,
            version_number=0,
            option_flags=0x80000001,
            header_extensions_len=HEADER_EXT_TOTAL_LEN,
            padding=b"\x00" * 20,
        )

        auth_header = STM32AuthHeader(
            extension_type=b"ST\x00\x02",
            extension_length=340,
            public_key_index=0,
            public_key_number=8,
            ecdsa_algorithm=1,
            ecdsa_public_key=b"B" * 64,
            public_key_hashes=[b"C" * 32] * 8,
        )

        padding_header = STM32PaddingHeader(
            extension_type=b"ST\xff\xff",
            extension_length=HEADER_PADDING_LEN,
            padding=b"D" * (HEADER_PADDING_LEN - PADDING_METADATA_LEN - HEADER_PADDING_PROTO_PREFIX_LEN),
            public_key_index=3,
        )

        header = STM32Header(
            base_header=base_header,
            auth_header=auth_header,
            padding_header=padding_header,
        )

        packed = header.pack()
        self.assertEqual(len(packed), HEADER_TOTAL_LEN)

        unpacked = STM32Header.unpack(packed)
        self.assertIsNotNone(unpacked.base_header)
        self.assertIsNotNone(unpacked.auth_header)
        self.assertIsNotNone(unpacked.padding_header)
        self.assertEqual(unpacked.padding_header.public_key_index, 3)

    def test_complete_header_without_auth(self):
        """Test complete STM32Header without authentication header."""
        padding_ext_len = HEADER_PADDING_LEN + HEADER_AUTH_LEN
        base_header = STM32BaseHeader(
            magic=b"STM\x32",
            image_signature=b"A" * 64,
            image_checksum=0x12345678,
            header_version=0x00020000,
            image_length=1024,
            image_entry_point=0x2FFC0000,
            reserved=b"\x00" * 12,
            version_number=0,
            option_flags=0x80000001,
            header_extensions_len=padding_ext_len,
            padding=b"\x00" * 20,
        )

        padding_header = STM32PaddingHeader(
            extension_type=b"ST\xff\xff",
            extension_length=padding_ext_len,
            padding=b"D" * (padding_ext_len - 8),
        )

        header = STM32Header(
            base_header=base_header,
            auth_header=None,
            padding_header=padding_header,
        )

        packed = header.pack()
        self.assertEqual(len(packed), HEADER_TOTAL_LEN)
        unpacked = STM32Header.unpack(packed)

        self.assertIsNotNone(unpacked.base_header)
        self.assertIsNone(unpacked.auth_header)
        self.assertIsNotNone(unpacked.padding_header)
        self.assertIsNone(unpacked.padding_header.public_key_index)

    def test_complete_header_invalid_length(self):
        """Test STM32Header with invalid total length."""
        with self.assertRaisesRegex(ValueError, f"Complete header must be {HEADER_TOTAL_LEN} bytes"):
            STM32Header.unpack(b"too_short")


class TestFSBLSigner(unittest.TestCase):
    """Test suite for FSBLSigner class."""

    def setUp(self):
        """Set up test fixtures."""
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

    def test_fsbl_signer_initialization(self):
        """Test FSBLSigner initialization."""
        signer = FSBLSigner(
            input=self.mock_input_file,
            output=self.mock_output_file,
            sk=self.mock_private_key,
            pwd="password",
            pk=[self.mock_public_key1, self.mock_public_key2],
            product="C2",
            pk_hashes_out=self.mock_hash_dir,
        )

        self.assertEqual(signer.input, self.mock_input_file)
        self.assertEqual(signer.output, self.mock_output_file)
        self.assertEqual(signer.sk, self.mock_private_key)
        self.assertEqual(signer.pwd, "password")
        self.assertEqual(signer.pk, [self.mock_public_key1, self.mock_public_key2])
        self.assertEqual(signer.product, "C2")
        self.assertEqual(signer.pk_hashes_out, self.mock_hash_dir)
        self.assertIsNotNone(signer.firmware)  # Should be loaded during init

    def test_fsbl_signer_no_input_file(self):
        """Test FSBLSigner initialization without input file."""
        signer = FSBLSigner(
            input=None,
            output=self.mock_output_file,
            sk=self.mock_private_key,
            pwd="password",
            pk=[self.mock_public_key1],
            product="C2",
            pk_hashes_out=self.mock_hash_dir,
        )

        self.assertIsNone(signer.firmware)

    def test_load_image_success(self):
        """Test successful image loading."""
        signer = FSBLSigner(input=None, output=None, sk=None, pwd=None, pk=[], product=None, pk_hashes_out=None)

        signer.input = self.mock_input_file
        signer.load_image()

        self.assertIsNotNone(signer.firmware)
        fw_len = len(signer.firmware) if signer.firmware else 0
        self.assertGreater(fw_len, 0)

    def test_load_image_file_not_found(self):
        """Test image loading with non-existent file."""
        signer = FSBLSigner(input=None, output=None, sk=None, pwd=None, pk=[], product=None, pk_hashes_out=None)

        signer.input = Path("/nonexistent/file.bin")

        with self.assertRaises(FileNotFoundError):
            signer.load_image()

    def test_save_image_success(self):
        """Test successful image saving."""
        signer = FSBLSigner(input=None, output=None, sk=None, pwd=None, pk=[], product=None, pk_hashes_out=None)

        signer.output = self.mock_output_file
        signer.firmware = b"test_firmware_data"
        signer.signed = True

        signer.save_image()

        self.assertTrue(self.mock_output_file.exists())
        self.assertEqual(self.mock_output_file.read_bytes(), b"test_firmware_data")

    def test_save_image_no_output_path(self):
        """Test image saving without output path."""
        signer = FSBLSigner(input=None, output=None, sk=None, pwd=None, pk=[], product=None, pk_hashes_out=None)

        signer.firmware = b"test_firmware_data"

        with self.assertRaisesRegex(ValueError, "Output path is not set"):
            signer.save_image()

    def test_save_image_no_firmware(self):
        """Test image saving without firmware loaded."""
        signer = FSBLSigner(input=None, output=None, sk=None, pwd=None, pk=[], product=None, pk_hashes_out=None)

        signer.output = self.mock_output_file

        with self.assertRaisesRegex(ValueError, "firmware is not loaded"):
            signer.save_image()


    @patch("fsbl_signer.ECC")
    def test_get_ecc_key_from_file_invalid_key(self, mock_ecc):
        """Test ECC key loading with invalid key."""
        mock_ecc.import_key.side_effect = ValueError("Invalid key")

        with self.assertRaisesRegex(ValueError, "Failed to load ECC key"):
            FSBLSigner.get_ecc_key_from_file(self.mock_private_key, "password")

    @patch("fsbl_signer.ECC")
    def test_get_ecc_key_from_file_passwordless(self, mock_ecc):
        """Test ECC key loading from unencrypted file without password."""
        mock_key = MagicMock()
        mock_ecc.import_key.return_value = mock_key

        key = FSBLSigner.get_ecc_key_from_file(self.mock_private_key, None)

        self.assertEqual(key, mock_key)
        # Should call import_key with passphrase=None
        mock_ecc.import_key.assert_called_once()
        args, kwargs = mock_ecc.import_key.call_args
        self.assertIsNone(kwargs.get("passphrase"))

    @patch("fsbl_signer.ECC")
    def test_get_ecc_key_from_file_password_on_unencrypted_key(self, mock_ecc):
        """Test ECC key loading from unencrypted file with password (should warn)."""
        mock_key = MagicMock()
        # First call with password succeeds, second call without password also succeeds (unencrypted key)
        mock_ecc.import_key.return_value = mock_key

        with self.assertLogs("fsbl_signer", level="WARNING") as log:
            key = FSBLSigner.get_ecc_key_from_file(self.mock_private_key, "unnecessary_password")

        self.assertEqual(key, mock_key)
        # Should log a warning about password being ignored
        self.assertTrue(any("key is not encrypted" in message for message in log.output))


    def test_get_raw_pubkey(self):
        """Test raw public key extraction."""
        # Mock ECC key
        mock_key = MagicMock()
        mock_key.pointQ.x.to_bytes.return_value = b"x" * 32  # Exactly 32 bytes
        mock_key.pointQ.y.to_bytes.return_value = b"y" * 32  # Exactly 32 bytes

        raw_pubkey = FSBLSigner.get_raw_pubkey(mock_key)

        self.assertEqual(len(raw_pubkey), 64)
        self.assertEqual(raw_pubkey[:32], b"x" * 32)
        self.assertEqual(raw_pubkey[32:], b"y" * 32)

    @patch("fsbl_signer.SHA256")
    def test_hash_pubkey_with_ecc_alg(self, mock_sha256):
        """Test public key hashing with ECDSA algorithm."""
        # Mock ECC key
        mock_key = MagicMock()
        mock_key.pointQ.x.to_bytes.return_value = b"x" * 32
        mock_key.pointQ.y.to_bytes.return_value = b"y" * 32

        # Mock SHA256
        mock_hash = MagicMock()
        mock_sha256.new.return_value = mock_hash

        result = FSBLSigner.hash_pubkey_with_ecc_alg(mock_key, ECDSA_ALGO)

        self.assertEqual(result, mock_hash)
        # Verify SHA256.new was called with correct data
        expected_data = ECDSA_ALGO.to_bytes(4, byteorder="little") + b"x" * 32 + b"y" * 32
        mock_sha256.new.assert_called_once_with(data=expected_data)

    @patch("fsbl_signer.ECC")
    @patch("fsbl_signer.DSS")
    @patch("fsbl_signer.SHA256")
    def test_verify_signature_with_raw_pubkey_success(self, mock_sha256, mock_dss, mock_ecc):
        """Test successful signature verification with raw public key."""
        # Mock components
        mock_point = MagicMock()
        mock_ecc.EccPoint.return_value = mock_point
        mock_public_key = MagicMock()
        mock_ecc.EccKey.return_value = mock_public_key
        mock_verifier = MagicMock()
        mock_dss.new.return_value = mock_verifier
        mock_hash = MagicMock()
        mock_sha256.new.return_value = mock_hash

        # Mock successful verification (no exception)
        mock_verifier.verify.return_value = None

        raw_pubkey = b"x" * 32 + b"y" * 32
        signature = b"signature_data"
        message = b"message_data"

        result = FSBLSigner.verify_signature_with_raw_pubkey(raw_pubkey, signature, message)

        self.assertTrue(result)
        mock_verifier.verify.assert_called_once_with(mock_hash, signature)

    @patch("fsbl_signer.ECC")
    @patch("fsbl_signer.DSS")
    @patch("fsbl_signer.SHA256")
    def test_verify_signature_with_raw_pubkey_failure(self, mock_sha256, mock_dss, mock_ecc):
        """Test failed signature verification with raw public key."""
        # Mock components
        mock_point = MagicMock()
        mock_ecc.EccPoint.return_value = mock_point
        mock_public_key = MagicMock()
        mock_ecc.EccKey.return_value = mock_public_key
        mock_verifier = MagicMock()
        mock_dss.new.return_value = mock_verifier
        mock_hash = MagicMock()
        mock_sha256.new.return_value = mock_hash

        # Mock failed verification (ValueError exception)
        mock_verifier.verify.side_effect = ValueError("Verification failed")

        raw_pubkey = b"x" * 32 + b"y" * 32
        signature = b"signature_data"
        message = b"message_data"

        result = FSBLSigner.verify_signature_with_raw_pubkey(raw_pubkey, signature, message)

        self.assertFalse(result)

    def test_get_public_signing_key_idx_success(self):
        """Test successful extraction of signing key index."""
        signer = FSBLSigner(
            input=None,
            output=None,
            sk=self.temp_dir / "privateKey03.pem",
            pwd=None,
            pk=[],
            product=None,
            pk_hashes_out=None,
        )

        # Create the key file
        assert signer.sk is not None  # silence mypy about sk potentially being None
        signer.sk.write_text("mock_key_data")

        idx = signer.get_public_signing_key_idx()
        self.assertEqual(idx, 3)


    def test_get_public_signing_key_idx_no_key(self):
        """Test signing key index extraction without key set."""
        signer = FSBLSigner(input=None, output=None, sk=None, pwd=None, pk=[], product=None, pk_hashes_out=None)

        with self.assertRaisesRegex(ValueError, "Signing key is not set"):
            signer.get_public_signing_key_idx()

    @patch("fsbl_signer.FSBLSigner.get_ecc_key_from_file")
    @patch("fsbl_signer.SHA256")
    def test_gen_pkht_success(self, mock_sha256, mock_get_key):
        """Test successful public key hash table generation."""
        # Mock keys
        mock_key1 = MagicMock()
        mock_key1.pointQ.x.to_bytes.return_value = b"x1" * 16
        mock_key1.pointQ.y.to_bytes.return_value = b"y1" * 16

        mock_key2 = MagicMock()
        mock_key2.pointQ.x.to_bytes.return_value = b"x2" * 16
        mock_key2.pointQ.y.to_bytes.return_value = b"y2" * 16

        mock_get_key.side_effect = [mock_key1, mock_key2]

        # Mock SHA256 hashes
        mock_hash1 = MagicMock()
        mock_hash1.digest.return_value = b"hash1___" * 4  # 32 bytes
        mock_hash2 = MagicMock()
        mock_hash2.digest.return_value = b"hash2___" * 4  # 32 bytes
        mock_pkhth_hash = MagicMock()
        mock_pkhth_hash.digest.return_value = b"pkhth___" * 4  # 32 bytes

        mock_sha256.new.side_effect = [mock_hash1, mock_hash2, mock_pkhth_hash]

        signer = FSBLSigner(
            input=None,
            output=None,
            sk=None,
            pwd=None,
            pk=[self.mock_public_key1, self.mock_public_key2],
            product=None,
            pk_hashes_out=None,
        )

        signer.gen_pkht()

        # Verify results
        self.assertEqual(len(signer.pkh), PUBKEY_COUNT)
        self.assertEqual(signer.pkh[0], b"hash1___" * 4)
        self.assertEqual(signer.pkh[1], b"hash2___" * 4)
        # Remaining slots should be filled with the last hash
        for i in range(2, PUBKEY_COUNT):
            self.assertEqual(signer.pkh[i], b"hash2___" * 4)

        self.assertEqual(signer.pkhth, b"pkhth___" * 4)

    def test_gen_pkht_too_many_keys(self):
        """Test public key hash table generation with too many keys."""
        # Create 9 public key files
        pk_files = []
        for i in range(9):
            pk_file = self.temp_dir / f"publicKey0{i}.pem"
            pk_file.write_text(f"mock_key_data_{i}")
            pk_files.append(pk_file)

        signer = FSBLSigner(input=None, output=None, sk=None, pwd=None, pk=pk_files, product=None, pk_hashes_out=None)

        with self.assertRaisesRegex(ValueError, "Expected at most 8 public keys"):
            signer.gen_pkht()

    @patch("fsbl_signer.FSBLSigner.gen_pkht")
    def test_gen_hash_files_success(self, mock_gen_pkht):
        """Test successful hash file generation."""
        signer = FSBLSigner(
            input=None, output=None, sk=None, pwd=None, pk=[], product=None, pk_hashes_out=self.mock_hash_dir
        )

        # Mock the hash data
        signer.pkh = [b"hash_data___" * 4] * 8  # 8 hashes of 32 bytes each
        signer.pkhth = b"pkhth_data__" * 4  # 32 bytes

        signer.gen_hash_files()

        # Verify hash files were created
        for i in range(8):
            hash_file = self.mock_hash_dir / f"publicKeyHash0{i}.bin"
            self.assertTrue(hash_file.exists())
            self.assertEqual(hash_file.read_bytes(), b"hash_data___" * 4)

        pkhth_file = self.mock_hash_dir / "publicKeysHashHashes.bin"
        self.assertTrue(pkhth_file.exists())
        self.assertEqual(pkhth_file.read_bytes(), b"pkhth_data__" * 4)

    def test_gen_hash_files_no_output_dir(self):
        """Test hash file generation without output directory."""
        signer = FSBLSigner(input=None, output=None, sk=None, pwd=None, pk=[], product=None, pk_hashes_out=None)

        with self.assertRaisesRegex(ValueError, "Public key hashes output directory is not set"):
            signer.gen_hash_files()

    @patch("fsbl_signer.FSBLSigner.gen_pkht")
    def test_gen_hash_files_auto_generate_hashes(self, mock_gen_pkht):
        """Test hash file generation with automatic hash generation."""
        signer = FSBLSigner(
            input=None, output=None, sk=None, pwd=None, pk=[], product=None, pk_hashes_out=self.mock_hash_dir
        )

        # Mock gen_pkht to set the hashes
        def mock_gen_pkht_side_effect():
            signer.pkh = [b"hash_data___" * 4] * 8
            signer.pkhth = b"pkhth_data__" * 4

        mock_gen_pkht.side_effect = mock_gen_pkht_side_effect

        signer.gen_hash_files()

        # Verify gen_pkht was called
        mock_gen_pkht.assert_called_once()


if __name__ == "__main__":
    unittest.main()

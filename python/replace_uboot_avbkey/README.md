# Replace U-Boot AVB Key

Minimal script for replacing AVB (Android Verified Boot) public keys in U-Boot binary images.

## Overview

This tool searches for a development AVB public key (520 bytes) in a U-Boot binary and replaces it with a production AVB public key. It fails if the key is missing or occurs more than once.

## Installation

Install standalone [uv](https://docs.astral.sh/uv/getting-started/installation/),
then run from the repository root without activating Hermit or a virtual environment:

```bash
uv sync --managed-python --project python --locked --extra dev
```

## Usage

### Command Line

```bash
uv run --managed-python --project python --locked replace_uboot_avbkey <dev_uboot.bin> <prod_uboot.bin> <dev.avbpubkey> <prod.avbpubkey>
```

**Arguments:**
1. `dev_uboot.bin` - Input U-Boot binary with development key
2. `prod_uboot.bin` - Output U-Boot binary with production key
3. `dev.avbpubkey` - Development AVB public key file (520 bytes)
4. `prod.avbpubkey` - Production AVB public key file (520 bytes)

### Example

```bash
uv run --managed-python --project python --locked replace_uboot_avbkey u-boot-dev.bin u-boot-prod.bin dev-key.avbpubkey prod-key.avbpubkey
```

### Programmatic API

```python
from replace_uboot_avbkey import replace_avbpubkey

replace_avbpubkey("u-boot-dev.bin", "u-boot-prod.bin", "dev-key.avbpubkey", "prod-key.avbpubkey")
```

## Process Flow

1. Read dev and prod key files
2. Verify both keys are exactly 520 bytes
3. Read U-Boot binary and count occurrences of dev key
4. Fail if dev key not found or found multiple times
5. Replace dev key with prod key
6. Write modified binary to output file
7. Verify the replacement was successful (ensure the prod key appears exactly once in the output and the dev key no longer appears)

## Error Handling

The script exits with an error if:

- Either key is not exactly 520 bytes
- Dev key is not found in the U-Boot binary
- Dev key is found multiple times (expected exactly one)
- Any file cannot be read or written

## Running Tests

```bash
uv run --managed-python --project python --locked --extra dev pytest python/replace_uboot_avbkey/tests/ -v
```

## Design Notes

The public API exports only `replace_avbpubkey()` for programmatic use. The `main()` CLI entry point is intentionally excluded from the public API to keep it clean for library usage.

# Reference Config Files

This directory contains reference configuration files used for FIP signing.

## Files

| File | Description |
|------|-------------|
| `hw-config.bin.reference` | Dev hw-config DTB with dev Linux signing pubkey |
| `hw-config-prod.bin.reference` | Prod hw-config DTB with prod Linux signing pubkey |
| `fw-config.bin.reference` | Firmware config DTB |
| `METADATA.txt` | Version and hash information |

## Important: DTB and Certificate Matching

**The hw-config DTB must be the exact binary that was used to generate the corresponding `stm32mp_cfg_cert.crt`.**

- `hw-config.bin.reference` must match `dev/certs/dev/c3/stm32mp_cfg_cert.crt`
- `hw-config-prod.bin.reference` must match `prod/certs/prod/c3/stm32mp_cfg_cert.crt`

The certificate contains a SHA256 hash of the hw-config DTB. If the DTB and certificate are from different versions or ceremonies, FIP signing will fail with a hash mismatch error.

When updating to a new firmware version:
1. Regenerate both dev and prod hw-config DTBs
2. Regenerate the corresponding certificates
3. Verify hashes match using `shasum -a 256 <dtb-file>`

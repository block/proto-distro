This directory stores certificates and keys for signing Proto firmware images. It is currently in a reorganizing state so the structure of the files will change in the future.

The current structure is as follows:

- `stm32mp13_dev_keys/` - Contains development keys for the STM32MP13 devices.
- `swupdate_dev_keys/` - Contains development keys for SWUpdate.
- `uboot_dev_keys/` - Contains development keys for U-Boot.
- `prod/` - Contains production keys for Proto firmware images.
- `dev/` - Contains development keys for Proto firmware images. The hardware-backed root of trust (RoT) keys here were copied from the `stm32mp13_dev_keys/` directory. The Linux FIT signing key and certificate were copied from the `uboot_dev_keys/` directory.

The eventual goal is to unify these into the `prod/` and `dev/` directories, with the `prod/` directory containing production keys and the `dev/` directory containing development keys. These directories currently contain
the hashboard keys as well which will be migrated later.

The structure of each directory is as follows:

- `certs/` - Contains the signing certificates
- `keys/` - Contains the keys used for signing various images. The `dev/` directory contains private and public keys, while the `prod/` directory contains only public keys
- `fsbl/` - Contains pre-signed TF-A/FSBL (First Stage Boot Loader) binaries for each security level
- `pkhash/` - Contains the public key hashes used on the STM32MP13 devices
- `yubikey-certs/` - Contains the certificates for the YubiKeys used in the signing / provisioning processes

## Certificate Versioning

### C3 Development Config Certificates

The config certificates (`tb_fw.crt`, `stm32mp_cfg_cert.crt`) in `dev/certs/dev/c3/` contain hashes of hardware and firmware device tree configurations.

**Current version:** Generated from **v1.0.16** release DTBs
**Last updated:** 2026-10-05

These certificates must be regenerated when hardware or firmware configs change.

**Certificate history:**
- 2026-10-05: Raise C3 development and production certificates to NV=2, preserving keys and config hashes.
- 2026-10-02: Re-signed all five C3 development CoT certificates with NV=1; preserved existing keys and v1.0.16 config hashes.
- 2025-10-22: Updated to v1.0.16 DTBs (fw-config and hw-config from v1.0.16 release FIP)
- 2025-06-05: Initial generation (original DTB version unknown)

**Reference configs:**
The DTBs used to generate these certs are saved in `regenerated-config-certs/`. See [`regenerated-config-certs/README.md`](regenerated-config-certs/README.md) for details on the reference files and important version matching requirements.

### Regenerating Config Certificates

When hardware or firmware device tree configs change (new release version, hardware updates, etc.), the config certificates must be updated.

**Legacy environment required:** `regenerate-config-certs.sh` invokes
`cot_signer`, which is no longer shipped by the Python wheel or current
distro-builder/injector images. The commands below require a separately
maintained legacy environment with `cot_signer` and its dependencies, plus native
`cert_create` and `fiptool` at the paths expected by the script. They do not work
in the trimmed images. See [the Python distribution caveat](../../../../../python/README.md#unshipped-tools-and-manual-certificate-regeneration).
This does not change automated native certificate generation or production
ceremony requirements.

**Run the regeneration script** (inside that legacy builder environment):

```bash
# Navigate to this directory
cd /home/btcm/proto-distro/sources/meta-miner/recipes-dev/miner-signing-keys/files

# Run with release package - script handles extraction and updates automatically
./regenerate-config-certs.sh \
    --env dev \
    --product C3 \
    --reference-fip /path/to/miner-image-release-c3-p1-<version>.zip \
    --version <version>

# Review changes and commit
git diff dev/certs/dev/c3/tb_fw.crt
git diff dev/certs/dev/c3/stm32mp_cfg_cert.crt
git diff README.md

git add dev/certs/dev/c3/{tb_fw.crt,stm32mp_cfg_cert.crt} README.md
git commit -m "Update canonical config certs for v<version> DTBs"
```

**Example for v1.0.16:**

```bash
cd /home/btcm/proto-distro/sources/meta-miner/recipes-dev/miner-signing-keys/files

# Run script (automatically extracts, generates, updates)
./regenerate-config-certs.sh \
    --env dev \
    --product C3 \
    --reference-fip /path/to/miner-image-release-c3-p1-1.0.16.zip \
    --version 1.0.16

# Review and commit
git diff dev/certs/dev/c3/
git add dev/certs/dev/c3/{tb_fw.crt,stm32mp_cfg_cert.crt} README.md
git commit -m "Update canonical config certs for v1.0.16 DTBs"
```

The script automatically:
- Extracts hw/fw configs from FIP (handles zip or FIP file)
- Generates new config certificates with those DTB hashes
- Updates canonical certs in place
- Updates this README with version and date
- Verifies the complete chain
- Saves reference configs in `regenerated-config-certs/`
- Cleans up all temporary extraction directories

### Production Config Certificates

**Version alignment:**
The production config certificates should match the same firmware version as the development certs. For example:
- If `dev/certs/dev/c3/` is on v1.0.16 DTBs
- Then `prod/certs/prod/c3/` should also be regenerated with v1.0.16 DTBs
- This ensures dev and prod firmware use identical configurations

**Production hw-config DTB:**
The prod hw-config DTB (`regenerated-config-certs/hw-config-prod.bin.reference`) is different from the dev DTB because it contains the **prod Linux signing public key** embedded for FIT image verification. The prod `stm32mp_cfg_cert.crt` contains a hash of this exact DTB - they must match.

## FSBL (First Stage Boot Loader) Binaries

The `fsbl/` directories contain pre-signed TF-A (Trusted Firmware-A) binaries for each security level and product. These binaries are the first code executed by the ROM bootloader and are verified against OTP fuses programmed during manufacturing.

**Directory structure:**
```
dev/fsbl/{product}/    - Development-signed FSBL binaries
prod/fsbl/{product}/   - Production-signed FSBL binaries (from offline ceremony)
```

The ROM bootloader verifies FSBL against OTP fuses, so:
- Dev-signed FSBL **will NOT boot** on production devices (OTP mismatch)
- Production-signed FSBL **will NOT boot** on dev devices (OTP mismatch)


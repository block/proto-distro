This directory stores certificates and keys for signing Proto firmware images. It is currently in a reorganizing state so the structure of the files will change in the future.

The current structure is as follows:

- `stm32mp13_dev_keys/` - Contains development keys for the STM32MP13 devices.
- `swupdate_dev_keys/` - Contains development keys for SWUpdate.
- `uboot_dev_keys/` - Contains development keys for U-Boot.
- `prod/` - Contains production keys for Proto firmware images.
- `dev/` - Contains development keys for Proto firmware images. The hardware-backed root of trust (RoT) keys here were copied from the `stm32mp13_dev_keys/` directory. The Linux FIT signing key and certificate were copied from the `uboot_dev_keys/` directory.

The eventual goal is to unify these into the `prod/` and `dev/` directories, with the `prod/` directory containing production keys and the `dev/` directory containing development keys. These directories currently contain
the hashboard keys as well which will be migrated later.

For more information on the different keys and their usage, refer to [Proto Production Firmware Key Generation Plan](https://docs.google.com/document/d/1_pqfSmo2yimqrsH9XX9TqpI5jPtzqCb7PguWZ7yufuo/edit?usp=sharing).

The production keys were generated as part of our offline ceremony which is documented in [proto-firmware-ca-ceremony](https://github.com/squareup/proto-firmware-ca-ceremony). The same scripts were used to generate the development keys.

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

**Run the regeneration script** (inside distro-builder container):

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

**IMPORTANT:** Production versions of these certificates (`prod/certs/prod/c3/tb_fw.crt`, `stm32mp_cfg_cert.crt`) **MUST** be generated offline by Hardware Security (HWSEC) using the production Root of Trust key.

The production RoT key is stored in secure and controlled Infosec storage and never leaves the secure environment. Production config certificates require a formal key ceremony with HWSEC.

**Requirements for production cert updates:**
1. Must be performed by HWSEC in a secure offline environment
2. Requires access to production RoT key stored offline
3. Follows formal key ceremony procedures
4. Must use the **same hw/fw config versions** as development certs

**Version alignment:**
The production config certificates should match the same firmware version as the development certs. For example:
- If `dev/certs/dev/c3/` is on v1.0.16 DTBs
- Then `prod/certs/prod/c3/` should also be regenerated with v1.0.16 DTBs
- This ensures dev and prod firmware use identical configurations

**To request production cert updates:**
1. Identify the new firmware version and DTBs needed
2. File a request with HWSEC (#hwsec) for production cert regeneration
3. Provide reference configs from `regenerated-config-certs/` as input
4. HWSEC will generate production certs offline and provide them for check-in
5. **Important:** Also obtain the prod hw-config DTB from the ceremony output. Output files are stored in [Google Drive](https://drive.google.com/drive/folders/13OUxyTRO1T5150t4ue9dztN9fRIWxp8J).

The `regenerate-config-certs.sh` script is for **development use only** and cannot be used for production (production RoT key is stored offline in an Infosec safe).

**Production hw-config DTB:**
The prod hw-config DTB (`regenerated-config-certs/hw-config-prod.bin.reference`) is different from the dev DTB because it contains the **prod Linux signing public key** embedded for FIT image verification. This DTB was generated during the HWSEC ceremony and must be checked in alongside the prod certificates. The prod `stm32mp_cfg_cert.crt` contains a hash of this exact DTB - they must match.

## FSBL (First Stage Boot Loader) Binaries

The `fsbl/` directories contain pre-signed TF-A (Trusted Firmware-A) binaries for each security level and product. These binaries are the first code executed by the ROM bootloader and are verified against OTP fuses programmed during manufacturing.

**Directory structure:**
```
dev/fsbl/{product}/    - Development-signed FSBL binaries
prod/fsbl/{product}/   - Production-signed FSBL binaries (from offline ceremony)
```

**Why FSBL replacement is required:**

Release packages are built with dev-signed FSBL by default. During the firmware signing process, the firmware-signer service replaces these FSBL binaries with environment-appropriate versions:
- **Dev/staging environments** → Use dev-signed FSBL (boots on dev devices)
- **Production environment** → Use prod-signed FSBL (boots on prod devices)

The ROM bootloader verifies FSBL against OTP fuses, so:
- Dev-signed FSBL **will NOT boot** on production devices (OTP mismatch)
- Production-signed FSBL **will NOT boot** on dev devices (OTP mismatch)

**Updating FSBL binaries:**

When a new firmware version requires updated FSBL:

1. **Dev binaries:** Extract from the mfg release package at the GitHub release
2. **Prod binaries:** Obtain from HWSEC offline signing ceremony output (stored in [Google Drive](https://drive.google.com/drive/folders/13OUxyTRO1T5150t4ue9dztN9fRIWxp8J))
3. **Naming convention:** `tf-a-stm32mp131f-{product}{variant}-{boot_source}-{version}.stm32`
   - Example: `tf-a-stm32mp131f-c3p1-emmc-0.2.11.stm32`
4. **Verification:** Ensure all three boot sources (emmc, uart, usb) are present for each security level
5. Update metadata file in each `fsbl/{product}/` directory with version and source info

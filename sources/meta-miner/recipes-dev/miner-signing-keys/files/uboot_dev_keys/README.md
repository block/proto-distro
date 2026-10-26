# Generating keys
The keys for FIT signing and dm-verity were generated using command:

```bash
$ openssl genrsa -F4 -out /path/to/private/key.priv.pem  2048
$ openssl rsa -in /path/to/private/key.priv.pem -pubout -out /path/to/public/key.pub.pem
```

To generate the FIT image signing cert:
```bash
$ openssl req -batch -new -x509 -key /path/to/private/key.priv.pem -out linux-signing-key.crt
```

To generate the DM Verity avbpubkey.
```bash
$ avbtool extract_public_key --key /path/to/public/key.pub.pem --output /path/to/public/key.avbpubkey
```

# Signing images

## Signing boot script
```bash
uboot-mkimage -k ${UBOOT_SIGN_KEYDIR} -f ${WORKDIR}/u-boot-scr-fit-image.its ${UBOOT_BOOTSCR_IMG}

${UBOOT_MKIMAGE_SIGN} -F -k ${UBOOT_SIGN_KEYDIR} -K {UBOOT_DTB} -r ${LINUX_FIT_IMAGE}

```
The ITS describes the FIT image and the configuration signature covering its script.

### Verification during boot

The U-Boot recipe backports upstream commit `bcc85b96b5ff` to support signed
script configurations. On machines with `UBOOT_SIGN_ENABLE = "1"`, the boot
command uses `source ${scriptaddr}#bootscr`. When verification is enabled, a
required configuration key (`required = "conf"`) must exist in U-Boot's control
DTB. The script executes only after its configuration signature and payload
hash both pass.

Successful verification prints this marker before executing the script:

```text
Boot script FIT signature and payload verified
```

The preceding `sha256,rsa2048:linux-signing-key+` indicates signature success;
`sha256+` alone indicates only a hash check. `Signature check OK` below is
output from the host tool, not the boot-time script command.

With verification enabled, signing-enabled builds reject unsigned/legacy
scripts, direct subimage execution, missing required keys, and unsigned fallback.
Only exact `verify=n` bypasses verification on unlocked boards; locked boards
and `UBOOT_FORCE_VERIFY=1` builds enforce `verify=y`. Verification rejection
enters UDP fastboot immediately on STM32MP13. Unsigned machine builds retain
upstream behavior. This checks `boot.scr.uimg`, not the entire bootfs filesystem,
and requires rebuilding and deploying U-Boot.

The boot script has no A/B fallback. Signature rejection bypasses boot retries
and enters compiled UDP recovery; retrying cannot repair an invalid signature.
Rootfs, kernel, and ordinary returned-boot failures retain the existing retry
path (120-second CLI timeout, `bootlimit=5`). Explicit fastboot requests and
exhausted retries still use `altbootcmd`. Failure to restore compiled boot-policy
variables also enters compiled UDP recovery instead of hanging immediately.

## Signing Linux FIT image
```bash
${UBOOT_MKIMAGE_SIGN} -F -k ${UBOOT_SIGN_KEYDIR} -K {UBOOT_DTB} -r ${LINUX_FIT_IMAGE}
```
The public portion of the signing key will be embedded into U-boot's devicetree.

## Verify signed images

### Validating that the fitImage was signed
```
dumpimage -l fitImage
```

### Viewing public key components into U-boot devicetree
```
fdtdump ${UBOOT_DTB} | grep -A 20 "signature {"

...
    signature {
        key-linux-signing-key {
            required = "conf";
            algo = "sha256,rsa2048";
            rsa,r-squared = <0xafcf8de5 0x4729d191 0x002977ae 0x1d30b2e9 0xf117ac94 0xe1ae7932 0xb8062055 0x666a8dd6 0x822884e2 0xbf8aefd6 0xf474d3ee 0x2fb2569e 0xf1596baa 0xb328c4f8 0x657dc6dc 0xa9940017 0xafb8c0e2 0x7d4cb881 0x1f99dd0a 0x8a29517b 0x217963ba 0x26239400 0xa89c0b45 0x599388f1 0xdf678671 0x040a8649 0xe9da8d1b 0xd2e4ac9c 0xc698e3fc 0xaf698872 0xd328c99e 0xfe0e7ab4 0xfb28fe4e 0x48253447 0x09bb809e 0xc4da3147 0x32cbda9c 0xe9f0e164 0xe7234ed5 0xb765308f 0x3d071589 0xfb21428a 0x9ec0806d 0x7e543973 0xa5534545 0x4f02fe72 0xf5d6ca64 0x2e12b123 0x99bbd603 0xa40704c0 0x5a114454 0x626caba5 0xd5956384 0xedd779ac 0x02c6521c 0x68fab6bc 0x05d666c2 0xaaee6fac 0x793b0211 0x1f1ee73d 0x6f41dc2c 0xdba5a9b6 0xfd7355ec 0x24e2fa44>;
            rsa,modulus = <0xb7df5d22 0x4435c9fa 0xced812eb 0x18581551 0xff09aae8 0x2d0ce011 0x62bf3bcf 0x5471919d 0x172214c1 0x353f690e 0x61c507f6 0x9d4e4d57 0x5b67acc7 0x88b5f524 0x185eef18 0x6115cc4f 0xab4ab34e 0x809e4b02 0xf34d9464 0xe884445c 0x260684db 0x7b235144 0xff780a9e 0x01efc0dc 0x9fc35ac3 0x5b5b2662 0xcdb1383f 0xbcfffa79 0xb117d970 0x980c9ec0 0x2da4939b 0xbfba742d 0xdf662eff 0x95a10ef1 0x41ae80da 0xcdb533ea 0xcd65b1e0 0xd1b6cd49 0x26f845f7 0x406de81c 0xb755c80b 0x89f78ccd 0x8773c3b9 0x120dca80 0xe3d3d7d4 0x08f2f29e 0x6c1798c8 0xc7d10ddc 0x3a81d6a6 0x4f2f5f5e 0x7481d6aa 0x122930bf 0xf0b03bdb 0xa2ca726f 0xbab0a18d 0x314d00b5 0xa1144c0a 0x5859609c 0xdf22f6d8 0x0b7f3d27 0xea472f1f 0xfa6ae0c6 0x0f3801fc 0xd8ba0cd1>;
            rsa,exponent = <0x00000000 0x00010001>;
            rsa,n0-inverse = <0x796233cf>;
            rsa,num-bits = <0x00000800>;
            key-name-hint = "linux-signing-key";
        };
    };

```

### Verifying that signature checks pass

We are looking for `Signature check OK` in the output.
```
${FIT_CHECK_SIGN} -f fitImage -k ${UBOOT_DTB}

Verifying Hash Integrity for node 'conf-stm32mp131f-c3p1.dtb'... sha256,rsa2048:linux-signing-key+ 
Verified OK, loading images
## Loading kernel from FIT Image at ffff8ae00000 ...
   Using 'conf-stm32mp131f-c3p1.dtb' configuration
   Verifying Hash Integrity ... 
sha256,rsa2048:linux-signing-key+ 
OK

   Trying 'kernel-1' kernel subimage
     Description:  Linux kernel
     Created:      Wed Jun 21 13:59:19 2023
     Type:         Kernel Image
     Compression:  uncompressed
     Data Size:    7902232 Bytes = 7717.02 KiB = 7.54 MiB
     Architecture: ARM
     OS:           Linux
     Load Address: 0xc0800000
     Entry Point:  0xc0800000
     Hash algo:    sha256
     Hash value:   ab1eeb239c19042af34b1c1923b4407fe2bd57e89ed8a95aea8db4a406245f36
   Verifying Hash Integrity ... 
sha256+ 
OK

   Decrypting Data ... 
OK

   Loading Kernel Image
## Loading fdt from FIT Image at ffff8ae00000 ...
   Using 'conf-stm32mp131f-c3p1.dtb' configuration
   Verifying Hash Integrity ... 
sha256,rsa2048:linux-signing-key+ 
OK

   Trying 'fdt-stm32mp131f-c3p1.dtb' fdt subimage
     Description:  Flattened Device Tree blob
     Created:      Wed Jun 21 13:59:19 2023
     Type:         Flat Device Tree
     Compression:  uncompressed
     Data Size:    52053 Bytes = 50.83 KiB = 0.05 MiB
     Architecture: ARM
     Hash algo:    sha256
     Hash value:   53da2f9e2c9597382d0ad8a9ced89d688fb7e72c6155fec832de18fb951343e7
   Verifying Hash Integrity ... 
sha256+ 
OK

   Decrypting Data ... 
OK

   Loading Flat Device Tree
## Loading ramdisk from FIT Image at ffff8ae00000 ...
   Using 'conf-stm32mp131f-c3p1.dtb' configuration
   Verifying Hash Integrity ... 
sha256,rsa2048:linux-signing-key+ 
OK

Could not find subimage node type 'ramdisk'
Signature check OK
```

# FIT Image Signing Scripts

These scripts are used to sign and verify Linux FIT (Flattened Image Tree) images for U-Boot FIT verification in production environments.

This is needed because the Yocto build system tightly couples image building and signing.
The scripts are intended to run in a Docker image with all prerequisite dependencies installed.

## Script Overview

### sign-fitimage-helper.sh

Core signing and verification helper that can be used standalone or called by other scripts.

**Purpose**: Signs a FIT image file and U-Boot DTB, then verifies the signature.

**Usage**:
```bash
./sign-fitimage-helper.sh <keydir> <fitimage> <uboot_dtb> <key_name>
```

**Arguments**:
| Argument | Description |
|----------|-------------|
| `keydir` | Directory containing the signing keys (`.key` and `.crt` files) |
| `fitimage` | Path to the FIT image file to sign (modified in place) |
| `uboot_dtb` | Path to the U-Boot device tree blob (modified in place) |
| `key_name` | Name of the key files (without `.key`/`.crt` extensions) |

**Example**:
```bash
./sign-fitimage-helper.sh \
    /path/to/signing/keys \
    fitImage \
    u-boot.dtb \
    "linux-signing-key"
```

### sign-fitimage-linux.sh

Complete workflow for signing FIT images extracted from rootfs filesystems.

**Purpose**: Extracts a FIT image from rootfs, signs it using `sign-fitimage-helper.sh`, and repackages it back into a new rootfs.

**Usage**:
```bash
./sign-fitimage-linux.sh <keydir> <rootfs> <uboot_dtb> <outdir> <key_name>
```

**Arguments**:
| Argument | Description |
|----------|-------------|
| `keydir` | Directory containing the signing keys (`.key` and `.crt` files) |
| `rootfs` | Path to the input rootfs.ext4 file containing the FIT image |
| `uboot_dtb` | Path to the U-Boot device tree blob for signature verification (HWCONFIG) |
| `outdir` | Output directory where signed files will be created |
| `key_name` | Name of the key files (without `.key`/`.crt` extensions) |

**Example**:
```bash
./sign-fitimage-linux.sh \
    /path/to/signing/keys \
    miner-image-mfg-c3-p1-3f4dac1501-dirty.rootfs.ext4 \
    u-boot.dtb \
    ./out \
    "linux-signing-key"
```

**Workflow**:
1. Extracts the FIT image from the rootfs filesystem
2. Calls `sign-fitimage-helper.sh` to sign and verify the FIT image
3. Creates a copy of the rootfs with the signed FIT image integrated

### sign-fitimage-uboot-scr.sh

Complete workflow for signing U-Boot script FIT images extracted from bootfs filesystems.

**Purpose**: Extracts a U-Boot script FIT image from bootfs, signs it using `sign-fitimage-helper.sh`, and repackages it back into a new bootfs.

**Usage**:
```bash
./sign-fitimage-uboot-scr.sh <keydir> <bootfs> <uboot_dtb> <outdir> <key_name>
```

**Arguments**:
| Argument | Description |
|----------|-------------|
| `keydir` | Directory containing the signing keys (`.key` and `.crt` files) |
| `bootfs` | Path to the bootfs.ext4 file containing the U-Boot script FIT image |
| `uboot_dtb` | Path to the U-Boot device tree blob for signature verification (HWCONFIG) |
| `outdir` | Output directory where signed files will be created |
| `key_name` | Name of the key files (without `.key`/`.crt` extensions) |

**Example**:
```bash
./sign-fitimage-uboot-scr.sh \
    /path/to/signing/keys \
    miner-image-c3-p1.bootfs.ext4 \
    u-boot.dtb \
    ./out \
    "uboot-script-signing-key"
```

**Workflow**:
1. Extracts the U-Boot script FIT image (`/boot.scr.uimg`) from the bootfs filesystem
2. Calls `sign-fitimage-helper.sh` to sign and verify the FIT image
3. Creates a copy of the bootfs with the signed FIT image integrated

## Prerequisites

### Required Tools

The scripts require the following tools to be available:
- `debugfs` - For extracting and modifying ext4 filesystem contents (used by sign-fitimage-linux.sh and sign-fitimage-uboot-scr.sh)
- `openssl` - For key validation and cryptographic operations
- `mkimage` - For signing FIT images (bundled with U-Boot tools)
- `fit_check_sign` - For verifying FIT image signatures (bundled with U-Boot tools)

### Required Files

1. **Signing Keys**: Two files in the key directory:
   - `<key_name>.key` - Private Key file in PEM format
   - `<key_name>.crt` - Public Key Certificate file

2. **Input Files**:
   - For `sign-fitimage-helper.sh`: FIT image and U-Boot DTB files
   - For `sign-fitimage-linux.sh`: Rootfs file (`.rootfs.ext4`) and U-Boot DTB
   - For `sign-fitimage-uboot-scr.sh`: Bootfs file (`.bootfs.ext4`) and U-Boot DTB

## Output Files

### sign-fitimage-helper.sh
Modifies files in place:
- The FIT image is signed
- The U-Boot DTB is updated with the public key

### sign-fitimage-linux.sh
Creates three production files in the output directory:

| File | Description |
|------|-------------|
| `prod-fitImage` | The extracted and signed FIT image |
| `prod-u-boot.dtb` | Copy of the U-Boot DTB with embedded public keys |
| `prod-<original-rootfs-name>` | Complete rootfs with the signed FIT image integrated |

### sign-fitimage-uboot-scr.sh
Creates three production files in the output directory:

| File | Description |
|------|-------------|
| `prod-boot.scr.uimg` | The extracted and signed U-Boot script FIT image |
| `prod-u-boot.dtb` | Copy of the U-Boot DTB with embedded public keys |
| `prod-<original-bootfs-name>` | Complete bootfs with the signed U-Boot script FIT image integrated |

# AVB Rootfs Image Creation

The `create_avb_image` CLI from `proto-distro-security-tools` creates dm-verity protected rootfs images using Android Verified Boot (AVB). The shared Go distro pipeline invokes the installed CLI after rootfs mutation; the same CLI supports standalone signing. Yocto exports plain images and no longer runs an AVB image task.

## Overview

The CLI automates the process of:
1. Taking a rootfs.ext4 image
2. Signing it with AVB hashtree footer for dm-verity verification
3. Verifying the signature
4. Creating `.avb.body` and `.avb.tail` metadata while preserving plain ext4.

The signed dm-init command line uses `PARTUUID=$(ANDROID_SYSTEM_PARTUUID)`
for both the data and hash devices. U-Boot substitutes the GUID of the selected
`rootfs_a` or `rootfs_b` partition, so the same image works in either slot.

The release boot script follows U-Boot's `verify` lock/unlock policy:

- `verify=n`: skip AVB and boot the selected slot directly using its PARTUUID,
  with a writable rootfs and no forced `console=ttynull`. This preserves the
  unlocked boot path on older bootchains without AVB support.
- Otherwise: require AVB verification for the selected slot and pass the
  resulting `dm-mod.create=... root=/dev/dm-0` arguments to Linux, with a
  read-only rootfs, `quiet`, and `console=ttynull` to suppress the Linux console.
  Missing or unexpected `verify` values
  do not disable verification. Missing or hashtree-disabled arguments fail
  closed; there is no raw-root fallback after an AVB failure.

`verify` is not a bootchain-version detector. An older locked bootchain with
`verify=y` still needs AVB support and a matching signed rootfs. Kernel-load
or kernel-boot failure resets in either mode. Manufacturing images continue
to use the separate default boot script.

Rebuild plain release/manufacturing rootfs and matching `.avb.body`/`.avb.tail` metadata together.
Use a compatible flasher that writes raw data at offset 0, body at raw size, and
the 512-byte tail at offset 1073741312.
Run `cb_st_flasher --verity <artifact-directory>` to write these three extents.
Older AVB images with a hardcoded partition 7 backing device must be
regenerated for A/B use. The signing key must match U-Boot's embedded AVB key.
Boot-script changes require updating the separate bootfs image as well; the
current rootfs OTA does not update bootfs.

Build, postbuild, and inject regenerate release/manufacturing AVB after package installation,
postinstall scripts, VERSION updates, unmounting, and filesystem checks.
Signing also runs without package/version changes. Plain ext4 is retained for
reinjection; both `.avb.body` and `.avb.tail` sidecars are included in local ZIPs
and CI direct uploads. Yocto exports plain ext4, not these sidecars. The deploy-only
`rootfs.avb` alias is retired.

Release and manufacturing SWUs contain gzip of plain ext4 under the existing `*.rootfs.ext4.gz` name,
plus separate `.avb.body` and `.avb.tail` records. Raw data is written at offset 0,
body at raw size, and the 512-byte tail at offset 1073741312. Each member is hashed
in the signed manifest. Hashes, A/B references and description signatures are
refreshed before checksums/publication. CI uploads these signed SWUs.

The updated proto-swupdate resolves a direct root partition or one single-backed
device-mapper layer. For the first migration from an old updater, run the corrected
updater from RAM or explicitly select the verified inactive slot with native
SWUpdate. Installing the corrected updater in the incoming rootfs cannot fix the
old process before it chooses a slot.
Rootfs OTA still does not update separate bootfs. Signed payload generation alone
does not prove hardware boot or successful locked-device OTA. Production tamper
resistance also requires separate bootloader and FIT signature enforcement;
development signing defaults do not establish production trust compatibility.

## Requirements

The injector image installs the Python package and exposes `create_avb_image`.
The distro pipeline reuses the configured image or pulls it if absent; signing
needs no runtime installation, network, privileged container, or host Python.
The signer mounts the artifact parent read-write and only three selected key
files read-only. Rebuild an older image with `just docker-build-injector`.

Configure `avb.<machine>.private_key`, `.public_key`, and `.avb_public_key` in
`distro.yaml`, with paths relative to that file. `c3-p1` uses the C3 family.
The selected nonempty paths must resolve to readable regular files.
Manufacturing also requires AVB configuration. SWU
`signing` credentials and `--swu-key-file/--swu-key-pass` are independent.

U-Boot still needs `DM_VERITY_ENABLE` and the existing
`miner-dev-c3-dm-verity-keys-native` recipe for its trust anchor. AVB is bundled
in the Python package. The original dm-init
patch remains under `recipes-devtools/avbtool/avbtool/` for provenance.

On signing failure, distro stops before SWU/hash/ZIP/latest finalization. A ZIP
input remains unchanged. Folder `.original` backups provide recovery, not
transactional rollback; its plain rootfs may already have been replaced.

For standalone use, install the wheel or invoke it with `uvx --from <wheel>`;
Python 3.12+ and OpenSSL 3.x on PATH are required.

## Usage

```bash
create_avb_image --rootfs-image <rootfs.ext4> --privkey <key.priv.pem> --pubkey <key.pub.pem> --avbpubkey <key.avbpubkey>
```

Matching `rootfs.ext4.avb.body` and `rootfs.ext4.avb.tail` files are created beside
the input. The plain ext4 remains unchanged; no full signed image is published.

### Arguments

| Argument | Description |
|----------|-------------|
| `--rootfs-image` | Path to plain rootfs ext4 image (e.g., `miner-image-c3-p1.rootfs.ext4`) |
| `--privkey` | Path to RSA2048 private key in PEM format for signing |
| `--pubkey` | Path to RSA2048 public key in PEM format for verification |
| `--avbpubkey` | Path to AVB public key binary (`.avbpubkey`) |

### Example

```bash
create_avb_image \
    --rootfs-image miner-image-c3-p1.rootfs.ext4 \
    --privkey signing-key.priv.pem \
    --pubkey signing-key.pub.pem \
    --avbpubkey signing-key.avbpubkey
```

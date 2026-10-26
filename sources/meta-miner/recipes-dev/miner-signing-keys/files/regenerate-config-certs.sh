#!/bin/bash
set -euo pipefail

# Script to regenerate canonical config certificates (tb_fw.crt, stm32mp_cfg_cert.crt)
# when hw/fw configs change in your builds

while [[ $# -gt 0 ]]; do
        case "$1" in
        --env)
                ENVIRONMENT="$2"
                shift 2
                ;;
        --product)
                PRODUCT="$2"
                shift 2
                ;;
        --reference-fip)
                REFERENCE_FIP="$2"
                shift 2
                ;;
        --hw-config)
                HW_CONFIG="$2"
                shift 2
                ;;
        --fw-config)
                FW_CONFIG="$2"
                shift 2
                ;;
        --out-dir)
                OUT_DIR="$2"
                shift 2
                ;;
        --version)
                VERSION="$2"
                shift 2
                ;;
        *)
                echo "Error: Unknown argument: $1"
                exit 1
                ;;
        esac
done

if [[ -z "${ENVIRONMENT:-}" || -z "${PRODUCT:-}" ]]; then
        echo "Error: Missing required arguments."
        echo
        echo "Regenerates canonical config certificates (tb_fw.crt, stm32mp_cfg_cert.crt)"
        echo "when hw/fw devicetree configs change."
        echo
        echo "Usage: $0 --env <env> --product <product> --reference-fip <fip-file> [--version <ver>] [--out-dir <dir>]"
        echo "   OR: $0 --env <env> --product <product> --hw-config <dtb> --fw-config <dtb> [--version <ver>] [--out-dir <dir>]"
        echo
        echo "  env: Environment (dev or prod)"
        echo "  product: Product name (C3)"
        echo "  reference-fip: Path to FIP file to extract hw/fw configs from"
        echo "  hw-config: (alternative) Direct path to hw-config DTB"
        echo "  fw-config: (alternative) Direct path to fw-config DTB"
        echo "  version: (optional) Version string for documentation (e.g., '1.0.16')"
        echo "  out-dir: (optional) Output directory for new certs (default: ./regenerated-config-certs)"
        echo
        echo "Examples:"
        echo "  # Extract configs from a FIP and regenerate certs"
        echo "  $0 --env dev --product C3 --reference-fip /path/to/fip-image.bin --version 1.0.16"
        echo
        echo "  # Use specific DTB files"
        echo "  $0 --env dev --product C3 --hw-config /path/to/u-boot.dtb --fw-config /path/to/fw-config.dtb --version 1.2.0"
        exit 1
fi

if [[ -z "${REFERENCE_FIP:-}" && ( -z "${HW_CONFIG:-}" || -z "${FW_CONFIG:-}" ) ]]; then
        echo "Error: Must provide either --reference-fip OR both --hw-config and --fw-config"
        exit 1
fi

if [[ "$ENVIRONMENT" != "dev" && "$ENVIRONMENT" != "prod" ]]; then
        echo "Error: Invalid environment '$ENVIRONMENT'. Valid options are: dev, prod."
        exit 1
fi

if [[ "$PRODUCT" != "C3" ]]; then
        echo "Error: Invalid product '$PRODUCT'. Valid option is: C3."
        exit 1
fi

if [[ "$ENVIRONMENT" == "dev" ]]; then
        KEY_TYPE=dev
else
        KEY_TYPE=prod
        echo "Error: Production mode requires RoT key which may not be accessible."
        echo "This script currently only supports dev mode."
        exit 1
fi

PRODUCT_LOWER=${PRODUCT,,}
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT_DIR="${OUT_DIR:-${SCRIPT_DIR}/regenerated-config-certs}"
CERT_CREATE="/home/btcm/proto-distro/build/tmp-glibc/sysroots-components/aarch64/tf-a-tools-native/usr/bin/cert_create"
FIPTOOL="/home/btcm/proto-distro/build/tmp-glibc/sysroots-components/aarch64/tf-a-tools-native/usr/bin/fiptool"
COT_SIGNER="cot_signer"
KEYS_ROOT="${SCRIPT_DIR}"
CERTS_DIR="${KEYS_ROOT}/dev/certs/dev/${PRODUCT_LOWER}"
README_FILE="${KEYS_ROOT}/README.md"

# Check if running inside container
if [ ! -f "$CERT_CREATE" ]; then
    echo "Error: This script must be run inside the distro-builder Docker container"
    exit 1
fi

# RoT key (only available in dev)
ROT_KEY="${KEYS_ROOT}/stm32mp13_dev_keys/privateKey00.pem"
ROT_KEY_PUB="${KEYS_ROOT}/stm32mp13_dev_keys/publicKey00.pem"
ROT_KEY_PWD="qwert1"

# Intermediate keys (we'll use real ones to match existing certs)
TRUSTED_WORLD_KEY="${KEYS_ROOT}/dev/keys/dev/${PRODUCT_LOWER}/intermediate/${PRODUCT_LOWER}-trusted-world-key-dev.priv.pem"
NON_TRUSTED_WORLD_KEY="${KEYS_ROOT}/dev/keys/dev/${PRODUCT_LOWER}/intermediate/${PRODUCT_LOWER}-nontrusted-world-key-dev.priv.pem"

# Leaf keys
TOS_FW_KEY="${KEYS_ROOT}/dev/keys/dev/${PRODUCT_LOWER}/intermediate/${PRODUCT_LOWER}-trusted-os-firmware-key-dev.priv.pem"
NT_FW_KEY="${KEYS_ROOT}/dev/keys/dev/${PRODUCT_LOWER}/intermediate/${PRODUCT_LOWER}-nontrusted-os-firmware-key-dev.priv.pem"

# Setup temporary workspace (will be cleaned up)
TEMP_DIR=$(mktemp -d)
trap "rm -rf ${TEMP_DIR}" EXIT

WORK_DIR="${TEMP_DIR}/work"
TEMP_OUT_DIR="${TEMP_DIR}/certs"
mkdir -p "${WORK_DIR}" "${TEMP_OUT_DIR}"

echo "====================================="
echo "Canonical Config Cert Regeneration"
echo "====================================="
echo ""

# Extract configs from FIP or zip if needed
if [[ -n "${REFERENCE_FIP:-}" ]]; then
        if [[ ! -f "$REFERENCE_FIP" ]]; then
                echo "Error: Reference FIP/zip not found: $REFERENCE_FIP"
                exit 1
        fi

        # Check if it's a zip file
        if [[ "$REFERENCE_FIP" == *.zip ]]; then
                echo "Extracting from release zip..."
                EXTRACT_DIR="${TEMP_DIR}/extracted"
                unzip -q "$REFERENCE_FIP" -d "${EXTRACT_DIR}"
                FIP_FILE=$(find "${EXTRACT_DIR}" -name 'fip-*.bin' | head -1)
                if [[ -z "$FIP_FILE" ]]; then
                        echo "Error: No FIP file found in zip"
                        exit 1
                fi
                echo "  ✓ Found FIP: $(basename "$FIP_FILE")"
                REFERENCE_FIP="$FIP_FILE"
        fi

        echo "Extracting hw/fw configs from FIP..."
        $FIPTOOL unpack --out "${WORK_DIR}" "$REFERENCE_FIP"
        HW_CONFIG="${WORK_DIR}/hw-config.bin"
        FW_CONFIG="${WORK_DIR}/fw-config.bin"
        echo "  ✓ Extracted hw-config.bin"
        echo "  ✓ Extracted fw-config.bin"
else
        if [[ ! -f "$HW_CONFIG" || ! -f "$FW_CONFIG" ]]; then
                echo "Error: Provided config files not found"
                exit 1
        fi
        echo "Using provided hw/fw configs:"
        echo "  hw-config: $HW_CONFIG"
        echo "  fw-config: $FW_CONFIG"
        # Copy to work dir for consistent handling
        cp "$HW_CONFIG" "${WORK_DIR}/hw-config.bin"
        cp "$FW_CONFIG" "${WORK_DIR}/fw-config.bin"
        HW_CONFIG="${WORK_DIR}/hw-config.bin"
        FW_CONFIG="${WORK_DIR}/fw-config.bin"
fi

# Create dummy binaries for cert_create
echo ""
echo "Creating dummy binaries for cert_create..."
touch "${WORK_DIR}/bl2-fake.bin"
dd if=/dev/urandom of="${WORK_DIR}/nt-fw-fake.bin" bs=1024 count=512 2>/dev/null
dd if=/dev/urandom of="${WORK_DIR}/tos-fw-fake.bin" bs=1024 count=256 2>/dev/null
dd if=/dev/urandom of="${WORK_DIR}/tos-fw-extra1-fake.bin" bs=1024 count=512 2>/dev/null
dd if=/dev/urandom of="${WORK_DIR}/tos-fw-extra2-fake.bin" bs=1024 count=1024 2>/dev/null

# Generate certificates with cert_create
echo ""
echo "Generating certificates with cert_create..."
echo "  • Using REAL RoT key (for signing root certs)"
echo "  • Using REAL intermediate keys (for matching existing intermediate certs)"
echo "  • Using REAL leaf keys (for consistency)"

$CERT_CREATE -n \
    --key-alg ecdsa \
    --hash-alg sha256 \
    --tfw-nvctr 2 \
    --ntfw-nvctr 2 \
    --rot-key "${ROT_KEY}" \
    --rot-key-pwd "${ROT_KEY_PWD}" \
    --trusted-world-key "${TRUSTED_WORLD_KEY}" \
    --non-trusted-world-key "${NON_TRUSTED_WORLD_KEY}" \
    --tos-fw-key "${TOS_FW_KEY}" \
    --nt-fw-key "${NT_FW_KEY}" \
    --tb-fw "${WORK_DIR}/bl2-fake.bin" \
    --hw-config "${HW_CONFIG}" \
    --fw-config "${FW_CONFIG}" \
    --nt-fw "${WORK_DIR}/nt-fw-fake.bin" \
    --tos-fw "${WORK_DIR}/tos-fw-fake.bin" \
    --tos-fw-extra1 "${WORK_DIR}/tos-fw-extra1-fake.bin" \
    --tos-fw-extra2 "${WORK_DIR}/tos-fw-extra2-fake.bin" \
    --tb-fw-cert "${TEMP_OUT_DIR}/tb_fw.crt" \
    --trusted-key-cert "${TEMP_OUT_DIR}/trusted_key.crt" \
    --tos-fw-key-cert "${TEMP_OUT_DIR}/tos_fw_key.crt" \
    --tos-fw-cert "${TEMP_OUT_DIR}/tos_fw_content.crt" \
    --nt-fw-key-cert "${TEMP_OUT_DIR}/nt_fw_key.crt" \
    --nt-fw-cert "${TEMP_OUT_DIR}/nt_fw_content.crt" \
    --stm32mp-cfg-cert "${TEMP_OUT_DIR}/stm32mp_cfg_cert.crt"

echo "  ✓ Certificates generated"

# Verify the new certs
echo ""
echo "Verifying new certificate chain..."
$COT_SIGNER verify-cot \
    --product "${PRODUCT}" \
    --rot-key-pk "${ROT_KEY_PUB}" \
    --hw-config "${HW_CONFIG}" \
    --fw-config "${FW_CONFIG}" \
    --tos-fw "${WORK_DIR}/tos-fw-fake.bin" \
    --tos-fw-extra1 "${WORK_DIR}/tos-fw-extra1-fake.bin" \
    --tos-fw-extra2 "${WORK_DIR}/tos-fw-extra2-fake.bin" \
    --nt-fw "${WORK_DIR}/nt-fw-fake.bin" \
    --certs-dir "${TEMP_OUT_DIR}/"

echo "  ✓ Certificate chain verified successfully!"

# Update canonical certs in place
echo ""
echo "Updating canonical certificates..."
cp "${TEMP_OUT_DIR}/tb_fw.crt" "${CERTS_DIR}/"
cp "${TEMP_OUT_DIR}/stm32mp_cfg_cert.crt" "${CERTS_DIR}/"
echo "  ✓ Updated tb_fw.crt"
echo "  ✓ Updated stm32mp_cfg_cert.crt"

# Update README with version info
if [[ -n "${VERSION:-}" && -f "${README_FILE}" ]]; then
    echo ""
    echo "Updating README with version info..."
    CURRENT_DATE=$(date +%Y-%m-%d)

    # Update "Current version" line
    sed -i.bak "s/^\*\*Current version:\*\* Generated from \*\*v.*\*\* release DTBs/\*\*Current version:\*\* Generated from \*\*v${VERSION}\*\* release DTBs/" "${README_FILE}"

    # Update "Last updated" line
    sed -i.bak "s/^\*\*Last updated:\*\* .*/\*\*Last updated:\*\* ${CURRENT_DATE}/" "${README_FILE}"

    # Add to history (if not already present)
    if ! grep -q "${CURRENT_DATE}: Updated to v${VERSION}" "${README_FILE}"; then
        sed -i.bak "/^\*\*Certificate history:\*\*/a\\
- ${CURRENT_DATE}: Updated to v${VERSION} DTBs" "${README_FILE}"
    fi

    rm -f "${README_FILE}.bak"
    echo "  ✓ Updated README.md"
fi

# Save reference configs
mkdir -p "${OUT_DIR}"
cp "${HW_CONFIG}" "${OUT_DIR}/hw-config.bin.reference"
cp "${FW_CONFIG}" "${OUT_DIR}/fw-config.bin.reference"

# Save version information
if [[ -n "${VERSION:-}" ]]; then
    echo "${VERSION}" > "${OUT_DIR}/VERSION.txt"
fi

# Update metadata file (preserve prod section if it exists)
METADATA_FILE="${OUT_DIR}/METADATA.txt"
HW_CONFIG_HASH=$(sha256sum "${HW_CONFIG}" | cut -d' ' -f1)

# Extract existing prod section if present (from "Prod hw-config:" to end of file)
PROD_SECTION=""
if [[ -f "${METADATA_FILE}" ]] && grep -q "^Prod hw-config:" "${METADATA_FILE}"; then
    PROD_SECTION=$(sed -n '/^Prod hw-config:/,$p' "${METADATA_FILE}")
fi

# Write new metadata with dev info
cat > "${METADATA_FILE}" << EOF
Version: ${VERSION:-unknown}
Product: ${PRODUCT}

Dev hw-config:
  File: hw-config.bin.reference
  Generated: $(date -u +"%Y-%m-%d %H:%M:%S UTC")
  SHA256: ${HW_CONFIG_HASH}
EOF

# Append prod section if it existed (with blank line separator)
if [[ -n "${PROD_SECTION}" ]]; then
    printf "\n%s\n" "${PROD_SECTION}" >> "${METADATA_FILE}"
fi

echo ""
echo "====================================="
echo "✓ Config Certs Updated Successfully"
echo "====================================="
echo ""
echo "Updated files:"
echo "  ✓ dev/certs/dev/${PRODUCT_LOWER}/tb_fw.crt"
echo "  ✓ dev/certs/dev/${PRODUCT_LOWER}/stm32mp_cfg_cert.crt"
if [[ -n "${VERSION:-}" ]]; then
    echo "  ✓ README.md (version: ${VERSION})"
fi
echo ""
echo "Reference files saved in: ${OUT_DIR}/"
echo "  - hw-config.bin.reference"
echo "  - fw-config.bin.reference"
echo "  - METADATA.txt"
if [[ -n "${VERSION:-}" ]]; then
    echo "  - VERSION.txt"
fi
echo ""
echo "Next steps:"
echo "  1. Review changes:"
echo "     git diff dev/certs/dev/${PRODUCT_LOWER}/tb_fw.crt"
echo "     git diff dev/certs/dev/${PRODUCT_LOWER}/stm32mp_cfg_cert.crt"
echo "     git diff README.md"
echo ""
echo "  2. Test signing with updated certs (should verify successfully)"
echo ""
echo "  3. If everything looks good, commit:"
if [[ -n "${VERSION:-}" ]]; then
    echo "     git add dev/certs/dev/${PRODUCT_LOWER}/tb_fw.crt dev/certs/dev/${PRODUCT_LOWER}/stm32mp_cfg_cert.crt README.md"
    echo "     git commit -m 'Update canonical config certs for v${VERSION} DTBs'"
else
    echo "     git add dev/certs/dev/${PRODUCT_LOWER}/tb_fw.crt dev/certs/dev/${PRODUCT_LOWER}/stm32mp_cfg_cert.crt"
    echo "     git commit -m 'Update canonical config certs'"
fi
echo ""

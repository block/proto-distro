#!/bin/bash

# Helper script for signing and verifying FIT images with U-Boot keys
# Usage: sign-fitimage-helper.sh <keydir> <fitimage> <uboot_dtb> <key_name>

set -euo pipefail

MKIMAGE="mkimage"
FIT_CHECK_SIGN="fit_check_sign"

# Function to check if required tools are available
check_tools() {
    if ! command -v "${MKIMAGE}" >/dev/null 2>&1; then
        echo "Error: Required tool '${MKIMAGE}' not found in PATH"
        echo "Please ensure mkimage is installed and available in your PATH."
        exit 1
    fi

    if ! command -v "${FIT_CHECK_SIGN}" >/dev/null 2>&1; then
        echo "Error: Required tool '${FIT_CHECK_SIGN}' not found in PATH"
        echo "Please ensure fit_check_sign is installed and available in your PATH."
        exit 1
    fi
}

# Function to print usage
usage() {
    echo "Usage: $0 <keydir> <fitimage> <uboot_dtb> <key_name>"
    echo ""
    echo "Arguments:"
    echo "  keydir    - Directory containing the signing keys"
    echo "  fitimage  - Path to the FIT image file to sign (will be modified in place)"
    echo "  uboot_dtb - Path to the U-Boot device tree blob (will be modified in place)"
    echo "  key_name  - Name of the key (without .key/.crt extension)"
    echo ""
    echo "Example:"
    echo "  $0 /path/to/keys fitImage u-boot.dtb linux-signing-key"
    echo ""
    echo "Note: Both fitimage and uboot_dtb will be signed/modified in place."
    exit 1
}

# Check if correct number of arguments provided
if [ $# -ne 4 ]; then
    echo "Error: Incorrect number of arguments"
    usage
fi

# Parse arguments
KEY_DIR="$1"
FIT_IMAGE="$2"
UBOOT_DTB="$3"
KEY_NAME="$4"

# Define key file paths
KEY_FILE="${KEY_DIR}/${KEY_NAME}.key"
CRT_FILE="${KEY_DIR}/${KEY_NAME}.crt"

# Check if required tools are available in PATH
echo "Checking for required tools..."
check_tools
echo "All required tools found."
echo ""

echo "=== FIT Image Signing and Verification ==="
echo "Key Directory: ${KEY_DIR}"
echo "FIT Image: ${FIT_IMAGE}"
echo "U-Boot DTB: ${UBOOT_DTB}"
echo "Key Name: ${KEY_NAME}"
echo ""

# Validate existence of input files and directories
echo "Validating input files and directories..."

if [ ! -d "${KEY_DIR}" ]; then
    echo "Error: Key directory '${KEY_DIR}' does not exist"
    exit 1
fi

if [ ! -f "${FIT_IMAGE}" ]; then
    echo "Error: FIT image file '${FIT_IMAGE}' does not exist"
    exit 1
fi

if [ ! -f "${UBOOT_DTB}" ]; then
    echo "Error: U-Boot DTB '${UBOOT_DTB}' does not exist"
    exit 1
fi

# Validate existence of key files
echo "Validating key files..."

if [ ! -f "${KEY_FILE}" ]; then
    echo "Error: Key file '${KEY_FILE}' does not exist"
    exit 1
fi

if [ ! -f "${CRT_FILE}" ]; then
    echo "Error: Certificate file '${CRT_FILE}' does not exist"
    exit 1
fi

# Verify that the public key matches the one from the cert
echo "Verifying public key..."
echo "Extracting public key from certificate: ${CRT_FILE}"

# Extract public key from certificate
if ! EXPECTED_PUBKEY=$(openssl x509 -in "${CRT_FILE}" -pubkey -noout 2>/dev/null); then
    echo "Error: Failed to extract public key from certificate file '${CRT_FILE}'"
    exit 1
fi

echo "Extracting public key from private key: ${KEY_FILE}"

# Extract public key from private key file (suppress "writing RSA key" message on stderr)
if ! ACTUAL_PUBKEY=$(openssl rsa -in "${KEY_FILE}" -pubout -outform PEM 2>/dev/null); then
    echo "Error: Failed to extract public key from private key file '${KEY_FILE}'"
    exit 1
fi

if [ "${ACTUAL_PUBKEY}" != "${EXPECTED_PUBKEY}" ]; then
    echo "Error: Public key does not match expected value"
    echo ""
    echo "Expected public key (from certificate):"
    echo "${EXPECTED_PUBKEY}"
    echo ""
    echo "Actual public key (from private key):"
    echo "${ACTUAL_PUBKEY}"
    echo ""
    echo "Certificate subject:"
    openssl x509 -in "${CRT_FILE}" -subject -noout || true
    echo ""
    echo "Private key modulus (first 64 chars):"
    openssl rsa -in "${KEY_FILE}" -modulus -noout 2>/dev/null | head -c 64 || echo "(failed to extract)"
    echo ""
    exit 1
fi

echo "All input files validated successfully."
echo ""

# Sign the FIT image
echo "Signing FIT image with mkimage..."
echo "Command: ${MKIMAGE} -F -k ${KEY_DIR} -K ${UBOOT_DTB} ${FIT_IMAGE}"

if ! ${MKIMAGE} -F -k "${KEY_DIR}" -K "${UBOOT_DTB}" "${FIT_IMAGE}"; then
    echo "Error: Failed to sign FIT image with mkimage"
    exit 1
fi

echo "FIT image signed successfully."
echo ""

# Verify the signature
echo "Verifying signature with fit_check_sign..."
echo "Command: ${FIT_CHECK_SIGN} -f ${FIT_IMAGE} -k ${UBOOT_DTB}"

VERIFY_OUTPUT=$(${FIT_CHECK_SIGN} -f "${FIT_IMAGE}" -k "${UBOOT_DTB}" 2>&1)
VERIFY_EXIT_CODE=$?

echo "Verification output:"
echo "${VERIFY_OUTPUT}"
echo ""

if [ ${VERIFY_EXIT_CODE} -ne 0 ]; then
    echo "Error: fit_check_sign command failed with exit code ${VERIFY_EXIT_CODE}"
    exit 1
fi

# Check if "Signature check OK" is in the output
if echo "${VERIFY_OUTPUT}" | grep -q "Signature check OK"; then
    echo "✓ Signature verification successful!"
    echo ""
    echo "=== FIT image signing and verification completed successfully ==="
    exit 0
else
    echo "Error: Signature verification failed - 'Signature check OK' not found in output"
    exit 1
fi

#!/bin/bash

# Script for signing the U-Boot script FIT image from the bootfs filesystem
# Extracts FIT image from bootfs, signs it using sign-fitimage-helper.sh,
# and repackages it back into bootfs
# Usage: sign-fitimage-uboot-scr.sh <keydir> <bootfs> <uboot_dtb> <outdir> <key_name>

set -euo pipefail

# Get the directory where this script is located
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SIGN_HELPER="${SCRIPT_DIR}/sign-fitimage-helper.sh"

# U-Boot script FIT image path in bootfs
UBOOT_SCR_PATH="/boot.scr.uimg"

# Function to print usage
usage() {
    echo "Usage: $0 <keydir> <bootfs> <uboot_dtb> <outdir> <key_name>"
    echo ""
    echo "Arguments:"
    echo "  keydir    - Directory containing the signing keys"
    echo "  bootfs    - Path to the bootfs.ext4 file containing the U-Boot script FIT image"
    echo "  uboot_dtb - Path to the U-Boot device tree blob"
    echo "  outdir    - Output directory for signed files"
    echo "  key_name  - Name of the key (without .key/.crt extension)"
    echo ""
    echo "Example:"
    echo "  $0 /path/to/keys miner-image.bootfs.ext4 u-boot.dtb /output/dir uboot-script-signing-key"
    echo ""
    echo "This script will:"
    echo "  1. Extract the U-Boot script FIT image from bootfs"
    echo "  2. Sign the FIT image using sign-fitimage-helper.sh"
    echo "  3. Create a copy of bootfs with the signed FIT image"
    exit 1
}

# Check if correct number of arguments provided
if [ $# -ne 5 ]; then
    echo "Error: Incorrect number of arguments"
    usage
fi

# Parse arguments
KEY_DIR="$1"
BOOTFS="$2"
UBOOT_DTB="$3"
OUT_DIR="$4"
KEY_NAME="$5"

# Define output file paths
RESIGNED_FIT_IMAGE="${OUT_DIR}/resigned-boot.scr.uimg"
RESIGNED_UBOOT_DTB="${OUT_DIR}/resigned-u-boot.dtb"
RESIGNED_BOOTFS="${OUT_DIR}/resigned-$(basename "${BOOTFS}")"

echo "=== U-Boot Script FIT Image Signing from Bootfs ==="
echo "Key Directory: ${KEY_DIR}"
echo "Input Bootfs: ${BOOTFS}"
echo "Input U-Boot DTB: ${UBOOT_DTB}"
echo "Output Directory: ${OUT_DIR}"
echo "Key Name: ${KEY_NAME}"
echo ""

# Validate helper script exists
if [ ! -f "${SIGN_HELPER}" ]; then
    echo "Error: Helper script '${SIGN_HELPER}' not found"
    echo "Expected location: ${SIGN_HELPER}"
    exit 1
fi

# Validate existence of input files and directories
echo "Validating input files and directories..."

if [ ! -d "${KEY_DIR}" ]; then
    echo "Error: Key directory '${KEY_DIR}' does not exist"
    exit 1
fi

if [ ! -f "${BOOTFS}" ]; then
    echo "Error: Bootfs file '${BOOTFS}' does not exist"
    exit 1
fi

if [ ! -f "${UBOOT_DTB}" ]; then
    echo "Error: U-Boot DTB '${UBOOT_DTB}' does not exist"
    exit 1
fi

if [ ! -d "${OUT_DIR}" ]; then
    echo "Creating output directory '${OUT_DIR}'"
    mkdir -p "${OUT_DIR}"
fi

echo "All input files validated successfully."
echo ""

# Extract U-Boot script FIT image from bootfs
echo "Extracting U-Boot script FIT image from bootfs..."

# Check if the file exists in bootfs
if ! debugfs -R "stat ${UBOOT_SCR_PATH}" "${BOOTFS}" >/dev/null 2>&1; then
    echo "Error: Could not find U-Boot script FIT image at ${UBOOT_SCR_PATH} in bootfs"
    exit 1
fi

echo "Found U-Boot script FIT image: ${UBOOT_SCR_PATH}"

# Extract the FIT image to a temporary file
TEMP_FIT_IMAGE="${OUT_DIR}/temp-boot.scr.uimg"
echo "Extracting U-Boot script FIT image to temporary file..."

if ! debugfs -R "dump ${UBOOT_SCR_PATH} ${TEMP_FIT_IMAGE}" "${BOOTFS}"; then
    echo "Error: Failed to extract U-Boot script FIT image from bootfs"
    exit 1
fi

echo "U-Boot script FIT image extracted successfully."
echo ""

# Copy files to output directory
echo "Preparing files for signing..."
cp "${UBOOT_DTB}" "${RESIGNED_UBOOT_DTB}"
cp "${TEMP_FIT_IMAGE}" "${RESIGNED_FIT_IMAGE}"
echo ""

# Call the signing helper script
echo "Calling sign-fitimage-helper.sh..."
echo "Command: ${SIGN_HELPER} ${KEY_DIR} ${RESIGNED_FIT_IMAGE} ${RESIGNED_UBOOT_DTB} ${KEY_NAME}"
echo ""

if ! "${SIGN_HELPER}" "${KEY_DIR}" "${RESIGNED_FIT_IMAGE}" "${RESIGNED_UBOOT_DTB}" "${KEY_NAME}"; then
    echo "Error: Signing helper script failed"
    exit 1
fi

echo ""
echo "U-Boot script FIT image signed and verified successfully."
echo ""

# Copy bootfs to output directory first
echo "Copying bootfs to output directory..."
cp "${BOOTFS}" "${RESIGNED_BOOTFS}"

# Now put the signed FIT image back into the copied bootfs
echo "Updating bootfs copy with signed U-Boot script FIT image..."

# Remove the old FIT image from bootfs
if ! debugfs -w -R "rm ${UBOOT_SCR_PATH}" "${RESIGNED_BOOTFS}"; then
    echo "Error: Failed to remove old U-Boot script FIT image from bootfs"
    exit 1
fi

# Write the new signed FIT image to bootfs
if ! debugfs -w -R "write ${RESIGNED_FIT_IMAGE} ${UBOOT_SCR_PATH}" "${RESIGNED_BOOTFS}"; then
    echo "Error: Failed to write signed U-Boot script FIT image to bootfs"
    exit 1
fi

# Set proper file permissions
if ! debugfs -w -R "set_inode_field ${UBOOT_SCR_PATH} mode 0100644" "${RESIGNED_BOOTFS}"; then
    echo "Error: Failed to set file permissions for U-Boot script FIT image in bootfs"
    exit 1
fi

echo "✓ Bootfs copy updated with signed U-Boot script FIT image successfully!"
echo ""

# Clean up temporary file
rm -f "${TEMP_FIT_IMAGE}"

echo "=== Signing completed successfully ==="
echo "Signed files available at:"
echo "  ${RESIGNED_FIT_IMAGE}"
echo "  ${RESIGNED_UBOOT_DTB}"
echo "  ${RESIGNED_BOOTFS}"

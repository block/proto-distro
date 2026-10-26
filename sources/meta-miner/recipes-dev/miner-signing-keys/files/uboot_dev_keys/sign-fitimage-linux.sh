#!/bin/bash

# Script for signing Linux FIT images from rootfs filesystem
# Extracts FIT image from rootfs, signs it using sign-fitimage-helper.sh,
# and repackages it back into rootfs
# Usage: sign-fitimage-linux.sh <keydir> <rootfs> <uboot_dtb> <outdir> <key_name>

set -euo pipefail

# Get the directory where this script is located
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SIGN_HELPER="${SCRIPT_DIR}/sign-fitimage-helper.sh"

# Function to print usage
usage() {
    echo "Usage: $0 <keydir> <rootfs> <uboot_dtb> <outdir> <key_name>"
    echo ""
    echo "Arguments:"
    echo "  keydir    - Directory containing the signing keys"
    echo "  rootfs    - Path to the rootfs.ext4 file containing the FIT image"
    echo "  uboot_dtb - Path to the U-Boot device tree blob"
    echo "  outdir    - Output directory for signed files"
    echo "  key_name  - Name of the key (without .key/.crt extension)"
    echo ""
    echo "Example:"
    echo "  $0 /path/to/keys miner-image.rootfs.ext4 u-boot.dtb /output/dir linux-signing-key"
    echo ""
    echo "This script will:"
    echo "  1. Extract the FIT image from rootfs"
    echo "  2. Sign the FIT image using sign-fitimage-helper.sh"
    echo "  3. Create a copy of rootfs with the signed FIT image"
    exit 1
}

# Check if correct number of arguments provided
if [ $# -ne 5 ]; then
    echo "Error: Incorrect number of arguments"
    usage
fi

# Parse arguments
KEY_DIR="$1"
ROOTFS="$2"
UBOOT_DTB="$3"
OUT_DIR="$4"
KEY_NAME="$5"

# Define output file paths
RESIGNED_FIT_IMAGE="${OUT_DIR}/resigned-fitImage"
RESIGNED_UBOOT_DTB="${OUT_DIR}/resigned-u-boot.dtb"
RESIGNED_ROOTFS="${OUT_DIR}/resigned-$(basename "${ROOTFS}")"

echo "=== FIT Image Signing from Rootfs ==="
echo "Key Directory: ${KEY_DIR}"
echo "Input Rootfs: ${ROOTFS}"
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

if [ ! -f "${ROOTFS}" ]; then
    echo "Error: Rootfs file '${ROOTFS}' does not exist"
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

# Extract FIT image from rootfs
echo "Extracting FIT image from rootfs..."

# Get the FIT image filename from the rootfs
FIT_FILENAME=$(debugfs -R "stat /boot/fitImage" "${ROOTFS}" | grep "Fast link dest" | cut -d ":" -f 2 | tr -d ' "')

if [ -z "${FIT_FILENAME}" ]; then
    echo "Error: Could not find FIT image symlink in rootfs /boot/fitImage"
    exit 1
fi

# Validate FIT_FILENAME
# Only allow alphanumeric characters, dots, dashes, and underscores
if ! [[ "${FIT_FILENAME}" =~ ^[a-zA-Z0-9._-]+$ ]]; then
    echo "Error: Invalid FIT image filename '${FIT_FILENAME}'"
    echo "Filename must contain only alphanumeric characters, dots, dashes, and underscores"
    exit 1
fi

echo "Found FIT image: ${FIT_FILENAME}"

# Extract the FIT image to a temporary file
TEMP_FIT_IMAGE="${OUT_DIR}/temp-fitImage"
echo "Extracting FIT image to temporary file..."

if ! debugfs -R "dump /boot/${FIT_FILENAME} ${TEMP_FIT_IMAGE}" "${ROOTFS}"; then
    echo "Error: Failed to extract FIT image from rootfs"
    exit 1
fi

echo "FIT image extracted successfully."
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
echo "FIT image signed and verified successfully."
echo ""

# Copy rootfs to output directory first
echo "Copying rootfs to output directory..."
cp "${ROOTFS}" "${RESIGNED_ROOTFS}"

# Now put the signed FIT image back into the copied rootfs
echo "Updating rootfs copy with signed FIT image..."

# Remove the old FIT image from rootfs
if ! debugfs -w -R "rm /boot/${FIT_FILENAME}" "${RESIGNED_ROOTFS}"; then
    echo "Error: Failed to remove old FIT image from rootfs"
    exit 1
fi

# Write the new signed FIT image to rootfs
if ! debugfs -w -R "write ${RESIGNED_FIT_IMAGE} /boot/${FIT_FILENAME}" "${RESIGNED_ROOTFS}"; then
    echo "Error: Failed to write signed FIT image to rootfs"
    exit 1
fi

# Set proper file permissions
if ! debugfs -w -R "set_inode_field /boot/${FIT_FILENAME} mode 0100644" "${RESIGNED_ROOTFS}"; then
    echo "Error: Failed to set file permissions for FIT image in rootfs"
    exit 1
fi

echo "✓ Rootfs copy updated with signed FIT image successfully!"
echo ""

# Clean up temporary file
rm -f "${TEMP_FIT_IMAGE}"

echo "=== Signing completed successfully ==="
echo "Signed files available at:"
echo "  ${RESIGNED_FIT_IMAGE}"
echo "  ${RESIGNED_UBOOT_DTB}"
echo "  ${RESIGNED_ROOTFS}"

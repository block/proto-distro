#!/bin/bash
set -e pipefail

fsbl_signer gen-pk-hashes \
	--pk ./keys/prod/c3/root/c3-root-of-trust-key-1-prod.pub.pem \
	--pk ./keys/prod/c3/root-backup/c3-root-of-trust-key-2-prod.pub.pem \
	--pk-hashes-out pkhash/

pushd pkhash/

hash1=$(cat publicKeyHash00.bin publicKeyHash01.bin publicKeyHash02.bin publicKeyHash03.bin publicKeyHash04.bin publicKeyHash05.bin publicKeyHash06.bin publicKeyHash07.bin | sha256sum)
hash2=$(xxd -p -c0 publicKeysHashHashes.bin)

popd

# Remove the dash and trailing spaces from hash1
hash1=$(echo "$hash1" | sed 's/-//g' | sed 's/ //g')

# Check if the two hashes match
if [ "$hash1" = "$hash2" ]; then
	echo "Hashes match"
	echo "Hash: $hash1"
else
	echo "Hashes do not match"
	echo "Hash1: $hash1"
	echo "Hash2: $hash2"
fi

# The production signed FSBL can be downloaded from https://github.com/block/miner-firmware/releases/tag/distro-0.2.11
fsbl_signer verify \
	-i ~/Desktop/tf-a-stm32mp131f-c3p1-usb-0.2.11_ProdSigned.stm32 \
	--pk ./keys/prod/c3/root/c3-root-of-trust-key-1-prod.pub.pem \
	--pk ./keys/prod/c3/root-backup/c3-root-of-trust-key-2-prod.pub.pem \
	--pk-hashes-in pkhash/publicKeysHashHashes.bin \
	--product C3

# Uncomment the following line to write the public keys hash to OTP memory
# STM32_Programmer_CLI -c port=USB1 -otp fwrite pkhash/publicKeysHashHashes.bin word=24 word=24

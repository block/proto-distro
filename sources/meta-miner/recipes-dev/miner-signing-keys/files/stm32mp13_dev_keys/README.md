# Generating keys and hashes
Keys in this folder were generated using following command:

```bash
# Generate first key with password: qwert1
openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:prime256v1 -out privateKey00.pem -aes256

openssl pkey -in privateKey00.pem -pubout -out publicKey00.pem

# Generate second key with password: qwert2
openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:prime256v1 -out privateKey01.pem -aes256

openssl pkey -in privateKey01.pem -pubout -out publicKey01.pem
```

Hash values in this folder were generated with:
```bash
fsbl_signer gen-pk-hashes --pk publicKey00.pem --pk publicKey01.pem
```

Key hash values can be verified against the public keys hash table hash file by comparing the output of the following commands:
```bash
cat publicKeyHash00.bin publicKeyHash01.bin publicKeyHash02.bin publicKeyHash03.bin publicKeyHash04.bin publicKeyHash05.bin publicKeyHash06.bin publicKeyHash07.bin | sha256sum

xxd -p -c0 publicKeysHashHashes.bin
```

# Signing FSBL (TF-A) FW
```bash
fsbl_signer sign -i /path/to/tf-a-stm32mp131f-c2evt-uart.stm32 --pk publicKey00.pem --pk publicKey01.pem --sk privateKey00.pem --pwd qwert1 --product C3 -o /path/to/tf-a-stm32mp131f-c2evt-uart_Signed.stm32
```
PS: The order of public key files passed into `fsbl_signer` is IMPORTANT!!!

Signed images can be verified:
```bash
fsbl_signer verify -i /path/to/tf-a-stm32mp131f-c2evt-uart_Signed.stm32 --pk publicKey00.pem --pk publicKey01.pem --pk-hashes-in publicKeysHashHashes.bin --product C3
```

# Signing FIP image
FIP image is image package that include optee + uboot FW and their corresponding device tree binaries.

```
cert_create -n --tfw-nvctr 0 --ntfw-nvctr 0 --key-alg ecdsa --hash-alg sha256  \
--rot-key proto_dev_rot.crt
--rot-key-pwd
--fw-config tmp-glibc/work/c2_evt-oe-linux-gnueabi/tf-a-stm32mp/v2.6-stm32mp-r2.1-r0/deploy-tf-a-stm32mp/arm-trusted-firmware/fwconfig/stm32mp131f-c2evt-fw-config-optee.dtb
--hw-config tmp-glibc/deploy/images/c2-evt/u-boot/u-boot-stm32mp131f-c2evt.dtb
--nt-fw tmp-glibc/deploy/images/c2-evt/u-boot/u-boot-nodtb-stm32mp13.bin
--tos-fw tmp-glibc/deploy/images/c2-evt/optee/tee-header_v2-stm32mp131f-c2evt.bin
--tos-fw-extra1 tmp-glibc/deploy/images/c2-evt/optee/tee-pager_v2-stm32mp131f-c2evt.bin
--tos-fw-extra2 tmp-glibc/deploy/images/c2-evt/optee/tee-pageable_v2-stm32mp131f-c2evt.bin
--tb-fw-cert tmp-glibc/work/c2_evt-oe-linux-gnueabi/tf-a-stm32mp/v2.6-stm32mp-r2.1-r0/tb_fw.crt
--trusted-key-cert tmp-glibc/work/c2_evt-oe-linux-gnueabi/tf-a-stm32mp/v2.6-stm32mp-r2.1-r0/trusted_key.crt
--nt-fw-cert tmp-glibc/work/c2_evt-oe-linux-gnueabi/tf-a-stm32mp/v2.6-stm32mp-r2.1-r0/nt_fw_content.crt
--nt-fw-key-cert tmp-glibc/work/c2_evt-oe-linux-gnueabi/tf-a-stm32mp/v2.6-stm32mp-r2.1-r0/nt_fw_key.crt
--tos-fw-cert tmp-glibc/work/c2_evt-oe-linux-gnueabi/tf-a-stm32mp/v2.6-stm32mp-r2.1-r0/tos_fw_content.crt
--tos-fw-key-cert tmp-glibc/work/c2_evt-oe-linux-gnueabi/tf-a-stm32mp/v2.6-stm32mp-r2.1-r0/tos_fw_key.crt
--stm32mp-cfg-cert tmp-glibc/work/c2_evt-oe-linux-gnueabi/tf-a-stm32mp/v2.6-stm32mp-r2.1-r0/stm32mp_cfg_cert.crt
--tb-fw tmp-glibc/work/c2_evt-oe-linux-gnueabi/tf-a-stm32mp/v2.6-stm32mp-r2.1-r0/bl2-fake.bin
```

# Fusing PKHASH OTP

This command will burn the PKHASH values onto the OTP with OUT closing/locking the STM32MP131f
```
STM32_Programmer_CLI -c port=USB1 -otp fwrite sources/meta-miner/recipes-core/miner-signing-keys/files/dev_rom_signing_keys/publicKeysHashHashes.bin word=24
```

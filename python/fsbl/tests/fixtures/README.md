# Test Fixtures for FSBL Signer

This directory contains ECC key fixtures for testing passwordless signing functionality.

## Files

- `privateKey00_unencrypted.pem` - Unencrypted NIST P-256 private key
- `publicKey00_unencrypted.pem` - Corresponding public key
- `privateKey00_encrypted.pem` - AES-256 encrypted NIST P-256 private key (password: `testpassword`)

## Purpose

These fixtures are used to test:
1. **Passwordless signing** - Verifies that unencrypted keys work without `--pwd` parameter
2. **Password handling** - Ensures encrypted keys still require passwords
3. **Error messages** - Validates clear error messages when password is needed but not provided
4. **Warnings** - Checks that unnecessary passwords trigger warnings

## Generation

The keys were generated using OpenSSL:

```bash
# Unencrypted key pair
openssl ecparam -name prime256v1 -genkey -noout -out privateKey00_unencrypted.pem
openssl ec -in privateKey00_unencrypted.pem -pubout -out publicKey00_unencrypted.pem

# Encrypted key (password: testpassword)
openssl ecparam -name prime256v1 -genkey -noout | \
  openssl ec -aes256 -out privateKey00_encrypted.pem -passout pass:testpassword
```

## Security Note

**These keys are for testing only.** Never use these keys for production signing.

# OP-TEE Default Keys

This directory contains the default TA signing key from OP-TEE for development and testing purposes.

For production use, you MUST generate your own private keys and keep them secure.

## Keys Included

### default_ta.pem

**Origin**:
- Source: https://github.com/OP-TEE/optee_os/blob/12fc37711783247b0d05fdc271ef007f4930767b/keys/default.pem
- Commit: 12fc37711783247b0d05fdc271ef007f4930767b
- OP-TEE version: v2.x and early v3.x

**Usage**:
- Signing TAs during build with `sign_encrypt.py`
- Embedded in OP-TEE OS for TA verification
- Used by optee_keys module for TA verification

## References

- [OP-TEE Documentation - Trusted Applications](https://optee.readthedocs.io/en/latest/building/trusted_applications.html)
- [OP-TEE OS Repository](https://github.com/OP-TEE/optee_os)
- [Original key source](https://github.com/OP-TEE/optee_os/blob/12fc37711783247b0d05fdc271ef007f4930767b/keys/default.pem)

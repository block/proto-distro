#ifndef LIB_MINER_OPTEE_DEVICE_IDENTITY_H
#define LIB_MINER_OPTEE_DEVICE_IDENTITY_H

/*
 * This UUID is generated with uuidgen
 * the ITU-T UUID generator at http://www.itu.int/ITU-T/asn1/uuid.html
 */
#define TA_PROVISION_UUID \
  {0x0d746117, 0x7c6a, 0x43e1, {0x89, 0xa3, 0x59, 0x27, 0x23, 0x8b, 0x28, 0xfe}}

/* The function IDs implemented in this TA */
#define TA_PROVISION_CMD_GENERATE_CSR                1
#define TA_PROVISION_CMD_READ_PUB_KEY                2
#define TA_PROVISION_CMD_VALIDATE_AND_WRITE_CERT     3
#define TA_PROVISION_CMD_READ_CERT                   4
#define TA_PROVISION_CMD_IS_CERT_VALID               5

/* Certificate validity status returned by IS_CERT_VALID */
typedef enum {
  CERT_STATUS_REVOKED   = 0x00,
  CERT_STATUS_VALID     = 0x01,
  CERT_STATUS_NOT_FOUND = 0x02,
} cert_validity_status_t;

/* RPMB definitions */
#define TA_PROVISION_KEY_PAIR_OBJ_ID    "proto-miner-key-pair"
#define TA_PROVISION_CERT_OBJ_ID        "proto-miner-cert"

/* BSEC PTA definitions */
#define PROVISION_TA_MFG_SERIAL_NUMBER_OTP_INDEX      59
#define PROVISION_TA_MFG_SERIAL_NUMBER_OTP_NUM_WORDS   4

#define PROVISION_TA_PKHASH_OTP_INDEX                 24
#define PROVISION_TA_PKHASH_OTP_NUM_WORDS              8

/* Key and object definitions */
#define ECC_PK_SIZE_BYTES          64
#define ECC_PK_X509_SIZE           91
#define DER_MAX_BUF_SIZE           1024
#define PEM_MAX_BUF_SIZE           2048
#define GENERIC_BUF_SIZE           256

/* Cert chain parsing helpers */
#define CMD_LEN_FIELD_LEN (sizeof(uint32_t))
#define CMD_NUM_CERTS_FIELD_LEN (sizeof(uint32_t))
#define MAX_CERTIFICATES_IN_CHAIN 5

#endif /* LIB_MINER_OPTEE_DEVICE_IDENTITY_H */

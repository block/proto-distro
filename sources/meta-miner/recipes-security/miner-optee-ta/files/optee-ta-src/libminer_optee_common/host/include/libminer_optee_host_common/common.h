#ifndef LIB_MINER_OPTEE_HOST_COMMON_H
#define LIB_MINER_OPTEE_HOST_COMMON_H

#include <stddef.h>
#include <stdint.h>

#include <tee_client_api.h>

#include <libminer_optee_ta_common/device_identity.h>

/* Provisioning command protocol */
#define MAX_COMMAND_SIZE 4096
#define CMD_ID_LEN 1
#define PAYLOAD_OFFSET (CMD_LEN_FIELD_LEN + CMD_ID_LEN)
#define HEADER_SIZE (PAYLOAD_OFFSET)
#define MAX_COMMAND_PAYLOAD_SIZE (MAX_COMMAND_SIZE - HEADER_SIZE)

#define CMD_ID_OFFSET 0x04 /* This follows a 4 byte length prefix */
#define GET_CMD_ID(buf) (*(uint8_t*)((buf) + CMD_ID_OFFSET))

typedef enum {
  CMD_GET_CSR_REQUEST   = 0x00,
  CMD_GET_CSR_RESPONSE  = 0x01,
  CMD_SEND_CERT_CHAIN   = 0x02,
  CMD_GET_CERT_REQUEST  = 0x03,
  CMD_GET_CERT_RESPONSE = 0x04,
  CMD_INTERNAL_ERR      = 0xFF
} cmd_id_t;

typedef struct device_identity_ctx {
  TEEC_Context ctx;
  TEEC_Session sess;
} device_identity_ctx_t;

TEEC_Result device_identity_prepare_tee_session(device_identity_ctx_t* ctx);
void device_identity_terminate_tee_session(device_identity_ctx_t* ctx);

TEEC_Result device_identity_generate_csr(device_identity_ctx_t* ctx,
                                         uint8_t* response_buffer,
                                         size_t response_size,
                                         size_t* out_size);

TEEC_Result device_identity_read_public_key(device_identity_ctx_t* ctx);

TEEC_Result device_identity_read_certificate(device_identity_ctx_t* ctx,
                                             uint8_t* response_buffer,
                                             size_t response_size,
                                             size_t* out_size);

TEEC_Result device_identity_validate_and_store_device_cert(device_identity_ctx_t* ctx,
                                                            const uint8_t* cert_chain_buf,
                                                            size_t cert_chain_size,
                                                            uint8_t* response_buffer,
                                                            size_t response_size,
                                                            size_t* out_size);

TEEC_Result device_identity_is_cert_valid(device_identity_ctx_t* ctx, cert_validity_status_t* status);

#endif /* LIB_MINER_OPTEE_HOST_COMMON_H */

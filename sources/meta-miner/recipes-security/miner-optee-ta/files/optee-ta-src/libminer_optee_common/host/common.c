#include "libminer_optee_host_common/common.h"

#include <stdio.h>
#include <string.h>

TEEC_Result device_identity_prepare_tee_session(device_identity_ctx_t* ctx) {
  TEEC_UUID uuid = TA_PROVISION_UUID;
  uint32_t origin = 0;
  TEEC_Result res;

  if (ctx == NULL) {
    return TEEC_ERROR_BAD_PARAMETERS;
  }

  res = TEEC_InitializeContext(NULL, &ctx->ctx);
  if (res != TEEC_SUCCESS) {
    printf("TEEC_InitializeContext failed with code 0x%x\n", res);
    return res;
  }

  res = TEEC_OpenSession(&ctx->ctx, &ctx->sess, &uuid, TEEC_LOGIN_PUBLIC, NULL, NULL, &origin);
  if (res != TEEC_SUCCESS) {
    printf("TEEC_OpenSession failed with code 0x%x origin 0x%x\n", res, origin);
    TEEC_FinalizeContext(&ctx->ctx);
    return res;
  }

  return TEEC_SUCCESS;
}

void device_identity_terminate_tee_session(device_identity_ctx_t* ctx) {
  if (ctx == NULL) {
    return;
  }

  TEEC_CloseSession(&ctx->sess);
  TEEC_FinalizeContext(&ctx->ctx);
}

TEEC_Result device_identity_read_public_key(device_identity_ctx_t* ctx) {
  TEEC_Operation op;
  uint32_t origin = 0;
  TEEC_Result res;

  if (ctx == NULL) {
    return TEEC_ERROR_BAD_PARAMETERS;
  }

  memset(&op, 0, sizeof(op));
  op.paramTypes = TEEC_PARAM_TYPES(TEEC_MEMREF_TEMP_OUTPUT, TEEC_NONE, TEEC_NONE, TEEC_NONE);

  uint8_t pem_buf[PEM_MAX_BUF_SIZE];
  op.params[0].tmpref.buffer = pem_buf;
  op.params[0].tmpref.size = sizeof(pem_buf);

  res = TEEC_InvokeCommand(&ctx->sess, TA_PROVISION_CMD_READ_PUB_KEY, &op, &origin);
  if (res != TEEC_SUCCESS) {
    printf("READ_PUB_KEY failed: 0x%x\n", res);
    return res;
  }

  printf("public key pem:\n%s\n", pem_buf);
  return TEEC_SUCCESS;
}

TEEC_Result device_identity_validate_and_store_device_cert(device_identity_ctx_t* ctx,
                                                           const uint8_t* cert_chain_buf,
                                                           size_t cert_chain_size,
                                                           uint8_t* response_buffer,
                                                           size_t response_size,
                                                           size_t* out_size) {
  TEEC_Operation op;
  uint32_t origin = 0;
  TEEC_Result res;

  if (ctx == NULL || cert_chain_buf == NULL || cert_chain_size == 0 || cert_chain_size > MAX_COMMAND_PAYLOAD_SIZE) {
    return TEEC_ERROR_BAD_PARAMETERS;
  }

  if (response_buffer == NULL || response_size < MAX_COMMAND_PAYLOAD_SIZE) {
    return TEEC_ERROR_BAD_PARAMETERS;
  }

  memset(&op, 0, sizeof(op));
  op.paramTypes = TEEC_PARAM_TYPES(TEEC_MEMREF_TEMP_INPUT, TEEC_MEMREF_TEMP_OUTPUT, TEEC_NONE, TEEC_NONE);
  op.params[0].tmpref.buffer = (void*)cert_chain_buf;
  op.params[0].tmpref.size = cert_chain_size;
  op.params[1].tmpref.buffer = (void*)response_buffer;
  op.params[1].tmpref.size = response_size;

  res = TEEC_InvokeCommand(&ctx->sess, TA_PROVISION_CMD_VALIDATE_AND_WRITE_CERT, &op, &origin);
  if (res != TEEC_SUCCESS) {
    printf("Cert chain validation failed: 0x%x\n", res);
    return res;
  }

  printf("Cert chain validation successful\nDevice certificate successfully stored\n");
  printf("Device certificate content:\n%.*s\n", (int)op.params[1].tmpref.size, (char*)response_buffer);

  if (out_size != NULL) {
    *out_size = op.params[1].tmpref.size;
  }

  return TEEC_SUCCESS;
}

TEEC_Result device_identity_read_certificate(device_identity_ctx_t* ctx,
                                             uint8_t* response_buffer,
                                             size_t response_size,
                                             size_t* out_size) {
  TEEC_Operation op;
  uint32_t origin = 0;
  TEEC_Result res;

  if (ctx == NULL || response_buffer == NULL || response_size < MAX_COMMAND_PAYLOAD_SIZE) {
    return TEEC_ERROR_BAD_PARAMETERS;
  }

  memset(&op, 0, sizeof(op));
  op.paramTypes = TEEC_PARAM_TYPES(TEEC_MEMREF_TEMP_OUTPUT, TEEC_NONE, TEEC_NONE, TEEC_NONE);
  op.params[0].tmpref.buffer = (void*)response_buffer;
  op.params[0].tmpref.size = response_size;

  res = TEEC_InvokeCommand(&ctx->sess, TA_PROVISION_CMD_READ_CERT, &op, &origin);
  if (res != TEEC_SUCCESS) {
    printf("Certificate read failed: 0x%x\n", res);
    return res;
  }

  if (op.params[0].tmpref.size == 0 || op.params[0].tmpref.size > MAX_COMMAND_PAYLOAD_SIZE - PAYLOAD_OFFSET) {
    printf("No certificate found or invalid certificate\n");
    return TEEC_ERROR_ITEM_NOT_FOUND;
  }

  printf("Certificate read successfully (%zu bytes)\n", op.params[0].tmpref.size);
  printf("Certificate content:\n%.*s\n", (int)op.params[0].tmpref.size, (char*)response_buffer);

  if (out_size != NULL) {
    *out_size = op.params[0].tmpref.size;
  }

  return TEEC_SUCCESS;
}

TEEC_Result device_identity_is_cert_valid(device_identity_ctx_t* ctx, cert_validity_status_t* status) {
  TEEC_Operation op;
  uint32_t origin = 0;
  TEEC_Result res;

  if (ctx == NULL || status == NULL) {
    return TEEC_ERROR_BAD_PARAMETERS;
  }

  uint8_t result_buf[1] = {0};

  memset(&op, 0, sizeof(op));
  op.paramTypes = TEEC_PARAM_TYPES(TEEC_MEMREF_TEMP_OUTPUT, TEEC_NONE, TEEC_NONE, TEEC_NONE);
  op.params[0].tmpref.buffer = result_buf;
  op.params[0].tmpref.size = sizeof(result_buf);

  res = TEEC_InvokeCommand(&ctx->sess, TA_PROVISION_CMD_IS_CERT_VALID, &op, &origin);
  if (res != TEEC_SUCCESS) {
    printf("is_cert_valid failed: 0x%x\n", res);
    return res;
  }

  *status = (cert_validity_status_t)result_buf[0];
  switch (*status) {
    case CERT_STATUS_VALID:
      printf("Device certificate: VALID\n");
      break;
    case CERT_STATUS_REVOKED:
      printf("Device certificate: REVOKED\n");
      break;
    case CERT_STATUS_NOT_FOUND:
      printf("Device certificate: NOT FOUND\n");
      break;
    default:
      printf("Device certificate: UNKNOWN status (0x%02x)\n", result_buf[0]);
      break;
  }

  return TEEC_SUCCESS;
}

TEEC_Result device_identity_generate_csr(device_identity_ctx_t* ctx,
                                         uint8_t* response_buffer,
                                         size_t response_size,
                                         size_t* out_size) {
  TEEC_Operation op;
  uint32_t origin = 0;
  TEEC_Result res;

  if (ctx == NULL || response_buffer == NULL || response_size < MAX_COMMAND_PAYLOAD_SIZE) {
    return TEEC_ERROR_BAD_PARAMETERS;
  }

  memset(&op, 0, sizeof(op));
  op.paramTypes = TEEC_PARAM_TYPES(TEEC_MEMREF_TEMP_OUTPUT, TEEC_NONE, TEEC_NONE, TEEC_NONE);
  op.params[0].tmpref.buffer = (void*)response_buffer;
  op.params[0].tmpref.size = response_size;

  res = TEEC_InvokeCommand(&ctx->sess, TA_PROVISION_CMD_GENERATE_CSR, &op, &origin);
  if (res != TEEC_SUCCESS) {
    printf("CSR generation failed: 0x%x\n", res);
    return res;
  }

  if (op.params[0].tmpref.size == 0 || op.params[0].tmpref.size > MAX_COMMAND_PAYLOAD_SIZE - PAYLOAD_OFFSET) {
    printf("Invalid CSR size: %zu bytes\n", op.params[0].tmpref.size);
    return TEEC_ERROR_BAD_PARAMETERS;
  }

  printf("CSR generation success in PEM format\n");
  printf("CSR content:\n%.*s\n", (int)op.params[0].tmpref.size, (char*)response_buffer);

  if (out_size != NULL) {
    *out_size = op.params[0].tmpref.size;
  }

  return TEEC_SUCCESS;
}

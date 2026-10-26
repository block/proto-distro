#include <attestation_ta.h>

#include <stdio.h>
#include <string.h>

#include "libminer_optee_host_common/common.h"

static void print_usage(const char* prog_name) {
  printf("Usage: %s <command>\n", prog_name);
  printf("Commands:\n");
  printf("  read_pub\n");
  printf("  read_cert\n");
  printf("  is_cert_valid\n");
}

static TEEC_Result handle_cli_command(device_identity_ctx_t* ctx, int argc, char* argv[]) {
  if (argc < 2) {
    print_usage(argv[0]);
    return TEEC_ERROR_BAD_PARAMETERS;
  }

  const char* cmd = argv[1];
  uint8_t response_buf[MAX_COMMAND_PAYLOAD_SIZE];

  if (!strcmp(cmd, "read_pub")) {
    printf("Reading public key\n");
    return device_identity_read_public_key(ctx);
  }

  if (!strcmp(cmd, "read_cert")) {
    printf("Reading certificate\n");
    return device_identity_read_certificate(ctx, response_buf, sizeof(response_buf), NULL);
  }

  if (!strcmp(cmd, "is_cert_valid")) {
    printf("Checking certificate validity\n");
    cert_validity_status_t status;
    return device_identity_is_cert_valid(ctx, &status);
  }

  printf("Invalid command: %s\n", argv[1]);
  print_usage(argv[0]);
  return TEEC_ERROR_BAD_PARAMETERS;
}

int main(int argc, char* argv[]) {
  device_identity_ctx_t ctx;
  TEEC_Result res;

  if (argc <= 1) {
    print_usage(argv[0]);
    return 1;
  }

  res = device_identity_prepare_tee_session(&ctx);
  if (res != TEEC_SUCCESS) {
    printf("Failed to prepare TEE session: 0x%x\n", res);
    return 1;
  }

  res = handle_cli_command(&ctx, argc, argv);
  if (res != TEEC_SUCCESS) {
    printf("%s command failed: 0x%x\n", argv[1], res);
  }

  device_identity_terminate_tee_session(&ctx);
  return (res == TEEC_SUCCESS) ? 0 : 1;
}

#include "tcp_server.h"
#include "provision_commands.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include "libminer_optee_host_common/common.h"

static void print_usage(const char* prog_name) {
  printf("Usage: %s <command> [args]\n", prog_name);
  printf("Commands:\n");
  printf("  generate_csr\n");
  printf("  read_pub\n");
  printf("  read_cert\n");
  printf("  is_cert_valid\n");
  printf("  server                        - Start TCP server on port %d\n", PROVISIONING_PORT);
}

static TEEC_Result handle_cli_command(device_identity_ctx_t* ctx, int argc, char* argv[]) {
  if (argc < 2) {
    print_usage(argv[0]);
    return TEEC_ERROR_BAD_PARAMETERS;
  }

  const char* cmd = argv[1];
  uint8_t response_buf[MAX_COMMAND_PAYLOAD_SIZE];

  if (!strcmp(cmd, "generate_csr")) {
    printf("Generating CSR\n");
    return device_identity_generate_csr(ctx, response_buf, sizeof(response_buf), NULL);
  }

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

/* Writes the total message length minus the length field itself */
void write_length_field(uint8_t* buffer, size_t payload_size) {
  uint32_t netlen = htonl(payload_size + CMD_ID_LEN); /* Exclude the length field itself */
  memcpy(buffer, &netlen, sizeof(netlen));
}

size_t get_length_field(const uint8_t* buffer) {
  if (buffer == NULL) {
    printf("Invalid buffer\n");
    return 0;
  }

  uint32_t netlen;
  memcpy(&netlen, buffer, sizeof(netlen));
  return ntohl(netlen);
}

/* Given a properly formatted response buffer (with a payload), generate a command response.
  * Returns the total message length
 */
size_t cmd_generate(uint8_t* response_buffer, cmd_id_t cmd_id, size_t payload_size) {
  if (response_buffer == NULL || payload_size > MAX_COMMAND_PAYLOAD_SIZE) {
    printf("Invalid response buffer or size\n");
    return 0;
  }

  write_length_field(response_buffer, payload_size);
  response_buffer[CMD_ID_OFFSET] = cmd_id;
  return payload_size + HEADER_SIZE;
}

/* Given an error message, generate an internal error response. Assumes the response buffer is memzeroed
 * Returns the total message length or zero on failure
 */
size_t cmd_generate_internal_error(uint8_t* response_buffer, const char* err_msg) {
  if (response_buffer == NULL) {
    printf("Invalid response buffer or error message\n");
    return 0;
  }

  if (err_msg == NULL) {
    err_msg = "Internal error occurred";
  }

  size_t payload_size = strnlen(err_msg, MAX_COMMAND_PAYLOAD_SIZE - 1) + 1;
  memcpy(response_buffer + PAYLOAD_OFFSET, err_msg, payload_size);

  return cmd_generate(response_buffer, CMD_INTERNAL_ERR, payload_size);
}

/**
 * Parse command from network buffer and execute it. Returns the total message length
 */
size_t handle_network_command(const uint8_t* command_buffer,
                              size_t command_size,
                              uint8_t* response_buffer,
                              size_t response_buffer_size) {
  TEEC_Result res;
  device_identity_ctx_t ctx;
  size_t payload_size = 0;
  size_t total_message_len = 0;

  if (command_buffer == NULL || response_buffer == NULL || response_buffer_size < MAX_COMMAND_SIZE) {
    printf("Invalid command or response buffer\n");
    return 0;
  }

  if (command_size < HEADER_SIZE || command_size > MAX_COMMAND_SIZE) {
    printf("Invalid command size: %zu bytes\n", command_size);
    return cmd_generate_internal_error(response_buffer, "Invalid command size");
  }
  if (get_length_field(command_buffer) != command_size - CMD_LEN_FIELD_LEN) {
    return cmd_generate_internal_error(response_buffer, "Invalid command length");
  }

  res = device_identity_prepare_tee_session(&ctx);
  if (res != TEEC_SUCCESS) {
    printf("Failed to prepare TEE session: 0x%x\n", res);
    return cmd_generate_internal_error(response_buffer, "Failed to prepare TEE session");
  }

  switch (GET_CMD_ID(command_buffer)) {
    case CMD_GET_CSR_REQUEST:
      printf("Received CSR request command\n");
      res = device_identity_generate_csr(&ctx,
                                         response_buffer + PAYLOAD_OFFSET,
                                         response_buffer_size - PAYLOAD_OFFSET,
                                         &payload_size);
      if (res != TEEC_SUCCESS) {
        printf("Failed to generate CSR: 0x%x\n", res);
        total_message_len = cmd_generate_internal_error(response_buffer, "Failed to generate CSR");
      } else {
        printf("CSR generated successfully\n");
        total_message_len = cmd_generate(response_buffer, CMD_GET_CSR_RESPONSE, payload_size);
      }
      break;

    case CMD_GET_CSR_RESPONSE:
      printf("Received CSR response command\n");
      total_message_len = cmd_generate_internal_error(response_buffer, "Received unexpected command [GET_CSR_RESPONSE]");
      break;

    case CMD_SEND_CERT_CHAIN: {
      size_t cert_chain_size = command_size - HEADER_SIZE;
      printf("Received send certificate chain command\n");
      res = device_identity_validate_and_store_device_cert(&ctx,
                                                           command_buffer + PAYLOAD_OFFSET,
                                                           cert_chain_size,
                                                           response_buffer + PAYLOAD_OFFSET,
                                                           response_buffer_size - PAYLOAD_OFFSET,
                                                           &payload_size);
      if (res != TEEC_SUCCESS) {
        printf("Failed to validate certificate chain: 0x%x\n", res);
        total_message_len = cmd_generate_internal_error(response_buffer, "Failed to validate certificate chain");
      } else {
        printf("Certificate chain verified and device cert stored successfully\n");
        total_message_len = cmd_generate(response_buffer, CMD_GET_CERT_RESPONSE, payload_size);
      }
      break;
    }

    case CMD_GET_CERT_REQUEST:
      printf("Received get certificate request command\n");
      res = device_identity_read_certificate(&ctx,
                                             response_buffer + PAYLOAD_OFFSET,
                                             response_buffer_size - PAYLOAD_OFFSET,
                                             &payload_size);
      if (res != TEEC_SUCCESS) {
        printf("Failed to get device certificate: 0x%x\n", res);
        total_message_len = cmd_generate_internal_error(response_buffer, "Failed to get device certificate");
      } else {
        printf("Device certificate read successfully\n");
        total_message_len = cmd_generate(response_buffer, CMD_GET_CERT_RESPONSE, payload_size);
      }
      break;

    case CMD_GET_CERT_RESPONSE:
      printf("Received get certificate response command\n");
      total_message_len = cmd_generate_internal_error(response_buffer, "Received unexpected command [GET_CERT_RESPONSE]");
      break;

    case CMD_INTERNAL_ERR:
      printf("Received internal error command\n");
      printf("Internal error: %.*s\n", (int)(command_size - PAYLOAD_OFFSET),
             (const char*)command_buffer + PAYLOAD_OFFSET);
      total_message_len = 0;
      break;

    default:
      printf("Unknown command ID: 0x%x\n", GET_CMD_ID(command_buffer));
      total_message_len = cmd_generate_internal_error(response_buffer, "Unknown or unsupported command ID");
      break;
  }

  device_identity_terminate_tee_session(&ctx);
  return total_message_len;
}

int main(int argc, char* argv[]) {
  device_identity_ctx_t ctx;
  TEEC_Result res;

  if (argc <= 1) {
    print_usage(argv[0]);
    return 1;
  }

  if (strcmp(argv[1], "server") == 0) {
    return start_server();
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

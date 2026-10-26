#include <err.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <tee_client_api.h>

#include "libminer_optee_host_common/rpmb.h"

#include "lock_ctrl_ta.h"

#define TA_LOCK_PASSWORD_MAX_LENGTH 16
#define RPMB_DEVICE_PATH "/dev/mmcblk0rpmb"
#define EXIT_RPMB_NOT_PROGRAMMED 0x7

static TEEC_UUID uuid = TA_LOCK_CTRL_UUID;
static TEEC_Context ctx;
static TEEC_Session sess;

static void print_usage(const char* prog_name) {
  printf("Usage: %s <command> [args]\n", prog_name);
  printf("Commands:\n");
  printf("  status\n");
  printf("  enable_secboot <password>\n");
  printf("  user_unlock <password>\n");
  printf("  nvctr_read\n");
}

TEEC_Result teec_start(void) {
  TEEC_Result res;
  uint32_t err_origin;

  res = TEEC_InitializeContext(NULL, &ctx);
  if (res != TEEC_SUCCESS) {
    printf("TEEC_InitializeContext failed with code 0x%x", res);
    return res;
  }

  res = TEEC_OpenSession(&ctx, &sess, &uuid, TEEC_LOGIN_PUBLIC, NULL, NULL, &err_origin);
  if (res != TEEC_SUCCESS) {
    printf("TEEC_OpenSession failed with code 0x%x origin 0x%x\n", res, err_origin);
    TEEC_FinalizeContext(&ctx);
    return res;
  }

  return res;
}

void teec_stop(void) {
  TEEC_CloseSession(&sess);

  TEEC_FinalizeContext(&ctx);
}

static void print_lock_status(uint32_t state) {
  printf("Device status: ");

  switch (state) {
    case TA_LOCK_CTRL_STATE_OPEN:
      printf("OPEN\n");
      break;
    case TA_LOCK_CTRL_STATE_CLOSED:
      printf("CLOSED\n");
      break;
    case TA_LOCK_CTRL_STATE_UNLOCKED:
      printf("UNLOCKED\n");
      break;
    case TA_LOCK_CTRL_STATE_UNINITIALIZED:
      printf("UNINITIALIZED\n");
      break;
    case TA_LOCK_CTRL_STATE_UNKNOWN:
    default:
      printf("UNKNOWN\n");
      break;
  }
}

TEEC_Result device_lock_status(uint32_t* state) {
  TEEC_Result res;
  TEEC_Operation op;
  uint32_t err_origin;
  rpmb_key_status_t key_status = RPMB_KEY_STATUS_UNKNOWN;

  if (state == NULL) {
    return TEEC_ERROR_BAD_PARAMETERS;
  }
  *state = TA_LOCK_CTRL_STATE_UNKNOWN;

  /* Embedded TAs can load without RPMB; do not depend on a session error. */
  if (rpmb_get_key_status(RPMB_DEVICE_PATH, &key_status) != 0) {
    return TEEC_ERROR_GENERIC;
  }
  if (key_status == RPMB_KEY_STATUS_NOT_PROGRAMMED) {
    *state = TA_LOCK_CTRL_STATE_UNINITIALIZED;
    return TEEC_SUCCESS;
  }
  if (key_status != RPMB_KEY_STATUS_PROGRAMMED) {
    return TEEC_ERROR_BAD_STATE;
  }

  res = teec_start();
  if (res != TEEC_SUCCESS) {
    return res;
  }

  memset(&op, 0, sizeof(op));
  op.paramTypes = TEEC_PARAM_TYPES(TEEC_VALUE_OUTPUT, TEEC_NONE, TEEC_NONE, TEEC_NONE);

  res = TEEC_InvokeCommand(&sess, TA_LOCK_CTRL_CMD_STATUS, &op, &err_origin);
  if (res != TEEC_SUCCESS) {
    printf("TEEC_InvokeCommand failed with code 0x%x origin 0x%x\n", res, err_origin);
  }
  teec_stop();
  if (res != TEEC_SUCCESS) {
    return res;
  }

  *state = op.params[0].value.a;
  return TEEC_SUCCESS;
}

TEEC_Result device_enable_secboot(const char* password) {
  TEEC_Result res;
  TEEC_Operation op;
  uint32_t err_origin;

  if (password == NULL || password[0] == '\0') {
    fprintf(stderr, "Password must be provided for locking the device.\n");
    return TEEC_ERROR_BAD_PARAMETERS;
  }
  int password_len = strnlen(password, TA_LOCK_PASSWORD_MAX_LENGTH);

  res = teec_start();
  if (res != TEEC_SUCCESS) {
    return res;
  }

  memset(&op, 0, sizeof(op));

  op.paramTypes = TEEC_PARAM_TYPES(TEEC_MEMREF_TEMP_INPUT, TEEC_VALUE_OUTPUT, TEEC_NONE, TEEC_NONE);
  op.params[0].tmpref.buffer = (char*)password;
  op.params[0].tmpref.size = password_len;

  res = TEEC_InvokeCommand(&sess, TA_LOCK_CTRL_CMD_ENABLE_SECBOOT, &op, &err_origin);
  if (res != TEEC_SUCCESS) {
    printf("TEEC_InvokeCommand failed with code 0x%x origin 0x%x\n", res, err_origin);
  }
  teec_stop();

  printf("Device lock: ");

  if ((op.params[1].value.a == TEEC_SUCCESS) &&
      (op.params[1].value.b == TA_LOCK_CTRL_CMD_ENABLE_SECBOOT)) {
    printf("Succeeded\n");
  } else {
    printf("Failed\n");
  }
  return res;
}

TEEC_Result device_user_unlock(const char* password) {
  TEEC_Result res;
  TEEC_Operation op;
  uint32_t err_origin;

  if (password == NULL || password[0] == '\0') {
    fprintf(stderr, "Password must be provided for unlocking the device.\n");
    return TEEC_ERROR_BAD_PARAMETERS;
  }
  int password_len = strnlen(password, TA_LOCK_PASSWORD_MAX_LENGTH);

  res = teec_start();
  if (res != TEEC_SUCCESS) {
    return res;
  }

  memset(&op, 0, sizeof(op));

  op.paramTypes = TEEC_PARAM_TYPES(TEEC_MEMREF_TEMP_INPUT, TEEC_VALUE_OUTPUT, TEEC_NONE, TEEC_NONE);
  op.params[0].tmpref.buffer = (char*)password;
  op.params[0].tmpref.size = password_len;

  res = TEEC_InvokeCommand(&sess, TA_LOCK_CTRL_CMD_USER_UNLOCK_SECBOOT, &op, &err_origin);
  if (res != TEEC_SUCCESS) {
    printf("TEEC_InvokeCommand failed with code 0x%x origin 0x%x\n", res, err_origin);
  }

  teec_stop();

  printf("Device unlock: ");

  if ((op.params[1].value.a == TEEC_SUCCESS) &&
      (op.params[1].value.b == TA_LOCK_CTRL_CMD_USER_UNLOCK_SECBOOT)) {
    printf("Succeeded\n");
  } else {
    printf("Failed\n");
  }
  return res;
}

TEEC_Result nvctr_read(void) {
  TEEC_Result res;
  TEEC_Operation op;
  uint32_t err_origin;

  res = teec_start();
  if (res != TEEC_SUCCESS) {
    return res;
  }

  memset(&op, 0, sizeof(op));
  op.paramTypes = TEEC_PARAM_TYPES(TEEC_VALUE_OUTPUT, TEEC_NONE, TEEC_NONE, TEEC_NONE);

  res = TEEC_InvokeCommand(&sess, TA_LOCK_CTRL_CMD_NVCTR_READ, &op, &err_origin);
  if (res != TEEC_SUCCESS) {
    printf("TEEC_InvokeCommand failed with code 0x%x origin 0x%x\n", res, err_origin);
  }
  teec_stop();
  if (res != TEEC_SUCCESS) {
    return res;
  }

  printf("nvctr: raw=0x%08x count=%u\n", op.params[0].value.a, op.params[0].value.b);
  return TEEC_SUCCESS;
}

int main(int argc, char* argv[]) {
  if (argc <= 1) {
    print_usage(argv[0]);
    return EXIT_FAILURE;
  }

  if (strcmp(argv[1], "status") == 0) {
    uint32_t state;
    TEEC_Result res = device_lock_status(&state);
    print_lock_status(state);
    if (res != TEEC_SUCCESS) {
      return EXIT_FAILURE;
    }
    switch (state) {
      case TA_LOCK_CTRL_STATE_UNINITIALIZED:
        return EXIT_RPMB_NOT_PROGRAMMED;
      case TA_LOCK_CTRL_STATE_OPEN:
      case TA_LOCK_CTRL_STATE_CLOSED:
      case TA_LOCK_CTRL_STATE_UNLOCKED:
        return EXIT_SUCCESS;
      default:
        return EXIT_FAILURE;
    }
  }

  if (strcmp(argv[1], "nvctr_read") == 0) {
    return nvctr_read() == TEEC_SUCCESS ? EXIT_SUCCESS : EXIT_FAILURE;
  }

  if (argc >= 3) {
    if (strcmp(argv[1], "enable_secboot") == 0) {
      return device_enable_secboot(argv[2]) == TEEC_SUCCESS ? EXIT_SUCCESS : EXIT_FAILURE;
    }

    if (strcmp(argv[1], "user_unlock") == 0) {
      return device_user_unlock(argv[2]) == TEEC_SUCCESS ? EXIT_SUCCESS : EXIT_FAILURE;
    }
  }

  printf("Invalid command!\n");
  print_usage(argv[0]);

  return EXIT_FAILURE;
}

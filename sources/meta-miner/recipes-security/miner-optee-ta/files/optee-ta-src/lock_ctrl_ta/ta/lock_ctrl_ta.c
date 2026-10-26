#include <lock_ctrl_ta.h>

#include <pta_bsec.h>
#include <util.h>

#include <tee_api_defines.h>
#include <tee_internal_api.h>
#include <tee_internal_api_extensions.h>
#include <trace.h>

#include "libminer_optee_ta_common/common.h"

#include <string.h>

#define LOCK_CTRL_TA_USER_UNLOCKED_OTP_INDEX 65
#define LOCK_CTRL_TA_USER_UNLOCKED_OTP_VALUE 0x5a5a5a5a

#define LOCK_CTRL_TA_OTP_UPPER_INDEX 32

#define LOCK_CTRL_TA_DEVICE_LOCK_OTP_INDEX 0
// Disable JTAG, boundary scan and mark device as secure
#define LOCK_CTRL_TA_DEVICE_LOCK_OTP_VALUE 0x03ff

#define LOCK_CTRL_TA_PKHASH_OTP_INDEX 24
#define LOCK_CTRL_TA_PKHASH_OTP_WORD_LEN 8

#define LOCK_CTRL_TA_SERIAL_NUM_INDEX 59
#define LOCK_CTRL_TA_SERIAL_NUM_WORD_LEN 4

/* Anti-rollback counter lives in one 32-bit OTP word; the counter value is
 * the popcount of that word (monotonic one-way burn). Read-only from the TA
 * side; bumps are performed by TF-A after it has verified the new image. */
#define LOCK_CTRL_TA_NVCTR_OTP_INDEX 11

static size_t session_refcount = 0;
static TEE_TASessionHandle pta_session = TEE_HANDLE_NULL;

static uint32_t serial_sn_buff[LOCK_CTRL_TA_SERIAL_NUM_WORD_LEN];

static TEE_Result otp_write_word(uint32_t otp_id, uint32_t otp_value) {
  TEE_Result res = TEE_SUCCESS;
  TEE_Param params[TEE_NUM_PARAMS];
  const uint32_t exp_pt =
      TEE_PARAM_TYPES(TEE_PARAM_TYPE_VALUE_INPUT, TEE_PARAM_TYPE_MEMREF_INPUT,
                      TEE_PARAM_TYPE_NONE, TEE_PARAM_TYPE_NONE);

  if (pta_session == TEE_HANDLE_NULL) {
    return TEE_ERROR_ACCESS_DENIED;
  }

  params[0].value.a = otp_id * sizeof(uint32_t);
  params[0].value.b = FUSE_ACCESS;
  params[1].memref.buffer = &otp_value;
  params[1].memref.size = sizeof(uint32_t);

  res = TEE_InvokeTACommand(pta_session, TEE_TIMEOUT_INFINITE,
                            PTA_BSEC_WRITE_MEM, exp_pt, params, NULL);
  if (res != TEE_SUCCESS) {
    EMSG("lock_ctrl_ta: %s failed with code 0x%x", __func__, res);
    return res;
  }

  if (otp_id >= LOCK_CTRL_TA_OTP_UPPER_INDEX) {
    uint32_t lock = LOCK_PERM;

    DMSG("Perm lock detected %i", otp_id);
    params[0].value.a = otp_id * sizeof(uint32_t);
    params[0].value.b = LOCK_ACCESS;
    params[1].memref.buffer = &lock;
    params[1].memref.size = sizeof(uint32_t);
    res = TEE_InvokeTACommand(pta_session, TEE_TIMEOUT_INFINITE,
                              PTA_BSEC_WRITE_MEM, exp_pt, params, NULL);
    if (res != TEE_SUCCESS) {
      EMSG("lock_ctrl_ta: %s failed with code 0x%x", __func__, res);
    }
  }
  return res;
}

static TEE_Result otp_read_word(uint32_t otp_id, uint32_t *otp_value) {
  TEE_Result res = TEE_SUCCESS;
  TEE_Param params[TEE_NUM_PARAMS];
  const uint32_t exp_pt =
      TEE_PARAM_TYPES(TEE_PARAM_TYPE_VALUE_INPUT, TEE_PARAM_TYPE_MEMREF_OUTPUT,
                      TEE_PARAM_TYPE_NONE, TEE_PARAM_TYPE_NONE);

  if (otp_value == NULL) {
    return TEE_ERROR_BAD_PARAMETERS;
  }

  if (pta_session == TEE_HANDLE_NULL) {
    return TEE_ERROR_ACCESS_DENIED;
  }

  params[0].value.a = otp_id * sizeof(uint32_t);
  params[0].value.b = FUSE_ACCESS;
  params[1].memref.buffer = otp_value;
  params[1].memref.size = sizeof(uint32_t);

  res = TEE_InvokeTACommand(pta_session, TEE_TIMEOUT_INFINITE,
                            PTA_BSEC_READ_MEM, exp_pt, params, NULL);
  if (res != TEE_SUCCESS) {
    EMSG("lock_ctrl_ta: otp_read_word failed with code 0x%x", res);
  }
  return res;
}

static TEE_Result otp_read_serial(void) {
  for (int i = 0; i < LOCK_CTRL_TA_SERIAL_NUM_WORD_LEN; i++) {
    TEE_Result res =
        otp_read_word(LOCK_CTRL_TA_SERIAL_NUM_INDEX + i, &serial_sn_buff[i]);
    if (res != TEE_SUCCESS) {
      EMSG("lock_ctrl_ta: otp_read_serial failed with code 0x%x", res);
      return res;
    }
  }

  return TEE_SUCCESS;
}

static TEE_Result otp_get_state(uint32_t *device_state) {
  TEE_Result res = TEE_SUCCESS;
  TEE_Param params[TEE_NUM_PARAMS];
  const uint32_t exp_pt =
      TEE_PARAM_TYPES(TEE_PARAM_TYPE_VALUE_OUTPUT, TEE_PARAM_TYPE_NONE,
                      TEE_PARAM_TYPE_NONE, TEE_PARAM_TYPE_NONE);

  if (!device_state) {
    return TEE_ERROR_BAD_PARAMETERS;
  }

  if (pta_session == TEE_HANDLE_NULL) {
    return TEE_ERROR_ACCESS_DENIED;
  }

  res = TEE_InvokeTACommand(pta_session, TEE_TIMEOUT_INFINITE,
                            PTA_BSEC_GET_STATE, exp_pt, params, NULL);
  if (res != TEE_SUCCESS) {
    EMSG("lock_ctrl_ta: otp_get_state failed with code 0x%x", res);
  } else {
    *device_state = params[0].value.a;
  }

  return res;
}

static TEE_Result otp_check_pkhash(void) {
  TEE_Result res = TEE_SUCCESS;

  // Ensure all register of pkhash is populated
  for (int i = 0; i < LOCK_CTRL_TA_PKHASH_OTP_WORD_LEN; i++) {
    uint32_t otp_value = 0;
    res = otp_read_word(LOCK_CTRL_TA_PKHASH_OTP_INDEX + i, &otp_value);
    if (res != TEE_SUCCESS) {
      EMSG("lock_ctrl_ta: otp_check_pkhash failed with code 0x%x", res);
      return res;
    }
    if (otp_value == 0) {
      EMSG("lock_ctrl_ta: pkhash OTP %d is not set", i);
      return TEE_ERROR_BAD_STATE;
    }
  }

  return res;
}

static TEE_Result otp_lock_pkhash(void) {
  TEE_Result res = TEE_SUCCESS;
  TEE_Param params[TEE_NUM_PARAMS];
  const uint32_t exp_pt =
      TEE_PARAM_TYPES(TEE_PARAM_TYPE_VALUE_INPUT, TEE_PARAM_TYPE_MEMREF_INPUT,
                      TEE_PARAM_TYPE_NONE, TEE_PARAM_TYPE_NONE);

  // Ensure all register of pkhash is populated
  for (int i = 0; i < LOCK_CTRL_TA_PKHASH_OTP_WORD_LEN; i++) {
    uint32_t lock = LOCK_PERM;

    params[0].value.a = (LOCK_CTRL_TA_PKHASH_OTP_INDEX + i) * sizeof(uint32_t);
    params[0].value.b = LOCK_ACCESS;
    params[1].memref.buffer = &lock;
    params[1].memref.size = sizeof(uint32_t);
    res = TEE_InvokeTACommand(pta_session, TEE_TIMEOUT_INFINITE,
                              PTA_BSEC_WRITE_MEM, exp_pt, params, NULL);
    if (res != TEE_SUCCESS) {
      EMSG("lock_ctrl_ta: %s failed with code 0x%x", __func__, res);
      break;
    }
  }
  return res;
}

TEE_Result TA_CreateEntryPoint(void) {
#ifdef MINER_TA_RUNTIME_LOG_LEVEL
  trace_set_level(MINER_TA_RUNTIME_LOG_LEVEL);
#endif
  DMSG("lock_ctrl_ta Create");

  return TEE_SUCCESS;
}

void TA_DestroyEntryPoint(void) { DMSG("lock_ctrl_ta Destroy"); }

TEE_Result TA_OpenSessionEntryPoint(uint32_t __maybe_unused param_types,
                                    TEE_Param __maybe_unused params[4],
                                    void __maybe_unused **sess_ctx) {
  static const TEE_UUID uuid = PTA_BSEC_UUID;
  TEE_Result res = TEE_ERROR_GENERIC;
  TEE_PropSetHandle h = TEE_HANDLE_NULL;
  TEE_Identity id = {};

  res = TEE_AllocatePropertyEnumerator(&h);
  if (res != TEE_SUCCESS) {
    goto out;
  }

  TEE_StartPropertyEnumerator(h, TEE_PROPSET_CURRENT_CLIENT);

  res = TEE_GetPropertyAsIdentity(h, NULL, &id);
  if (res != TEE_SUCCESS)
    goto out;

  if (!session_refcount) {
    res = TEE_OpenTASession(&uuid, TEE_TIMEOUT_INFINITE, 0, NULL, &pta_session,
                            NULL);
    if ((res != TEE_SUCCESS) || (pta_session == TEE_HANDLE_NULL)) {
      EMSG("lock_ctrl_ta: 0x%08x, %p", res, pta_session);
      goto out;
    }
  }

  session_refcount++;

out:
  if (h)
    TEE_FreePropertyEnumerator(h);

  return res;
}

void TA_CloseSessionEntryPoint(void __maybe_unused *sess_ctx) {
  DMSG("lock_ctrl_ta CloseSession");

  session_refcount--;
  if (session_refcount == 0) {
    TEE_CloseTASession(pta_session);
  }
}

static TEE_Result lock_ctrl_status(uint32_t param_types, TEE_Param params[4]) {
  uint32_t exp_param_types =
      TEE_PARAM_TYPES(TEE_PARAM_TYPE_VALUE_OUTPUT, TEE_PARAM_TYPE_NONE,
                      TEE_PARAM_TYPE_NONE, TEE_PARAM_TYPE_NONE);
  int unlocked_state = TA_LOCK_CTRL_STATE_UNKNOWN;

  if (param_types != exp_param_types)
    return TEE_ERROR_BAD_PARAMETERS;

  uint32_t device_state;
  TEE_Result res = otp_get_state(&device_state);
  if (res != TEE_SUCCESS) {
    return TEE_ERROR_GENERIC;
  }

  uint32_t user_unlocked;
  res = otp_read_word(LOCK_CTRL_TA_USER_UNLOCKED_OTP_INDEX, &user_unlocked);
  if (res != TEE_SUCCESS) {
    return TEE_ERROR_GENERIC;
  }

  if ((device_state == 0) && (user_unlocked == 0)) {
    unlocked_state = TA_LOCK_CTRL_STATE_OPEN;
  } else if ((device_state != 0) && (user_unlocked == 0)) {
    unlocked_state = TA_LOCK_CTRL_STATE_CLOSED;
  } else if (user_unlocked != 0) {
    unlocked_state = TA_LOCK_CTRL_STATE_UNLOCKED;
  }

  params[0].value.a = unlocked_state;

  return TEE_SUCCESS;
}

static TEE_Result lock_ctrl_enable_secboot(uint32_t param_types,
                                           TEE_Param params[4]) {
  uint32_t exp_param_types =
      TEE_PARAM_TYPES(TEE_PARAM_TYPE_MEMREF_INPUT, TEE_PARAM_TYPE_VALUE_OUTPUT,
                      TEE_PARAM_TYPE_NONE, TEE_PARAM_TYPE_NONE);

  if (param_types != exp_param_types)
    return TEE_ERROR_BAD_PARAMETERS;

  params[1].value.a = TEE_ERROR_GENERIC; // Assume Failure
  params[1].value.b = TA_LOCK_CTRL_CMD_ENABLE_SECBOOT;

  if (params[0].memref.buffer == NULL) {
    return TEE_ERROR_BAD_PARAMETERS;
  }
  if (params[0].memref.size < sizeof(serial_sn_buff)) {
    return TEE_ERROR_SHORT_BUFFER;
  }

  TEE_Result res = otp_read_serial();
  if (res != TEE_SUCCESS) {
    EMSG("lock_ctrl_ta: otp_read_serial failed with code 0x%x", res);
    return res;
  }

  if (TEE_MemCompare(params[0].memref.buffer, serial_sn_buff,
                     sizeof(serial_sn_buff)) != 0) {
    EMSG("lock_ctrl_ta: Serial number mismatch");
    return TEE_ERROR_BAD_PARAMETERS;
  }

  res = otp_check_pkhash();
  if (res != TEE_SUCCESS) {
    EMSG("lock_ctrl_ta: pkhash check failed");
    return TEE_ERROR_BAD_STATE;
  }

  res = otp_lock_pkhash();
  if (res != TEE_SUCCESS) {
    EMSG("lock_ctrl_ta: pkhash lock failed");
    return TEE_ERROR_BAD_STATE;
  }

  IMSG("lock_ctrl_ta: Locking device");

  res = otp_write_word(LOCK_CTRL_TA_DEVICE_LOCK_OTP_INDEX,
                       LOCK_CTRL_TA_DEVICE_LOCK_OTP_VALUE);
  if (res != TEE_SUCCESS) {
    EMSG("lock_ctrl_ta: otp_write_word failed with code 0x%x", res);
  } else {
    // Lock is successful
    params[1].value.a = TEE_SUCCESS;
  }

  return res;
}

static TEE_Result lock_ctrl_nvctr_read(uint32_t param_types,
                                       TEE_Param params[4]) {
  const uint32_t exp_param_types =
      TEE_PARAM_TYPES(TEE_PARAM_TYPE_VALUE_OUTPUT, TEE_PARAM_TYPE_NONE,
                      TEE_PARAM_TYPE_NONE, TEE_PARAM_TYPE_NONE);
  if (param_types != exp_param_types)
    return TEE_ERROR_BAD_PARAMETERS;

  uint32_t raw = 0;
  TEE_Result res = otp_read_word(LOCK_CTRL_TA_NVCTR_OTP_INDEX, &raw);
  if (res != TEE_SUCCESS) {
    return res;
  }

  params[0].value.a = raw;
  params[0].value.b = __builtin_popcount(raw);
  return TEE_SUCCESS;
}

static TEE_Result lock_ctrl_user_unlock_secboot(uint32_t param_types,
                                                TEE_Param params[4]) {
  uint32_t exp_param_types =
      TEE_PARAM_TYPES(TEE_PARAM_TYPE_MEMREF_INPUT, TEE_PARAM_TYPE_VALUE_OUTPUT,
                      TEE_PARAM_TYPE_NONE, TEE_PARAM_TYPE_NONE);
  if (param_types != exp_param_types)
    return TEE_ERROR_BAD_PARAMETERS;

  params[1].value.a = TEE_ERROR_GENERIC; // Assume Failure
  params[1].value.b = TA_LOCK_CTRL_CMD_USER_UNLOCK_SECBOOT;

  if (params[0].memref.buffer == NULL) {
    return TEE_ERROR_BAD_PARAMETERS;
  }
  if (params[0].memref.size < sizeof(serial_sn_buff)) {
    return TEE_ERROR_SHORT_BUFFER;
  }

  TEE_Result res = otp_read_serial();
  if (res != TEE_SUCCESS) {
    EMSG("lock_ctrl_ta: otp_read_serial failed with code 0x%x", res);
    return res;
  }

  if (TEE_MemCompare(params[0].memref.buffer, serial_sn_buff,
                     sizeof(serial_sn_buff)) != 0) {
    EMSG("lock_ctrl_ta: Serial number mismatch");
    return TEE_ERROR_BAD_PARAMETERS;
  }

  IMSG("lock_ctrl_ta: Unlocking device");

  res = otp_write_word(LOCK_CTRL_TA_USER_UNLOCKED_OTP_INDEX,
                       LOCK_CTRL_TA_USER_UNLOCKED_OTP_VALUE);
  if (res != TEE_SUCCESS) {
    EMSG("lock_ctrl_ta: otp_write_word failed with code 0x%x", res);
  } else {
    // Unlock is successful
    params[1].value.a = TEE_SUCCESS;
  }

  return res;
}

TEE_Result TA_InvokeCommandEntryPoint(void __maybe_unused *sess_ctx,
                                      uint32_t cmd_id, uint32_t param_types,
                                      TEE_Param params[4]) {
  (void)&sess_ctx; /* Unused parameter */

  switch (cmd_id) {
  case TA_LOCK_CTRL_CMD_STATUS:
    return lock_ctrl_status(param_types, params);
  case TA_LOCK_CTRL_CMD_ENABLE_SECBOOT:
    return lock_ctrl_enable_secboot(param_types, params);
  case TA_LOCK_CTRL_CMD_USER_UNLOCK_SECBOOT:
    return lock_ctrl_user_unlock_secboot(param_types, params);
  case TA_LOCK_CTRL_CMD_NVCTR_READ:
    return lock_ctrl_nvctr_read(param_types, params);
  default:
    return TEE_ERROR_BAD_PARAMETERS;
  }
}

#include <libminer_optee_ta_common/device_identity_ta_core.h>

static const char ATTESTATION_TA_NAME[] = "attestation_ta";

TEE_Result TA_CreateEntryPoint(void) {
  return device_identity_ta_create_entrypoint(ATTESTATION_TA_NAME);
}

void TA_DestroyEntryPoint(void) {
  device_identity_ta_destroy_entrypoint(ATTESTATION_TA_NAME);
}

TEE_Result TA_OpenSessionEntryPoint(uint32_t param_types,
                                    TEE_Param params[4],
                                    void** sess_ctx) {
  return device_identity_ta_open_session_entrypoint(ATTESTATION_TA_NAME,
                                                    param_types,
                                                    params,
                                                    sess_ctx);
}

void TA_CloseSessionEntryPoint(void* sess_ctx) {
  device_identity_ta_close_session_entrypoint(ATTESTATION_TA_NAME, sess_ctx);
}

TEE_Result TA_InvokeCommandEntryPoint(void __maybe_unused* sess_ctx,
                                      uint32_t cmd_id,
                                      uint32_t param_types,
                                      TEE_Param params[4]) {
  return device_identity_ta_invoke_command_entrypoint(cmd_id,
                                                      param_types,
                                                      params,
                                                      false);
}

#ifndef LIB_MINER_OPTEE_DEVICE_IDENTITY_TA_CORE_H
#define LIB_MINER_OPTEE_DEVICE_IDENTITY_TA_CORE_H

#include <stdbool.h>

#include <tee_internal_api.h>

TEE_Result device_identity_ta_create_entrypoint(const char* ta_name);
void device_identity_ta_destroy_entrypoint(const char* ta_name);

TEE_Result device_identity_ta_open_session_entrypoint(const char* ta_name,
                                                      uint32_t param_types,
                                                      TEE_Param params[4],
                                                      void** sess_ctx);

void device_identity_ta_close_session_entrypoint(const char* ta_name,
                                                 void* sess_ctx);

TEE_Result device_identity_ta_invoke_command_entrypoint(uint32_t cmd_id,
                                                        uint32_t param_types,
                                                        TEE_Param params[4],
                                                        bool allow_cert_write);

#endif /* LIB_MINER_OPTEE_DEVICE_IDENTITY_TA_CORE_H */

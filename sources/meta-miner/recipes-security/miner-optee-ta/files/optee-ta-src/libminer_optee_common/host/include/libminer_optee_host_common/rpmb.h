#ifndef LIB_MINER_OPTEE_HOST_COMMON_RPMB_H
#define LIB_MINER_OPTEE_HOST_COMMON_RPMB_H

typedef enum {
  RPMB_KEY_STATUS_UNKNOWN = 0,
  RPMB_KEY_STATUS_PROGRAMMED,
  RPMB_KEY_STATUS_NOT_PROGRAMMED,
} rpmb_key_status_t;

/*
 * Query the RPMB write counter without modifying RPMB contents.
 *
 * Returns 0 only when a well-formed response confirms whether the key is
 * programmed. On failure, status is left as RPMB_KEY_STATUS_UNKNOWN.
 */
int rpmb_get_key_status(const char* device_path, rpmb_key_status_t* status);

#endif /* LIB_MINER_OPTEE_HOST_COMMON_RPMB_H */

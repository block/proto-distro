#include <libminer_optee_ta_common/device_identity.h>
#include <libminer_optee_ta_common/device_identity_ta_core.h>

#include <pta_bsec.h>

#include <tee_internal_api.h>
#include <tee_internal_api_extensions.h>
#include <trace.h>

#include "mbedtls/ctr_drbg.h"
#include "mbedtls/ecp.h"
#include "mbedtls/entropy.h"
#include "mbedtls/pk.h"
#include "mbedtls/x509_csr.h"
#include "mbedtls/x509_crt.h"
#include "mbedtls/error.h"
#include "mbedtls/pem.h"

#include <string.h>

#define PROVISION_TA_SUBJECT_NAME_LENGTH (30) // CN= Prefix: 3 + Serial Prefix: 10 + Serial: 16 + Null terminator: 1
#define COMMON_NAME_PREFIX_LENGTH (3) // "CN=" prefix length
static const char SUBJECT_NAME_PREFIX[] = "CN=HW-Serial-"; // CN Prefix + Serial Prefix

/* sources/meta-miner/recipes-dev/miner-signing-keys/files/prod/pkhash/publicKeysHashHashes.bin
 * 4635c1c755573ab09f78c44b0efc7e0f4d46672d7a8af60aa4b8ce3d3e30b956 to le32
 */
static const uint8_t PKHTH_PROD[PROVISION_TA_PKHASH_OTP_NUM_WORDS * sizeof(uint32_t)] = {
  0xc7, 0xc1, 0x35, 0x46, 0xb0, 0x3a, 0x57, 0x55, 0x4b, 0xc4, 0x78, 0x9f, 0x0f, 0x7e, 0xfc, 0x0e,
  0x2d, 0x67, 0x46, 0x4d, 0x0a, 0xf6, 0x8a, 0x7a, 0x3d, 0xce, 0xb8, 0xa4, 0x56, 0xb9, 0x30, 0x3e
};

/* sources/meta-miner/recipes-dev/miner-signing-keys/files/dev/pkhash/publicKeysHashHashes.bin
 * 7941b4596de2251e99cb7280c245793d1ef7675a1b203b1c339a5cce7ed0b965 to le32
 */
static const uint8_t PKHTH_DEV[PROVISION_TA_PKHASH_OTP_NUM_WORDS * sizeof(uint32_t)] = {
  0x59, 0xb4, 0x41, 0x79, 0x1e, 0x25, 0xe2, 0x6d, 0x80, 0x72, 0xcb, 0x99, 0x3d, 0x79, 0x45, 0xc2,
  0x5a, 0x67, 0xf7, 0x1e, 0x1c, 0x3b, 0x20, 0x1b, 0xce, 0x5c, 0x9a, 0x33, 0x65, 0xb9, 0xd0, 0x7e
};

/* For the C3 product
 * proto-attestation-eol-signer/resources/c3/prod/proto-attestation-root-ca.1.crt.pem
 * 14480fdddd9b904a6c3a0e520e9686e5d1de00ee815b6ccc9fde17b8df0db0b0440081cee7df9c13412b3fd0a34961f4fae98092f74255dc921db1fa0386040a 
 */
static const uint8_t ROOT_CA_PK_PROD[ECC_PK_SIZE_BYTES] = {
  0x14, 0x48, 0x0f, 0xdd, 0xdd, 0x9b, 0x90, 0x4a, 0x6c, 0x3a, 0x0e, 0x52, 0x0e, 0x96, 0x86, 0xe5,
  0xd1, 0xde, 0x00, 0xee, 0x81, 0x5b, 0x6c, 0xcc, 0x9f, 0xde, 0x17, 0xb8, 0xdf, 0x0d, 0xb0, 0xb0,
  0x44, 0x00, 0x81, 0xce, 0xe7, 0xdf, 0x9c, 0x13, 0x41, 0x2b, 0x3f, 0xd0, 0xa3, 0x49, 0x61, 0xf4,
  0xfa, 0xe9, 0x80, 0x92, 0xf7, 0x42, 0x55, 0xdc, 0x92, 0x1d, 0xb1, 0xfa, 0x03, 0x86, 0x04, 0x0a
};

/* For the C3 product
 * proto-attestation-eol-signer/resources/c3/dev/proto-attestation-root-ca.1.crt.pem
 * 69f6a662e280d669ab14ca9e74c2f71d8f5a8618bf162b91559549d055c4d7fb2f9a3d1b05107b8a96e9c7727ef9a1832dc8013cb197649b21ace59b9ec3f085
 */
static const uint8_t ROOT_CA_PK_DEV[ECC_PK_SIZE_BYTES] = {
  0x69, 0xf6, 0xa6, 0x62, 0xe2, 0x80, 0xd6, 0x69, 0xab, 0x14, 0xca, 0x9e, 0x74, 0xc2, 0xf7, 0x1d,
  0x8f, 0x5a, 0x86, 0x18, 0xbf, 0x16, 0x2b, 0x91, 0x55, 0x95, 0x49, 0xd0, 0x55, 0xc4, 0xd7, 0xfb,
  0x2f, 0x9a, 0x3d, 0x1b, 0x05, 0x10, 0x7b, 0x8a, 0x96, 0xe9, 0xc7, 0x72, 0x7e, 0xf9, 0xa1, 0x83,
  0x2d, 0xc8, 0x01, 0x3c, 0xb1, 0x97, 0x64, 0x9b, 0x21, 0xac, 0xe5, 0x9b, 0x9e, 0xc3, 0xf0, 0x85
};

static size_t session_refcount = 0;
static TEE_TASessionHandle pta_session = TEE_HANDLE_NULL;
static bool rpmb_available = false;


static const char* get_ta_name(const char* ta_name) {
  if (ta_name == NULL || ta_name[0] == '\0') {
    return "device_identity_ta";
  }

  return ta_name;
}


TEE_Result device_identity_ta_create_entrypoint(const char* ta_name) {
#ifdef MINER_TA_RUNTIME_LOG_LEVEL
  trace_set_level(MINER_TA_RUNTIME_LOG_LEVEL);
#endif
  DMSG("%s Create", get_ta_name(ta_name));
  return TEE_SUCCESS;
}

void device_identity_ta_destroy_entrypoint(const char* ta_name) {
  DMSG("%s Destroy", get_ta_name(ta_name));
}

TEE_Result device_identity_ta_open_session_entrypoint(const char* ta_name,
                                                      uint32_t __maybe_unused param_types,
                                                      TEE_Param __maybe_unused params[4],
                                                      void __maybe_unused** sess_ctx) {
  static const TEE_UUID uuid = PTA_BSEC_UUID;
  TEE_Result res = TEE_ERROR_GENERIC;
  TEE_PropSetHandle h = TEE_HANDLE_NULL;
  TEE_Identity id = {};

  res = TEE_AllocatePropertyEnumerator(&h);
  if (res != TEE_SUCCESS)
    goto out;

  TEE_StartPropertyEnumerator(h, TEE_PROPSET_CURRENT_CLIENT);

  res = TEE_GetPropertyAsIdentity(h, NULL, &id);
  if (res != TEE_SUCCESS)
    goto out;

  if (!session_refcount) {
    res = TEE_OpenTASession(&uuid, TEE_TIMEOUT_INFINITE, 0, NULL, &pta_session, NULL);
    if ((res != TEE_SUCCESS) || (pta_session == TEE_HANDLE_NULL)) {
      EMSG("%s: 0x%08x, %p", get_ta_name(ta_name), res, pta_session);
      goto out;
    }

    /* Probe RPMB availability so commands can fail gracefully instead
     * of triggering a TEE_Panic from TEE_OpenPersistentObject when
     * the RPMB key is not provisioned. */
    TEE_ObjectEnumHandle enum_hdl = TEE_HANDLE_NULL;
    res = TEE_AllocatePersistentObjectEnumerator(&enum_hdl);
    if (res == TEE_SUCCESS) {
      TEE_Result probe_res = TEE_StartPersistentObjectEnumerator(enum_hdl, TEE_STORAGE_PRIVATE_RPMB);
      TEE_FreePersistentObjectEnumerator(enum_hdl);
      if (probe_res == TEE_SUCCESS || probe_res == TEE_ERROR_ITEM_NOT_FOUND) {
        rpmb_available = true;
      } else {
        EMSG("RPMB storage not available: 0x%x", probe_res);
        rpmb_available = false;
      }
    } else {
      EMSG("Failed to allocate enumerator: 0x%x", res);
      rpmb_available = false;
    }
    res = TEE_SUCCESS;
  }

  session_refcount++;

out:
  if (h)
    TEE_FreePropertyEnumerator(h);

  return res;
}

void device_identity_ta_close_session_entrypoint(const char* ta_name,
                                                 void __maybe_unused* sess_ctx) {
  DMSG("%s CloseSession", get_ta_name(ta_name));

  session_refcount--;
  if (session_refcount == 0) {
    TEE_CloseTASession(pta_session);
  }
}

/* Reads from OTP at word offset otp_id and word size value_len */
static TEE_Result otp_read_mem(uint32_t otp_id, uint32_t* otp_value, size_t value_len) {
  TEE_Result res = TEE_SUCCESS;
  TEE_Param params[TEE_NUM_PARAMS];
  const uint32_t exp_pt = TEE_PARAM_TYPES(TEE_PARAM_TYPE_VALUE_INPUT, TEE_PARAM_TYPE_MEMREF_OUTPUT,
                                          TEE_PARAM_TYPE_NONE, TEE_PARAM_TYPE_NONE);

  if (pta_session == TEE_HANDLE_NULL) {
    return TEE_ERROR_ACCESS_DENIED;
  }

  if (otp_value == NULL) {
    return TEE_ERROR_BAD_PARAMETERS;
  }

  params[0].value.a = otp_id * sizeof(uint32_t);
  params[0].value.b = FUSE_ACCESS;
  params[1].memref.buffer = otp_value;
  params[1].memref.size = value_len * sizeof(uint32_t);

  res =
    TEE_InvokeTACommand(pta_session, TEE_TIMEOUT_INFINITE, PTA_BSEC_READ_MEM, exp_pt, params, NULL);
  if (res != TEE_SUCCESS) {
    EMSG("otp_read_mem failed with code 0x%x", res);
  }
  return res;
}

/* Determines device fusing. A return of TEE_SUCCESS means that pkhth value was matched to either prod or dev. */
__attribute__((warn_unused_result))
static TEE_Result is_device_production_fused(bool* is_production_fused) {
  TEE_Result res;
  uint32_t pkhth_value[PROVISION_TA_PKHASH_OTP_NUM_WORDS] = {0};

  if (is_production_fused == NULL) {
    EMSG("Invalid parameter: is_production_fused is NULL");
    return TEE_ERROR_BAD_PARAMETERS;
  }

  *is_production_fused = false;

  res = otp_read_mem(PROVISION_TA_PKHASH_OTP_INDEX, pkhth_value, PROVISION_TA_PKHASH_OTP_NUM_WORDS);
  if (res != TEE_SUCCESS) {
    EMSG("Failed to read OTP memory: 0x%x", res);
    return res;
  }

  // Production check
  if ((sizeof(pkhth_value) == sizeof(PKHTH_PROD)) && TEE_MemCompare(pkhth_value, PKHTH_PROD, sizeof(pkhth_value)) == 0) {
    *is_production_fused = true;
    DMSG("Device is production fused");
    return TEE_SUCCESS;
  }
  // Dev check
  if ((sizeof(pkhth_value) == sizeof(PKHTH_DEV)) && TEE_MemCompare(pkhth_value, PKHTH_DEV, sizeof(pkhth_value)) == 0) {
    DMSG("Device is development fused");
    return TEE_SUCCESS;
  }
  // Unfused check
  if (TEE_MemCompare(pkhth_value, (uint32_t[]){0, 0, 0, 0, 0, 0, 0, 0}, sizeof(pkhth_value)) == 0) {
    DMSG("Device is unfused. PKHTH must be set before a device identity can be issued");
    return TEE_ERROR_SECURITY;
  }

  // Catch-all
  EMSG("Device fusing not recognized: OTP value does not match known production or development hashes");
  return TEE_ERROR_BAD_STATE;
}

static TEE_Result store_obj_in_rpmb(const void *object_id, size_t object_id_len, const uint8_t *buf, size_t buf_len) {
  TEE_ObjectHandle handle = TEE_HANDLE_NULL;
  TEE_Result res = TEE_ERROR_GENERIC;

  if (!rpmb_available) {
    EMSG("RPMB storage not available, cannot store object");
    return TEE_ERROR_STORAGE_NOT_AVAILABLE;
  }

  if (object_id == NULL || object_id_len == 0 || buf == NULL || buf_len == 0) {
    EMSG("Invalid parameters for storing key in RPMB");
    return TEE_ERROR_BAD_PARAMETERS;
  }

  uint32_t obj_data_flag = TEE_DATA_FLAG_ACCESS_READ |      /* we can later read the object */
                  TEE_DATA_FLAG_ACCESS_WRITE |     /* we can later write into the object */
                  TEE_DATA_FLAG_OVERWRITE |        /* destroy existing object of same ID */
                  TEE_DATA_FLAG_ACCESS_WRITE_META; /* we can later destroy or rename the object */

  /* Create or overwrite the persistent object in RPMB */
  res = TEE_CreatePersistentObject(TEE_STORAGE_PRIVATE_RPMB, object_id, object_id_len,
                        obj_data_flag, NULL, buf, buf_len, &handle);
  if (res != TEE_SUCCESS) {
    EMSG("Failed to create persistent object: 0x%x", res);
  }
  else {
    DMSG("Obj written successfully (%zu bytes)", buf_len);
    TEE_CloseObject(handle);
  }

  return res;
}

static TEE_Result load_obj_from_rpmb(const void *object_id, size_t object_id_len, uint8_t *buf, size_t *buf_len)
{
  TEE_ObjectHandle handle = TEE_HANDLE_NULL;
  TEE_Result res = TEE_ERROR_GENERIC;
  TEE_ObjectInfo obj_info;
  uint32_t read_bytes = 0;

  if (!rpmb_available) {
    EMSG("RPMB storage not available, cannot load object");
    return TEE_ERROR_STORAGE_NOT_AVAILABLE;
  }

  if (object_id == NULL || buf == NULL || buf_len == NULL || *buf_len == 0) {
    EMSG("Invalid parameters for loading key from RPMB");
    return TEE_ERROR_BAD_PARAMETERS;
  }

  res = TEE_OpenPersistentObject(TEE_STORAGE_PRIVATE_RPMB, object_id, object_id_len, TEE_DATA_FLAG_ACCESS_READ, &handle);
  if (res != TEE_SUCCESS) {
    EMSG("Failed to open object: 0x%x", res);
    return res;
  }

  res = TEE_GetObjectInfo1(handle, &obj_info);
  if (res != TEE_SUCCESS) {
    EMSG("Failed to get object info: 0x%x", res);
    goto cleanup;
  }

  if (obj_info.dataSize > *buf_len) {
    EMSG("Buffer too small. Need %u bytes", obj_info.dataSize);
    res = TEE_ERROR_SHORT_BUFFER;
    goto cleanup;
  }

  res = TEE_ReadObjectData(handle, buf, obj_info.dataSize, &read_bytes);
  if (res != TEE_SUCCESS) {
    EMSG("Failed to read object data: 0x%x", res);
    goto cleanup;
  }

  *buf_len = read_bytes;
  DMSG("Successfully read %u bytes from RPMB", read_bytes);

cleanup:
  TEE_CloseObject(handle);
  return res;
}

static int hw_entropy_source(void *data __unused, unsigned char *output, size_t len, size_t *olen) {
  /* Source initial entropy for mbedtls */
  if (output == NULL || olen == NULL) {
    return MBEDTLS_ERR_ENTROPY_SOURCE_FAILED;
  }

  TEE_GenerateRandom(output, len);

  *olen = len;
  return 0;
}

/**
 * Generate a new identity keypair and store it in RPMB.
 * This function uses mbedTLS to generate a P-256 ECDSA keypair,
 * exports it in DER format, and stores it in the RPMB partition.
 */
static TEE_Result generate_identity_keypair(void) {
  TEE_Result res = TEE_ERROR_GENERIC;
  uint8_t* sk_der = TEE_Malloc(DER_MAX_BUF_SIZE, TEE_MALLOC_FILL_ZERO);
  size_t sk_der_len = DER_MAX_BUF_SIZE;
  int mbedtls_ret = -1;
  mbedtls_pk_context* sk_ctx = TEE_Malloc(sizeof(mbedtls_pk_context), TEE_MALLOC_FILL_ZERO);
  mbedtls_ctr_drbg_context ctr_drbg;
  mbedtls_entropy_context entropy;

  mbedtls_ctr_drbg_init(&ctr_drbg);
  mbedtls_entropy_init(&entropy);
  if (sk_ctx != NULL) {
    mbedtls_pk_init(sk_ctx);
  }

  if (sk_der == NULL || sk_ctx == NULL) {
    EMSG("Failed to allocate memory for key or context");
    res = TEE_ERROR_OUT_OF_MEMORY;
    goto cleanup;
  }

  /* Source entropy from TEE */
  mbedtls_ret = mbedtls_entropy_add_source(&entropy, hw_entropy_source, NULL, 32, MBEDTLS_ENTROPY_SOURCE_STRONG);
  if (mbedtls_ret != 0) {
    EMSG("Error adding entropy from TEE: -0x%04X", -mbedtls_ret);
    res = TEE_ERROR_GENERIC;
    goto cleanup;
  }

  mbedtls_ret = mbedtls_ctr_drbg_seed(&ctr_drbg, mbedtls_entropy_func, &entropy, NULL, 0);
  if (mbedtls_ret != 0) {
    EMSG("Error seeding random number generator: -0x%04X", -mbedtls_ret);
    res = TEE_ERROR_GENERIC;
    goto cleanup;
  }

  /* setup ECKEY context */
  mbedtls_ret = mbedtls_pk_setup(sk_ctx, mbedtls_pk_info_from_type(MBEDTLS_PK_ECKEY));
  if (mbedtls_ret != 0) {
    EMSG("Error setting up ECKEY context: -0x%04X", -mbedtls_ret);
    res = TEE_ERROR_GENERIC;
    goto cleanup;
  }

  /* generate a P-256 keypair */
  mbedtls_ret = mbedtls_ecp_gen_key(MBEDTLS_ECP_DP_SECP256R1, mbedtls_pk_ec(*sk_ctx), mbedtls_ctr_drbg_random, &ctr_drbg);
  if (mbedtls_ret != 0) {
    EMSG("Error generating keypair: -0x%04X", -mbedtls_ret);
    res = TEE_ERROR_GENERIC;
    goto cleanup;
  }

  /* export key in DER form */
  mbedtls_ret = mbedtls_pk_write_key_der(sk_ctx, sk_der, sk_der_len);
  if (mbedtls_ret <= 0) {
    EMSG("Error writing key to DER: -0x%04X", -mbedtls_ret);
    res = TEE_ERROR_GENERIC;
    goto cleanup;
  }
  sk_der_len = (size_t)mbedtls_ret;

  /* mbedtls_pk_write_key_der writes at end of buffer, so move it to the front */
  memmove(sk_der, sk_der + DER_MAX_BUF_SIZE - sk_der_len, sk_der_len);

  /* store DER blob in RPMB */
  res = store_obj_in_rpmb(TA_PROVISION_KEY_PAIR_OBJ_ID, strlen(TA_PROVISION_KEY_PAIR_OBJ_ID), sk_der, sk_der_len);
  IMSG("Key generation successful");

cleanup:
  if (sk_ctx != NULL) {
    mbedtls_pk_free(sk_ctx);
    TEE_MemFill(sk_ctx, 0, sizeof(mbedtls_pk_context)); // Clear sensitive data
    TEE_Free(sk_ctx);
  }
  if (sk_der != NULL) {
    TEE_MemFill(sk_der, 0, DER_MAX_BUF_SIZE); // Clear sensitive data
    TEE_Free(sk_der);
  }

  mbedtls_ctr_drbg_free(&ctr_drbg);
  mbedtls_entropy_free(&entropy);
  return res;
}

static TEE_Result ta_provision_read_public_key(uint32_t param_types, TEE_Param params[4]) {
  TEE_Result res = TEE_ERROR_GENERIC;
  uint8_t* sk_der = TEE_Malloc(DER_MAX_BUF_SIZE, TEE_MALLOC_FILL_ZERO);
  size_t sk_der_len = DER_MAX_BUF_SIZE;
  mbedtls_pk_context* sk_ctx = TEE_Malloc(sizeof(mbedtls_pk_context), TEE_MALLOC_FILL_ZERO);
  int mbedtls_ret = -1;

  if (sk_ctx != NULL) {
    mbedtls_pk_init(sk_ctx);
  }

  /* Verify parameters */
  const uint32_t exp_param_types = TEE_PARAM_TYPES(TEE_PARAM_TYPE_MEMREF_OUTPUT, TEE_PARAM_TYPE_NONE,
                                                   TEE_PARAM_TYPE_NONE, TEE_PARAM_TYPE_NONE);

  if (param_types != exp_param_types) {
    EMSG("Invalid parameters");
    res = TEE_ERROR_BAD_PARAMETERS;
    goto cleanup;
  }
  if (sk_der == NULL || sk_ctx == NULL) {
    EMSG("Failed to allocate memory");
    res = TEE_ERROR_OUT_OF_MEMORY;
    goto cleanup;
  }

  uint8_t* out_pem = params[0].memref.buffer;
  size_t out_len = params[0].memref.size;

  if (out_pem == NULL || out_len == 0) {
    EMSG("Invalid output buffer");
    res = TEE_ERROR_BAD_PARAMETERS;
    goto cleanup;
  }

  res = load_obj_from_rpmb(TA_PROVISION_KEY_PAIR_OBJ_ID, strlen(TA_PROVISION_KEY_PAIR_OBJ_ID), sk_der, &sk_der_len);
  if (res != TEE_SUCCESS) {
    EMSG("Failed to load key from RPMB: 0x%x", res);
    goto cleanup;
  }

  mbedtls_ret = mbedtls_pk_parse_key(sk_ctx, sk_der, sk_der_len, NULL, 0);
  if (mbedtls_ret != 0) {
    EMSG("Failed to parse key: -0x%04X", -mbedtls_ret);
    res = TEE_ERROR_GENERIC;
    goto cleanup;
  }

  mbedtls_ret = mbedtls_pk_write_pubkey_pem(sk_ctx, out_pem, out_len);
  if (mbedtls_ret != 0) {
    EMSG("Failed to write public key PEM: -0x%04X", -mbedtls_ret);
    res = TEE_ERROR_GENERIC;
    goto cleanup;
  }

  IMSG("Public key read successfully");

cleanup:
  if (sk_ctx) {
    mbedtls_pk_free(sk_ctx);
    TEE_MemFill(sk_ctx, 0, sizeof(mbedtls_pk_context)); // Clear sensitive data
    TEE_Free(sk_ctx);
  }
  if (sk_der) {
    TEE_MemFill(sk_der, 0, DER_MAX_BUF_SIZE); // Clear sensitive data
    TEE_Free(sk_der);
  }
  return res;
}

static TEE_Result generate_csr(char* subject_name, char* csr_buf, size_t csr_buf_len) {
  TEE_Result res;
  uint8_t* sk_der = TEE_Malloc(DER_MAX_BUF_SIZE, TEE_MALLOC_FILL_ZERO);
  size_t sk_der_len = DER_MAX_BUF_SIZE;

  /* mbedTLS contexts */
  mbedtls_pk_context* sk_ctx = TEE_Malloc(sizeof(mbedtls_pk_context), TEE_MALLOC_FILL_ZERO);
  mbedtls_x509write_csr csr;
  mbedtls_ctr_drbg_context ctr_drbg;
  mbedtls_entropy_context entropy;
  int mbedtls_ret = -1;

  mbedtls_x509write_csr_init(&csr);
  mbedtls_ctr_drbg_init(&ctr_drbg);
  mbedtls_entropy_init(&entropy);
  if (sk_ctx != NULL) {
    mbedtls_pk_init(sk_ctx);
  }

  if (sk_der == NULL || sk_ctx == NULL) {
    EMSG("Failed to allocate memory for key or context");
    res = TEE_ERROR_OUT_OF_MEMORY;
    goto cleanup;
  }

  /* Load the private key from RPMB */
  res = load_obj_from_rpmb(TA_PROVISION_KEY_PAIR_OBJ_ID, strlen(TA_PROVISION_KEY_PAIR_OBJ_ID), sk_der, &sk_der_len);
  if (res == TEE_ERROR_ITEM_NOT_FOUND) {
      IMSG("No identity key found in RPMB; generating one");
      res = generate_identity_keypair();
      if (res != TEE_SUCCESS) {
          EMSG("Key generation failed: 0x%x", res);
          goto cleanup;
      }

      /* Try loading the key again after generation */
      res = load_obj_from_rpmb(TA_PROVISION_KEY_PAIR_OBJ_ID, strlen(TA_PROVISION_KEY_PAIR_OBJ_ID), sk_der, &sk_der_len);
  }
  if (res != TEE_SUCCESS) {
      EMSG("Failed to load key from RPMB: 0x%x", res);
      goto cleanup;
  }

  /* Parse the private key */
  mbedtls_ret = mbedtls_pk_parse_key(sk_ctx, sk_der, sk_der_len, NULL, 0);
  if (mbedtls_ret != 0) {
      EMSG("Failed to parse private key: -0x%04x", -mbedtls_ret);
      res = TEE_ERROR_GENERIC;
      goto cleanup;
  }

  /* Source entropy from TEE */
  mbedtls_ret = mbedtls_entropy_add_source(&entropy, hw_entropy_source, NULL, 32, MBEDTLS_ENTROPY_SOURCE_STRONG);
  if (mbedtls_ret != 0) {
    EMSG("Error adding entropy from TEE: -0x%04X", -mbedtls_ret);
    res = TEE_ERROR_GENERIC;
    goto cleanup;
  }

  /* Initialize random number generator */
  mbedtls_ret = mbedtls_ctr_drbg_seed(&ctr_drbg, mbedtls_entropy_func, &entropy, NULL, 0);
  if (mbedtls_ret != 0) {
      EMSG("Failed to seed RNG: -0x%04x", -mbedtls_ret);
      res = TEE_ERROR_GENERIC;
      goto cleanup;
  }

  /* Set up CSR parameters */
  mbedtls_x509write_csr_set_md_alg(&csr, MBEDTLS_MD_SHA256);
  mbedtls_x509write_csr_set_key(&csr, sk_ctx);

  mbedtls_ret = mbedtls_x509write_csr_set_subject_name(&csr, subject_name);
  if (mbedtls_ret != 0) {
      EMSG("Failed to set subject name: -0x%04x", -mbedtls_ret);
      res = TEE_ERROR_GENERIC;
      goto cleanup;
  }

  /* Generate CSR in PEM format */
  mbedtls_ret = mbedtls_x509write_csr_pem(&csr, (unsigned char *)csr_buf, csr_buf_len,
                                  mbedtls_ctr_drbg_random, &ctr_drbg);
  if (mbedtls_ret != 0) {
      EMSG("Failed to write CSR: -0x%04x", -mbedtls_ret);
      res = TEE_ERROR_GENERIC;
      goto cleanup;
  }

  res = TEE_SUCCESS;
  IMSG("CSR generated successfully");

cleanup:
  if (sk_der) {
    TEE_MemFill(sk_der, 0, DER_MAX_BUF_SIZE); // Clear sensitive data
    TEE_Free(sk_der);
  }
  if (sk_ctx) {
    mbedtls_pk_free(sk_ctx);
    TEE_MemFill(sk_ctx, 0, sizeof(mbedtls_pk_context)); // Clear sensitive data
    TEE_Free(sk_ctx);
  }
  mbedtls_x509write_csr_free(&csr);
  mbedtls_ctr_drbg_free(&ctr_drbg);
  mbedtls_entropy_free(&entropy);

  return res;
}

/* Generates the subject name given the OTP Serial Number and Subject Name prefix
 * Writes the result to the provided buffer.
 */
static TEE_Result generate_csr_subject_name(char* subject_name, size_t subject_name_len) {
  TEE_Result res;
  uint32_t serial_number[PROVISION_TA_MFG_SERIAL_NUMBER_OTP_NUM_WORDS] = {0};

  if (subject_name == NULL || subject_name_len != PROVISION_TA_SUBJECT_NAME_LENGTH ||
      subject_name_len != (sizeof(serial_number) + sizeof(SUBJECT_NAME_PREFIX))) {
    EMSG("Invalid subject name buffer or length");
    return TEE_ERROR_BAD_PARAMETERS;
  }

  res = otp_read_mem(PROVISION_TA_MFG_SERIAL_NUMBER_OTP_INDEX, serial_number,
                     PROVISION_TA_MFG_SERIAL_NUMBER_OTP_NUM_WORDS);
  if (res != TEE_SUCCESS) {
    EMSG("Failed to read manufacturing serial number from OTP: 0x%x", res);
    return res;
  }

  // Copy prefix minus null terminator
  TEE_MemMove(subject_name, SUBJECT_NAME_PREFIX, sizeof(SUBJECT_NAME_PREFIX) - 1);
  TEE_MemMove(subject_name + sizeof(SUBJECT_NAME_PREFIX) - 1, serial_number, sizeof(serial_number));

  subject_name[PROVISION_TA_SUBJECT_NAME_LENGTH - 1] = '\0';

  return TEE_SUCCESS;
}

static TEE_Result ta_provision_generate_csr(uint32_t param_types, TEE_Param params[4]) {
  TEE_Result res;

  uint32_t exp_param_types =
    TEE_PARAM_TYPES(TEE_PARAM_TYPE_MEMREF_OUTPUT, TEE_PARAM_TYPE_NONE, TEE_PARAM_TYPE_NONE,
                    TEE_PARAM_TYPE_NONE);

  if (param_types != exp_param_types)
    return TEE_ERROR_BAD_PARAMETERS;

  char* csr_buf = params[0].memref.buffer;
  size_t csr_buf_len = params[0].memref.size;

  if (csr_buf == NULL || csr_buf_len == 0) {
    EMSG("Invalid parameters");
    return TEE_ERROR_BAD_PARAMETERS;
  }

  /* Read serial number from OTP and write generate the CSR CN */
  char* subject_name = (char*)TEE_Malloc(PROVISION_TA_SUBJECT_NAME_LENGTH, TEE_MALLOC_FILL_ZERO);

  res = generate_csr_subject_name(subject_name, PROVISION_TA_SUBJECT_NAME_LENGTH);
  if (res != TEE_SUCCESS) {
    EMSG("Failed to generate subject name: 0x%x", res);
    goto cleanup;
  }

  IMSG("Subject Name: %s", subject_name);
  res = generate_csr(subject_name, csr_buf, csr_buf_len);

  /* Overwrite size parameter with length of written data
   * Since we are using a CSR PEM, we can just use strnlen here */
  params[0].memref.size = strnlen(csr_buf, csr_buf_len);
  IMSG("CSR generated successfully, length: %zu bytes", params[0].memref.size);

cleanup:
  if (subject_name) {
    TEE_Free(subject_name);
  }

  return res;
}

/* Takes in a buffer and size_t pointer to a pem buf and reads the device cert from rpmb */
static TEE_Result get_device_cert_pem(uint8_t* device_cert_pem, size_t* device_cert_pem_len) {
  TEE_Result res = TEE_ERROR_GENERIC;
  int mbedtls_ret = -1;
  mbedtls_x509_crt* crt = NULL;

  if (device_cert_pem == NULL || device_cert_pem_len == NULL || *device_cert_pem_len == 0) {
    EMSG("Invalid parameters for reading device certificate");
    return TEE_ERROR_BAD_PARAMETERS;
  }

  uint8_t* device_cert_der = TEE_Malloc(DER_MAX_BUF_SIZE, TEE_MALLOC_FILL_ZERO);
  size_t device_cert_der_len = DER_MAX_BUF_SIZE;
  if (device_cert_der == NULL) {
    EMSG("Failed to allocate memory for device certificate DER buffer");
    return TEE_ERROR_OUT_OF_MEMORY;
  }

  res = load_obj_from_rpmb(TA_PROVISION_CERT_OBJ_ID, strlen(TA_PROVISION_CERT_OBJ_ID), device_cert_der, &device_cert_der_len);
  if (res != TEE_SUCCESS) {
    EMSG("Failed to load device cert from RPMB: 0x%x", res);
    goto cleanup;
  }

  crt = TEE_Malloc(sizeof(mbedtls_x509_crt), TEE_MALLOC_FILL_ZERO);
  if (crt == NULL) {
    EMSG("Failed to allocate memory for mbedtls_x509_crt structure");
    res = TEE_ERROR_OUT_OF_MEMORY;
    goto cleanup;
  }
  mbedtls_x509_crt_init(crt);

  mbedtls_ret = mbedtls_x509_crt_parse_der(crt, device_cert_der, device_cert_der_len);
  if (mbedtls_ret != 0) {
    EMSG("Failed to parse device certificate: -0x%04X", -mbedtls_ret);
    res = TEE_ERROR_BAD_FORMAT;
    goto cleanup;
  }

  mbedtls_ret = mbedtls_pem_write_buffer(
               "-----BEGIN CERTIFICATE-----\n",
               "-----END CERTIFICATE-----\n",
               device_cert_der, device_cert_der_len,
               device_cert_pem, *device_cert_pem_len, device_cert_pem_len);

  if (mbedtls_ret < 0) {
    EMSG("Failed to write device certificate to PEM: -0x%04X", -mbedtls_ret);
    res = TEE_ERROR_GENERIC;
    goto cleanup;
  }

cleanup:
  if (device_cert_der != NULL) {
    TEE_Free(device_cert_der);
  }
  if (crt != NULL) {
    mbedtls_x509_crt_free(crt);
    TEE_Free(crt);
  }

  return res;
}

static TEE_Result ta_provision_read_certificate(uint32_t param_types, TEE_Param params[4]) {
  uint32_t exp_param_types = TEE_PARAM_TYPES(TEE_PARAM_TYPE_MEMREF_OUTPUT, TEE_PARAM_TYPE_NONE,
                                             TEE_PARAM_TYPE_NONE, TEE_PARAM_TYPE_NONE);

  if (param_types != exp_param_types)
    return TEE_ERROR_BAD_PARAMETERS;

  TEE_Result res;
  uint8_t* cert_buf = params[0].memref.buffer;
  size_t cert_buf_len = params[0].memref.size;
  if (cert_buf == NULL || cert_buf_len == 0) {
    EMSG("Invalid parameters");
    return TEE_ERROR_BAD_PARAMETERS;
  }

  res = get_device_cert_pem(cert_buf, &cert_buf_len);
  if (res != TEE_SUCCESS) {
    EMSG("Failed to get device certificate: 0x%x", res);
    return res;
  }

  IMSG("Device certificate read successfully");

  /* Overwrite size parameter with length of written data
   * Since we are using a certificate PEM, we can just use strnlen here
   */
  params[0].memref.size = strnlen((char*)cert_buf, cert_buf_len);

  return res;
}

static uint32_t _read_encoded_length_field(const uint8_t* buf) {
  if (buf == NULL) {
    return 0;
  }

  return ((uint32_t)buf[0] << 24) | ((uint32_t)buf[1] << 16) |
         ((uint32_t)buf[2] << 8) | (uint32_t)buf[3];
}

/* Given an encoded cert chain in the binary protocol, parse out the certificate chain into mbedtls structs */
static TEE_Result parse_cert_chain(const uint8_t* cert_chain, size_t cert_chain_len, 
                                   mbedtls_x509_crt* root_ca, mbedtls_x509_crt* chain) {
  int mbedtls_ret = -1;
  size_t idx = 0;

  if (cert_chain == NULL || cert_chain_len == 0 || root_ca == NULL || chain == NULL) {
    EMSG("Invalid parameters for parsing certificate chain");
    return TEE_ERROR_BAD_PARAMETERS;
  }

  // First field is the number of certificates in the chain
  if (cert_chain_len < CMD_NUM_CERTS_FIELD_LEN) {
    return TEE_ERROR_BAD_FORMAT;
  }
  size_t expected_num_certs = _read_encoded_length_field(cert_chain);
  if (expected_num_certs == 0 || expected_num_certs > MAX_CERTIFICATES_IN_CHAIN) {
    EMSG("Invalid number of certificates in chain: %zu", expected_num_certs);
    return TEE_ERROR_BAD_FORMAT;
  }
  idx += CMD_NUM_CERTS_FIELD_LEN; // Move past the num_certs field

  /* Cert ordering is: device cert -> yk cert -> aws cert -> rootca cert */
  mbedtls_x509_crt* current_chain;
  size_t cert_count = 0;
  while (cert_count < expected_num_certs && idx < cert_chain_len) {
    /* Last cert is the RootCA cert which is parsed to the trust anchor */
    if (cert_count + 1 == expected_num_certs) {
      DMSG("Parsing Root CA certificate at index %zu", idx);
      current_chain = root_ca;
    } else {
      DMSG("Parsing certificate %zu at index %zu", cert_count + 1, idx);
      current_chain = chain;
    }

    if (cert_chain_len - idx < CMD_LEN_FIELD_LEN) {
      return TEE_ERROR_BAD_FORMAT;
    }
    uint32_t cert_len = _read_encoded_length_field(cert_chain + idx);
    idx += CMD_LEN_FIELD_LEN; // Move past the length field
    if (cert_len == 0 || cert_len > cert_chain_len - idx) {
      EMSG("Invalid certificate length: %u at index %zu", cert_len, idx);
      return TEE_ERROR_BAD_FORMAT;
    }

    DMSG("Found certificate of length %u at index %zu", cert_len, idx);
    mbedtls_ret = mbedtls_x509_crt_parse_der(current_chain, cert_chain + idx, cert_len);
    if (mbedtls_ret != 0) {
      EMSG("Failed to parse certificate at index %zu: -0x%04X", idx, -mbedtls_ret);
      return TEE_ERROR_BAD_FORMAT;
    }

    DMSG("Parsed certificate %zu successfully", cert_count + 1);
    idx += cert_len; // Move to the next certificate length field
    cert_count++;
  }

  IMSG("Found %zu certificates in the chain", cert_count);
  if (cert_count != expected_num_certs) {
    EMSG("Mismatch in expected number of certificates: %zu vs %zu", cert_count, expected_num_certs);
    return TEE_ERROR_BAD_FORMAT;
  }

  return TEE_SUCCESS;
}

static TEE_Result verify_device_cert_pk(const uint8_t* pk_raw, size_t pk_raw_len) {
  TEE_Result res = TEE_ERROR_GENERIC;
  int mbedtls_ret = -1;

  uint8_t* device_cert_sk = TEE_Malloc(DER_MAX_BUF_SIZE, TEE_MALLOC_FILL_ZERO);
  size_t device_cert_sk_len = DER_MAX_BUF_SIZE;
  uint8_t* device_cert_pk = TEE_Malloc(DER_MAX_BUF_SIZE, TEE_MALLOC_FILL_ZERO);
  size_t device_cert_pk_len = DER_MAX_BUF_SIZE;
  mbedtls_pk_context* sk_ctx = NULL;

  if (pk_raw == NULL || pk_raw_len == 0 || pk_raw_len > DER_MAX_BUF_SIZE) {
    EMSG("Invalid parameters for verifying device certificate public key");
    res = TEE_ERROR_BAD_PARAMETERS;
    goto cleanup;
  }
  if (device_cert_sk == NULL || device_cert_pk == NULL) {
    EMSG("Failed to allocate memory for device certificate key buffers");
    res = TEE_ERROR_OUT_OF_MEMORY;
    goto cleanup;
  }

  /* Read the device cert private key from RPMB */
  res = load_obj_from_rpmb(TA_PROVISION_KEY_PAIR_OBJ_ID, strlen(TA_PROVISION_KEY_PAIR_OBJ_ID),
                           device_cert_sk, &device_cert_sk_len);
  if (res != TEE_SUCCESS) {
    EMSG("Failed to load device cert secret key from RPMB: 0x%x", res);
    goto cleanup;
  }

  sk_ctx = TEE_Malloc(sizeof(mbedtls_pk_context), TEE_MALLOC_FILL_ZERO);
  if (sk_ctx == NULL) {
    EMSG("Failed to allocate memory for key context");
    res = TEE_ERROR_OUT_OF_MEMORY;
    goto cleanup;
  }
  mbedtls_pk_init(sk_ctx);
  mbedtls_ret = mbedtls_pk_parse_key(sk_ctx, device_cert_sk, device_cert_sk_len, NULL, 0);
  if (mbedtls_ret != 0) {
    res = TEE_ERROR_GENERIC;
    EMSG("Failed to parse key: -0x%04X", -mbedtls_ret);
    goto cleanup;
  }

  // overwrite the device_cert_sk with 0
  TEE_MemFill(device_cert_sk, 0, device_cert_sk_len);

  mbedtls_ret = mbedtls_pk_write_pubkey_der(sk_ctx, device_cert_pk, device_cert_pk_len);
  if (mbedtls_ret < 0) {
    res = TEE_ERROR_GENERIC;
    EMSG("Failed to write pubkey der to buffer");
    goto cleanup;
  }

  /* mbedtls_pk_write_pubkey_der returns the length of the written data
   * DER is at offset device_cert_pk + (device_cert_pk_len - ret) */
  device_cert_pk_len = mbedtls_ret;
  if (device_cert_pk_len > DER_MAX_BUF_SIZE) {
    EMSG("Public key DER length exceeds buffer size: %zu > %zu", device_cert_pk_len, DER_MAX_BUF_SIZE);
    res = TEE_ERROR_GENERIC;
    goto cleanup;
  }

  /* The public key is written at the end of the buffer, so we need to adjust the pointer */
  uint8_t* device_cert_pk_start = device_cert_pk + DER_MAX_BUF_SIZE - device_cert_pk_len;

  /* Compare the raw public key bytes */
  if (pk_raw_len != device_cert_pk_len || TEE_MemCompare(pk_raw, device_cert_pk_start, pk_raw_len) != 0) {
    EMSG("Device certificate public key does not match the expected value");
    EMSG("Expected public key length: %zu, Actual: %zu", pk_raw_len, device_cert_pk_len);
    res = TEE_ERROR_SECURITY;
    goto cleanup;
  }

  res = TEE_SUCCESS;
  IMSG("Device certificate public key verified successfully");
cleanup:
  if (sk_ctx != NULL) {
    mbedtls_pk_free(sk_ctx);
    TEE_MemFill(sk_ctx, 0, sizeof(mbedtls_pk_context)); // Clear sensitive data
    TEE_Free(sk_ctx);
  }
  if (device_cert_sk != NULL) {
    TEE_MemFill(device_cert_sk, 0, DER_MAX_BUF_SIZE); // Clear sensitive data
    TEE_Free(device_cert_sk);
  }
  if (device_cert_pk != NULL) {
    TEE_Free(device_cert_pk);
  }
  return res;
}

static TEE_Result verify_root_ca_cert_pk(const uint8_t* pk_raw, size_t pk_raw_len) {
  TEE_Result res = TEE_ERROR_GENERIC;
  bool is_production = false;

  res = is_device_production_fused(&is_production);
  if (res != TEE_SUCCESS) {
    EMSG("Failed to check device production fuse: 0x%x", res);
    return res;
  }

  if (pk_raw == NULL || pk_raw_len != ECC_PK_X509_SIZE) {
    EMSG("Invalid parameters for verifying Root CA public key");
    DMSG("Expected ECC key size: %d, Actual: %zu", ECC_PK_X509_SIZE, pk_raw_len);
    return TEE_ERROR_BAD_PARAMETERS;
  }

  // Set a pointer to the correct Root CA public key based on operating environment
  const uint8_t *root_ca_pk;
  if (is_production) {
    root_ca_pk = ROOT_CA_PK_PROD;
    IMSG("Using production Root CA public key for verification");
  } else {
    root_ca_pk = ROOT_CA_PK_DEV;
    IMSG("Using development Root CA public key for verification");
  }

  /* Only compare the last 64 bytes which are the x||y component of the public key */
  size_t pk_offset = ECC_PK_X509_SIZE - ECC_PK_SIZE_BYTES;
  if (TEE_MemCompare(pk_raw + pk_offset, root_ca_pk, ECC_PK_SIZE_BYTES) == 0) {
    IMSG("Root CA public key passed verification");
    return TEE_SUCCESS;
  }

  EMSG("Root CA public key failed verification");
  return TEE_ERROR_SECURITY;
}

/* Verifies the device certificate chain and stores in RPMB if successful */
static TEE_Result verify_cert_chain_and_store(mbedtls_x509_crt* root_ca, mbedtls_x509_crt* chain) {
  TEE_Result res = TEE_ERROR_GENERIC;
  int mbedtls_ret = -1;
  char* subject_name = NULL;
  char* verify_buf = NULL;

  /* Generate subject name for cert verification */
  subject_name = (char*)TEE_Malloc(PROVISION_TA_SUBJECT_NAME_LENGTH, TEE_MALLOC_FILL_ZERO);
  res = generate_csr_subject_name(subject_name, PROVISION_TA_SUBJECT_NAME_LENGTH);
  if (res != TEE_SUCCESS) {
    EMSG("Failed to generate subject name for verification: 0x%x", res);
    goto cleanup;
  }
  IMSG("Subject Name for verification: %s", subject_name);

  verify_buf = TEE_Malloc(GENERIC_BUF_SIZE, TEE_MALLOC_FILL_ZERO);
  if (verify_buf == NULL) {
    EMSG("Failed to allocate memory for verification buffer");
    res = TEE_ERROR_OUT_OF_MEMORY;
    goto cleanup;
  }

  /* Verify the certificate chain */
  uint32_t flags;
  mbedtls_x509_crt_profile profile = {
    .allowed_mds = MBEDTLS_X509_ID_FLAG(MBEDTLS_MD_SHA256), /* Only allow SHA256 */
    .allowed_pks = MBEDTLS_X509_ID_FLAG(MBEDTLS_PK_ECDSA) | MBEDTLS_X509_ID_FLAG(MBEDTLS_PK_ECKEY), /* Only allow ECC */
    .allowed_curves = MBEDTLS_X509_ID_FLAG(MBEDTLS_ECP_DP_SECP256R1), /* Only allow P-256/prime256v1 */
    .rsa_min_bitlen = 0  /* Not used since we only allow ECDSA */
  };

  mbedtls_ret = mbedtls_x509_crt_verify_with_profile(chain, root_ca,
                      NULL, /* CRL */
                      &profile,
                      subject_name + COMMON_NAME_PREFIX_LENGTH, /* CN to verify */
                      &flags,
                      NULL, /* verify callback */
                      NULL); /* context for verify callback */

  if(mbedtls_ret != 0 || flags != 0) {
    mbedtls_x509_crt_verify_info(verify_buf, GENERIC_BUF_SIZE, "  ! ", flags);
    EMSG(" failed to verify cert chain:%s\n", verify_buf);
    EMSG("Certificate chain verification failed with error: -0x%04x", -mbedtls_ret);
    res = TEE_ERROR_SECURITY;
    goto cleanup;
  }

  /* Verify device cert public key matches what is stored in RPMB */
  res = verify_device_cert_pk(chain->pk_raw.p, chain->pk_raw.len);
  if (res != TEE_SUCCESS) {
    EMSG("Device certificate public key verification failed: 0x%x", res);
    goto cleanup;
  }

  res = verify_root_ca_cert_pk(root_ca->pk_raw.p, root_ca->pk_raw.len);
  if (res != TEE_SUCCESS) {
    EMSG("Root CA public key verification failed: 0x%x", res);
    goto cleanup;
  }

  IMSG("All certificates verified successfully\n");

  /* Write the device cert to RPMB in DER format */
  res = store_obj_in_rpmb(TA_PROVISION_CERT_OBJ_ID, strlen(TA_PROVISION_CERT_OBJ_ID), chain->raw.p, chain->raw.len);
  if (res != TEE_SUCCESS) {
    EMSG("Failed to write device certificate: 0x%x", res);
    goto cleanup;
  }

cleanup:
  if (subject_name != NULL) {
    TEE_Free(subject_name);
  }
  if (verify_buf != NULL) {
    TEE_Free(verify_buf);
  }

  return res;
}

static TEE_Result ta_provision_validate_and_write_device_cert(uint32_t param_types, TEE_Param params[4]) {
  uint32_t exp_param_types = TEE_PARAM_TYPES(TEE_PARAM_TYPE_MEMREF_INPUT, TEE_PARAM_TYPE_MEMREF_OUTPUT,
                                             TEE_PARAM_TYPE_NONE, TEE_PARAM_TYPE_NONE);

  if (param_types != exp_param_types)
    return TEE_ERROR_BAD_PARAMETERS;

  TEE_Result res;
  const uint8_t* cert_chain = params[0].memref.buffer;
  size_t cert_chain_len = params[0].memref.size;
  uint8_t* device_cert_pem = params[1].memref.buffer;
  size_t device_cert_pem_len = params[1].memref.size;

  if (cert_chain == NULL || cert_chain_len == 0 || device_cert_pem == NULL || device_cert_pem_len == 0) {
    EMSG("Invalid parameters for parsing certificate chain");
    return TEE_ERROR_BAD_PARAMETERS;
  }

  mbedtls_x509_crt* root_ca = TEE_Malloc(sizeof(mbedtls_x509_crt), TEE_MALLOC_FILL_ZERO);
  mbedtls_x509_crt* chain = TEE_Malloc(sizeof(mbedtls_x509_crt), TEE_MALLOC_FILL_ZERO);
  if (root_ca != NULL) {
    mbedtls_x509_crt_init(root_ca);
  }
  if (chain != NULL) {
    mbedtls_x509_crt_init(chain);
  }

  if (root_ca == NULL || chain == NULL) {
    EMSG("Failed to allocate memory for certificate structures");
    res = TEE_ERROR_OUT_OF_MEMORY;
    goto cleanup;
  }

  res = parse_cert_chain(cert_chain, cert_chain_len, root_ca, chain);
  if (res != TEE_SUCCESS) {
    EMSG("Failed to parse certificate chain: 0x%x", res);
    goto cleanup;
  }

  res = verify_cert_chain_and_store(root_ca, chain);
  if (res != TEE_SUCCESS) {
    EMSG("Failed to verify or store device certificate: 0x%x", res);
    goto cleanup;
  }

  /* Device cert was successfully stored in rpmb. Read it out and return as PEM */

  res = get_device_cert_pem(device_cert_pem, &device_cert_pem_len);
  if (res != TEE_SUCCESS) {
    EMSG("Failed to get device certificate PEM: 0x%x", res);
    goto cleanup;
  }
  params[1].memref.size = device_cert_pem_len; // Update the size to the actual PEM length

  IMSG("Certificate chain parsed successfully. Device certificate size: %zu bytes", params[1].memref.size);

cleanup:
  if (chain != NULL) {
    mbedtls_x509_crt_free(chain);
    TEE_Free(chain);
  }
  if (root_ca != NULL) {
    mbedtls_x509_crt_free(root_ca);
    TEE_Free(root_ca);
  }

  return res;
}

/* Denylist of revoked issuer certificate subject DNs (DER-encoded).
 * Device certs issued by these YubiKeys are considered revoked.
 * Source: proto-attestation-eol-signer/resources/c3/prod/yubikeys
 */
#define REVOKED_ISSUER_COUNT 8
#define REVOKED_ISSUER_DN_SIZE 42
static const uint8_t REVOKED_ISSUER_DNS[REVOKED_ISSUER_COUNT][REVOKED_ISSUER_DN_SIZE] = {
  /* Proto_EOL_Issuing_CA_33126891 (orange) */
  { 0x30, 0x28, 0x31, 0x26, 0x30, 0x24, 0x06, 0x03, 0x55, 0x04, 0x03, 0x0c, 0x1d, 0x50, 0x72, 0x6f,
    0x74, 0x6f, 0x5f, 0x45, 0x4f, 0x4c, 0x5f, 0x49, 0x73, 0x73, 0x75, 0x69, 0x6e, 0x67, 0x5f, 0x43,
    0x41, 0x5f, 0x33, 0x33, 0x31, 0x32, 0x36, 0x38, 0x39, 0x31 },
  /* Proto_EOL_Issuing_CA_33126688 (pink) */
  { 0x30, 0x28, 0x31, 0x26, 0x30, 0x24, 0x06, 0x03, 0x55, 0x04, 0x03, 0x0c, 0x1d, 0x50, 0x72, 0x6f,
    0x74, 0x6f, 0x5f, 0x45, 0x4f, 0x4c, 0x5f, 0x49, 0x73, 0x73, 0x75, 0x69, 0x6e, 0x67, 0x5f, 0x43,
    0x41, 0x5f, 0x33, 0x33, 0x31, 0x32, 0x36, 0x36, 0x38, 0x38 },
  /* Proto_EOL_Issuing_CA_26872184 (red) */
  { 0x30, 0x28, 0x31, 0x26, 0x30, 0x24, 0x06, 0x03, 0x55, 0x04, 0x03, 0x0c, 0x1d, 0x50, 0x72, 0x6f,
    0x74, 0x6f, 0x5f, 0x45, 0x4f, 0x4c, 0x5f, 0x49, 0x73, 0x73, 0x75, 0x69, 0x6e, 0x67, 0x5f, 0x43,
    0x41, 0x5f, 0x32, 0x36, 0x38, 0x37, 0x32, 0x31, 0x38, 0x34 },
  /* Proto_EOL_Issuing_CA_26872282 (silver) */
  { 0x30, 0x28, 0x31, 0x26, 0x30, 0x24, 0x06, 0x03, 0x55, 0x04, 0x03, 0x0c, 0x1d, 0x50, 0x72, 0x6f,
    0x74, 0x6f, 0x5f, 0x45, 0x4f, 0x4c, 0x5f, 0x49, 0x73, 0x73, 0x75, 0x69, 0x6e, 0x67, 0x5f, 0x43,
    0x41, 0x5f, 0x32, 0x36, 0x38, 0x37, 0x32, 0x32, 0x38, 0x32 },
  /* Proto_EOL_Issuing_CA_33126571 (white) */
  { 0x30, 0x28, 0x31, 0x26, 0x30, 0x24, 0x06, 0x03, 0x55, 0x04, 0x03, 0x0c, 0x1d, 0x50, 0x72, 0x6f,
    0x74, 0x6f, 0x5f, 0x45, 0x4f, 0x4c, 0x5f, 0x49, 0x73, 0x73, 0x75, 0x69, 0x6e, 0x67, 0x5f, 0x43,
    0x41, 0x5f, 0x33, 0x33, 0x31, 0x32, 0x36, 0x35, 0x37, 0x31 },
  /* Proto_EOL_Issuing_CA_31953414 (yellow) */
  { 0x30, 0x28, 0x31, 0x26, 0x30, 0x24, 0x06, 0x03, 0x55, 0x04, 0x03, 0x0c, 0x1d, 0x50, 0x72, 0x6f,
    0x74, 0x6f, 0x5f, 0x45, 0x4f, 0x4c, 0x5f, 0x49, 0x73, 0x73, 0x75, 0x69, 0x6e, 0x67, 0x5f, 0x43,
    0x41, 0x5f, 0x33, 0x31, 0x39, 0x35, 0x33, 0x34, 0x31, 0x34 },
  /* Proto_EOL_Issuing_CA_37110853 (foxconn-sj-1) - SECENG-9521 */
  { 0x30, 0x28, 0x31, 0x26, 0x30, 0x24, 0x06, 0x03, 0x55, 0x04, 0x03, 0x0c, 0x1d, 0x50, 0x72, 0x6f,
    0x74, 0x6f, 0x5f, 0x45, 0x4f, 0x4c, 0x5f, 0x49, 0x73, 0x73, 0x75, 0x69, 0x6e, 0x67, 0x5f, 0x43,
    0x41, 0x5f, 0x33, 0x37, 0x31, 0x31, 0x30, 0x38, 0x35, 0x33 },
  /* Proto_EOL_Issuing_CA_35998998 (foxconn-sj-2) - SECENG-9521 */
  { 0x30, 0x28, 0x31, 0x26, 0x30, 0x24, 0x06, 0x03, 0x55, 0x04, 0x03, 0x0c, 0x1d, 0x50, 0x72, 0x6f,
    0x74, 0x6f, 0x5f, 0x45, 0x4f, 0x4c, 0x5f, 0x49, 0x73, 0x73, 0x75, 0x69, 0x6e, 0x67, 0x5f, 0x43,
    0x41, 0x5f, 0x33, 0x35, 0x39, 0x39, 0x38, 0x39, 0x39, 0x38 },
};

/* Checks if the stored device certificate was issued by a denylisted issuer.
 * Returns TEE_SUCCESS and writes 1 byte to the output buffer:
 *   0x01 = valid (issuer not in denylist)
 *   0x00 = revoked (issuer is in denylist)
 *   0x02 = certificate not found (no device certificate stored)
 */
static TEE_Result ta_provision_is_cert_valid(uint32_t param_types, TEE_Param params[4]) {
  uint32_t exp_param_types = TEE_PARAM_TYPES(TEE_PARAM_TYPE_MEMREF_OUTPUT, TEE_PARAM_TYPE_NONE,
                                             TEE_PARAM_TYPE_NONE, TEE_PARAM_TYPE_NONE);

  if (param_types != exp_param_types)
    return TEE_ERROR_BAD_PARAMETERS;

  TEE_Result res;
  int mbedtls_ret;
  uint8_t* out_buf = params[0].memref.buffer;
  size_t out_buf_len = params[0].memref.size;

  if (out_buf == NULL || out_buf_len < 1) {
    EMSG("Invalid output buffer for is_cert_valid");
    return TEE_ERROR_BAD_PARAMETERS;
  }

  uint8_t* device_cert_der = TEE_Malloc(DER_MAX_BUF_SIZE, TEE_MALLOC_FILL_ZERO);
  size_t device_cert_der_len = DER_MAX_BUF_SIZE;
  mbedtls_x509_crt* crt = NULL;

  if (device_cert_der == NULL) {
    EMSG("Failed to allocate memory for device certificate DER buffer");
    return TEE_ERROR_OUT_OF_MEMORY;
  }

  res = load_obj_from_rpmb(TA_PROVISION_CERT_OBJ_ID, strlen(TA_PROVISION_CERT_OBJ_ID),
                           device_cert_der, &device_cert_der_len);
  if (res == TEE_ERROR_ITEM_NOT_FOUND) {
    IMSG("No device certificate found in RPMB: 0x%x", res);
    out_buf[0] = CERT_STATUS_NOT_FOUND;
    params[0].memref.size = 1;
    res = TEE_SUCCESS;
    goto cleanup;
  }
  if (res != TEE_SUCCESS) {
    EMSG("Failed to load device certificate from RPMB: 0x%x", res);
    goto cleanup;
  }

  crt = TEE_Malloc(sizeof(mbedtls_x509_crt), TEE_MALLOC_FILL_ZERO);
  if (crt == NULL) {
    EMSG("Failed to allocate memory for mbedtls_x509_crt structure");
    res = TEE_ERROR_OUT_OF_MEMORY;
    goto cleanup;
  }
  mbedtls_x509_crt_init(crt);

  mbedtls_ret = mbedtls_x509_crt_parse_der(crt, device_cert_der, device_cert_der_len);
  if (mbedtls_ret != 0) {
    EMSG("Failed to parse device certificate: -0x%04X", -mbedtls_ret);
    res = TEE_ERROR_BAD_FORMAT;
    goto cleanup;
  }

  /* Print the issuer DN for debugging */
  char issuer_buf[GENERIC_BUF_SIZE];
  mbedtls_ret = mbedtls_x509_dn_gets(issuer_buf, sizeof(issuer_buf), &crt->issuer);
  if (mbedtls_ret > 0) {
    DMSG("Device certificate issuer: %s", issuer_buf);
  } else {
    DMSG("Failed to format issuer DN: -0x%04X", -mbedtls_ret);
  }

  /* Check if the device cert's issuer DN matches any revoked issuer */
  out_buf[0] = CERT_STATUS_VALID;
  for (size_t i = 0; i < REVOKED_ISSUER_COUNT; i++) {
    if (crt->issuer_raw.len == REVOKED_ISSUER_DN_SIZE &&
        TEE_MemCompare(crt->issuer_raw.p, REVOKED_ISSUER_DNS[i], REVOKED_ISSUER_DN_SIZE) == 0) {
      IMSG("Device certificate issued by REVOKED issuer (index %zu): %s", i,
           mbedtls_ret > 0 ? issuer_buf : "unknown");
      out_buf[0] = CERT_STATUS_REVOKED;
      break;
    }
  }

  params[0].memref.size = 1;
  if (out_buf[0] == CERT_STATUS_VALID) {
    IMSG("Device certificate is valid");
  }

  res = TEE_SUCCESS;

cleanup:
  if (device_cert_der != NULL) {
    TEE_Free(device_cert_der);
  }
  if (crt != NULL) {
    mbedtls_x509_crt_free(crt);
    TEE_Free(crt);
  }

  return res;
}

TEE_Result device_identity_ta_invoke_command_entrypoint(uint32_t cmd_id,
                                                         uint32_t param_types,
                                                         TEE_Param params[4],
                                                         bool allow_cert_write) {
  switch (cmd_id) {
    case TA_PROVISION_CMD_GENERATE_CSR:
      return ta_provision_generate_csr(param_types, params);
    case TA_PROVISION_CMD_READ_PUB_KEY:
      return ta_provision_read_public_key(param_types, params);
    case TA_PROVISION_CMD_VALIDATE_AND_WRITE_CERT:
      if (!allow_cert_write) {
        return TEE_ERROR_NOT_SUPPORTED;
      }
      return ta_provision_validate_and_write_device_cert(param_types, params);
    case TA_PROVISION_CMD_READ_CERT:
      return ta_provision_read_certificate(param_types, params);
    case TA_PROVISION_CMD_IS_CERT_VALID:
      return ta_provision_is_cert_valid(param_types, params);
    default:
      return TEE_ERROR_BAD_PARAMETERS;
  }
}

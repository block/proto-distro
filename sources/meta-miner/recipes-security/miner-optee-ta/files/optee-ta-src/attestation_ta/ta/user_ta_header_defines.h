/*
 * The name of this file must not be modified
 */

#ifndef USER_TA_HEADER_DEFINES_H
#define USER_TA_HEADER_DEFINES_H

/* To get the TA UUID definition */
#include <attestation_ta.h>

/* Uses the same UUID as provision_ta so both variants share the same
 * RPMB storage namespace (key pair + certificate objects). Only one
 * variant is installed per image: provision_ta for mfg, attestation_ta
 * for release. */
#define TA_UUID TA_PROVISION_UUID

/*
 * TA properties: multi-instance TA, no specific attribute
 * TA_FLAG_EXEC_DDR is meaningless but mandated.
 */
#define TA_FLAGS TA_FLAG_EXEC_DDR

/* Provisioned stack size */
#define TA_STACK_SIZE (4 * 1024)

/* Provisioned heap size for TEE_Malloc() and friends */
#define TA_DATA_SIZE (32 * 1024)

/* The gpd.ta.version property */
#define TA_VERSION "0.1"

/* The gpd.ta.description property */
#define TA_DESCRIPTION "Proto Miner Attestation Trusted Application"

#endif /* USER_TA_HEADER_DEFINES_H */

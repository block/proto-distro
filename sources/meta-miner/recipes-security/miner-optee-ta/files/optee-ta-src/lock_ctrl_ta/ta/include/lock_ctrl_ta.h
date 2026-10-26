#ifndef TA_LOCK_CTRL_H
#define TA_LOCK_CTRL_H

/*
 * This UUID is generated with uuidgen
 * the ITU-T UUID generator at http://www.itu.int/ITU-T/asn1/uuid.html
 */
#define TA_LOCK_CTRL_UUID \
  {0xa75709fa, 0xf0cc, 0x41c6, {0x98,0xec, 0xb3, 0x9b, 0x1f, 0x99, 0x31, 0x16}}

/* The function IDs implemented in this TA */
#define TA_LOCK_CTRL_CMD_STATUS  0
#define TA_LOCK_CTRL_CMD_ENABLE_SECBOOT  1
#define TA_LOCK_CTRL_CMD_USER_UNLOCK_SECBOOT  2
#define TA_LOCK_CTRL_CMD_NVCTR_READ  3


#define TA_LOCK_CTRL_STATE_OPEN 0 // Both device NOT closed and user unlock register is unwritten
#define TA_LOCK_CTRL_STATE_CLOSED 1 // Device is closed and user unlock register is 0
#define TA_LOCK_CTRL_STATE_UNLOCKED 2 // User unlock register is NOT 0
#define TA_LOCK_CTRL_STATE_UNINITIALIZED 3 // RPMB key is not programmed; also synthesized by the host probe
#define TA_LOCK_CTRL_STATE_UNKNOWN 4 // Default unknown state

#endif /*TA_LOCK_CTRL_H*/

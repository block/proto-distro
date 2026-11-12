#include "libminer_optee_host_common/rpmb.h"

#include <fcntl.h>
#include <linux/mmc/ioctl.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <unistd.h>

#define MMC_READ_MULTIPLE_BLOCK 18U
#define MMC_WRITE_MULTIPLE_BLOCK 25U

#define MMC_RSP_PRESENT (1U << 0)
#define MMC_RSP_CRC (1U << 2)
#define MMC_RSP_OPCODE (1U << 4)
#define MMC_CMD_ADTC (1U << 5)
#define MMC_RSP_SPI_R1 (1U << 7)
#define MMC_RSP_R1 (MMC_RSP_PRESENT | MMC_RSP_CRC | MMC_RSP_OPCODE)
#define RPMB_MMC_FLAGS (MMC_RSP_SPI_R1 | MMC_RSP_R1 | MMC_CMD_ADTC)

#define RPMB_FRAME_SIZE 512U
#define RPMB_RESULT_OFFSET 508U
#define RPMB_REQ_RESP_OFFSET 510U
#define RPMB_REQUEST_READ_COUNTER 0x0002U
#define RPMB_RESPONSE_READ_COUNTER 0x0200U
#define RPMB_RESULT_MASK 0x007fU
#define RPMB_RESULT_COUNTER_EXPIRED 0x0080U
#define RPMB_RESULT_OK 0x0000U
#define RPMB_RESULT_NO_AUTH_KEY 0x0007U
#define RPMB_COMMAND_COUNT 2U

typedef struct {
  uint8_t bytes[RPMB_FRAME_SIZE];
} rpmb_frame_t;

static uint16_t get_be16(const uint8_t* value) {
  return (uint16_t)(((uint16_t)value[0] << 8) | value[1]);
}

static void put_be16(uint8_t* value, uint16_t input) {
  value[0] = (uint8_t)(input >> 8);
  value[1] = (uint8_t)input;
}

static int rpmb_parse_key_status(const rpmb_frame_t* response, rpmb_key_status_t* status) {
  uint16_t result;

  if (response == NULL || status == NULL) {
    return -1;
  }

  *status = RPMB_KEY_STATUS_UNKNOWN;
  if (get_be16(&response->bytes[RPMB_REQ_RESP_OFFSET]) != RPMB_RESPONSE_READ_COUNTER) {
    return -1;
  }

  result = get_be16(&response->bytes[RPMB_RESULT_OFFSET]);
  if (result & ~(RPMB_RESULT_MASK | RPMB_RESULT_COUNTER_EXPIRED)) {
    return -1;
  }
  if (result == RPMB_RESULT_OK || result == RPMB_RESULT_COUNTER_EXPIRED) {
    *status = RPMB_KEY_STATUS_PROGRAMMED;
    return 0;
  }

  if (result == RPMB_RESULT_NO_AUTH_KEY) {
    *status = RPMB_KEY_STATUS_NOT_PROGRAMMED;
    return 0;
  }

  return -1;
}

static void prepare_mmc_command(struct mmc_ioc_cmd* command,
                                uint32_t opcode,
                                int write_flag,
                                rpmb_frame_t* frame) {
  command->opcode = opcode;
  command->write_flag = write_flag;
  command->blksz = RPMB_FRAME_SIZE;
  command->blocks = 1;
  command->flags = RPMB_MMC_FLAGS;
  mmc_ioc_cmd_set_data((*command), frame);
}

int rpmb_get_key_status(const char* device_path, rpmb_key_status_t* status) {
  struct mmc_ioc_multi_cmd* commands;
  rpmb_frame_t request;
  rpmb_frame_t response;
  int device_fd;
  int result = -1;

  if (status == NULL) {
    return -1;
  }
  *status = RPMB_KEY_STATUS_UNKNOWN;

  if (device_path == NULL) {
    return -1;
  }

  device_fd = open(device_path, O_RDWR);
  if (device_fd < 0) {
    return -1;
  }

  commands = calloc(1, sizeof(*commands) + RPMB_COMMAND_COUNT * sizeof(commands->cmds[0]));
  if (commands == NULL) {
    close(device_fd);
    return -1;
  }

  memset(&request, 0, sizeof(request));
  memset(&response, 0, sizeof(response));
  put_be16(&request.bytes[RPMB_REQ_RESP_OFFSET], RPMB_REQUEST_READ_COUNTER);

  commands->num_of_cmds = RPMB_COMMAND_COUNT;
  prepare_mmc_command(&commands->cmds[0], MMC_WRITE_MULTIPLE_BLOCK, 1, &request);
  prepare_mmc_command(&commands->cmds[1], MMC_READ_MULTIPLE_BLOCK, 0, &response);

  if (ioctl(device_fd, MMC_IOC_MULTI_CMD, commands) == 0) {
    result = rpmb_parse_key_status(&response, status);
  }

  free(commands);
  close(device_fd);
  return result;
}

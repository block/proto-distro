#ifndef TCP_SERVER_H
#define TCP_SERVER_H

#include "libminer_optee_host_common/common.h"

#include <netinet/in.h>

/* Constants */
#define PROVISIONING_PORT 5024

/* Client connection context */
typedef struct client_ctx {
  int client_socket;
  struct sockaddr_in client_addr;
} client_ctx_t;

/* Function prototypes */
int start_server(void);

#endif /* TCP_SERVER_H */

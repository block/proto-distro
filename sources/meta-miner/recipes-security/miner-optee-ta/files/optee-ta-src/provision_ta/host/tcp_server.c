#include "tcp_server.h"
#include "provision_commands.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <arpa/inet.h>
#include <sys/socket.h>
#include <sys/select.h>
#include <pthread.h>
#include <signal.h>
#include <errno.h>
#include <fcntl.h>

/* Only the main thread receives shutdown signals, while inside pselect. */
static volatile sig_atomic_t stop_requested;

static void* handle_client(void* arg);

static void signal_handler(int sig) {
  (void)sig;
  stop_requested = 1;
}

int start_server(void) {
  struct sockaddr_in server_addr;
  int server_socket = -1;
  int result = -1;
  int opt = 1;
  sigset_t shutdown_signals, old_mask, wait_mask;
  struct sigaction action = {0};

  sigemptyset(&shutdown_signals);
  sigaddset(&shutdown_signals, SIGINT);
  sigaddset(&shutdown_signals, SIGTERM);
  int error = pthread_sigmask(SIG_BLOCK, &shutdown_signals, &old_mask);
  if (error != 0) {
    fprintf(stderr, "pthread_sigmask failed: %s\n", strerror(error));
    return -1;
  }
  wait_mask = old_mask;
  sigdelset(&wait_mask, SIGINT);
  sigdelset(&wait_mask, SIGTERM);
  stop_requested = 0;
  action.sa_handler = signal_handler;
  sigemptyset(&action.sa_mask);
  if (sigaction(SIGINT, &action, NULL) < 0 || sigaction(SIGTERM, &action, NULL) < 0) {
    perror("sigaction failed");
    goto cleanup;
  }

  server_socket = socket(AF_INET, SOCK_STREAM, 0);
  if (server_socket < 0) {
    perror("socket creation failed");
    goto cleanup;
  }
  if (server_socket >= FD_SETSIZE) {
    fprintf(stderr, "Server socket exceeds pselect descriptor limit\n");
    goto cleanup;
  }
  if (setsockopt(server_socket, SOL_SOCKET, SO_REUSEADDR, &opt, sizeof(opt)) < 0) {
    perror("setsockopt failed");
    goto cleanup;
  }
  if (fcntl(server_socket, F_SETFL, O_NONBLOCK) < 0) {
    perror("fcntl failed");
    goto cleanup;
  }

  memset(&server_addr, 0, sizeof(server_addr));
  server_addr.sin_family = AF_INET;
  server_addr.sin_addr.s_addr = INADDR_ANY;
  server_addr.sin_port = htons(PROVISIONING_PORT);
  if (bind(server_socket, (struct sockaddr*)&server_addr, sizeof(server_addr)) < 0) {
    perror("bind failed");
    goto cleanup;
  }
  if (listen(server_socket, 5) < 0) {
    perror("listen failed");
    goto cleanup;
  }

  printf("Provisioning server listening on port %d\n", PROVISIONING_PORT);
  while (!stop_requested) {
    fd_set readable;
    FD_ZERO(&readable);
    FD_SET(server_socket, &readable);
    int ready = pselect(server_socket + 1, &readable, NULL, NULL, NULL, &wait_mask);
    if (ready < 0) {
      if (errno == EINTR) {
        continue;
      }
      perror("pselect failed");
      goto cleanup;
    }
    if (stop_requested) {
      break;
    }

    struct sockaddr_in client_addr;
    socklen_t client_len = sizeof(client_addr);
    int client_socket = accept(server_socket, (struct sockaddr*)&client_addr, &client_len);
    if (client_socket < 0) {
      if (errno != EINTR && errno != EAGAIN && errno != EWOULDBLOCK) {
        perror("accept failed");
      }
      continue;
    }
    /* Some platforms inherit O_NONBLOCK from the listening socket. */
    if (fcntl(client_socket, F_SETFL, 0) < 0) {
      perror("fcntl failed");
      close(client_socket);
      continue;
    }
    /* <sys/socket.h> provides SO_NOSIGPIPE on macOS. This socket option
     * suppresses SIGPIPE for writes to this client, so a broken connection
     * produces an EPIPE error instead of terminating the process.
     * Linux uses the per-call MSG_NOSIGNAL flag in send() instead. */
#ifdef SO_NOSIGPIPE
    if (setsockopt(client_socket, SOL_SOCKET, SO_NOSIGPIPE, &opt, sizeof(opt)) < 0) {
      perror("setsockopt failed");
      close(client_socket);
      continue;
    }
#endif

    client_ctx_t* client = malloc(sizeof(*client));
    if (!client) {
      perror("malloc failed");
      close(client_socket);
      continue;
    }
    client->client_socket = client_socket;
    client->client_addr = client_addr;

    /* Workers inherit blocked SIGINT/SIGTERM; only pselect unmasks them. */
    pthread_t thread;
    error = pthread_create(&thread, NULL, handle_client, client);
    if (error != 0) {
      fprintf(stderr, "pthread_create failed: %s\n", strerror(error));
      close(client_socket);
      free(client);
      continue;
    }
    pthread_detach(thread);
  }
  result = 0;

cleanup:
  if (server_socket >= 0) {
    close(server_socket);
  }
  pthread_sigmask(SIG_SETMASK, &old_mask, NULL);
  return result;
}

static void* handle_client(void* arg) {
  client_ctx_t* client = arg;
  uint8_t command_buffer[MAX_COMMAND_SIZE];
  uint8_t response_buffer[MAX_COMMAND_SIZE];

  while (1) {
    size_t command_size = CMD_LEN_FIELD_LEN;
    size_t received_size = 0;
    /* Read the prefix first, then extend the read to exactly one frame. */
    while (received_size < command_size) {
      ssize_t received = recv(client->client_socket, command_buffer + received_size,
                              command_size - received_size, 0);
      if (received < 0 && errno == EINTR) {
        continue;
      }
      if (received <= 0) {
        goto cleanup;
      }
      received_size += (size_t)received;
      if (received_size == CMD_LEN_FIELD_LEN) {
        uint32_t netlen;
        memcpy(&netlen, command_buffer, sizeof(netlen));
        size_t body_size = ntohl(netlen);
        if (body_size < CMD_ID_LEN || body_size > MAX_COMMAND_SIZE - CMD_LEN_FIELD_LEN) {
          goto cleanup;
        }
        command_size += body_size;
      }
    }

    memset(response_buffer, 0, sizeof(response_buffer));
    size_t response_size = handle_network_command(command_buffer, command_size,
                                                   response_buffer, sizeof(response_buffer));
    if (response_size > sizeof(response_buffer)) {
      break;
    }
    size_t sent_size = 0;
    while (sent_size < response_size) {
      /* <sys/socket.h> provides MSG_NOSIGNAL on Linux. It suppresses SIGPIPE,
       * whose default action terminates the process, when sending to a broken
       * connection. send() returns -1 with errno == EPIPE instead.
       * macOS uses SO_NOSIGPIPE instead of this per-call flag. */
#ifdef MSG_NOSIGNAL
      ssize_t sent = send(client->client_socket, response_buffer + sent_size,
                          response_size - sent_size, MSG_NOSIGNAL);
#else
      /* SO_NOSIGPIPE is set on accepted sockets on platforms without MSG_NOSIGNAL. */
      ssize_t sent = send(client->client_socket, response_buffer + sent_size,
                          response_size - sent_size, 0);
#endif
      if (sent < 0 && errno == EINTR) {
        continue;
      }
      if (sent <= 0) {
        goto cleanup;
      }
      sent_size += (size_t)sent;
    }
  }

cleanup:
  close(client->client_socket);
  free(client);
  return NULL;
}

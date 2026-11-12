#ifndef PROVISION_COMMANDS_H
#define PROVISION_COMMANDS_H

size_t handle_network_command(const uint8_t* command_buffer, size_t command_size, uint8_t* response_buffer,
                            size_t response_size);

#endif /* PROVISION_COMMANDS_H */

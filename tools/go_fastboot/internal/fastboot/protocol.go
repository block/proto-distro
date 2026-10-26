// Package fastboot provides a native Go implementation of the fastboot protocol.
package fastboot

import (
	"context"
	"fmt"
	"strconv"

	"proto-distro/tools/go_fastboot/internal/fastboot/udp"
)

// ResponseStatus represents the fastboot response status.
type ResponseStatus string

const (
	StatusOkay ResponseStatus = "OKAY" // Command succeeded
	StatusFail ResponseStatus = "FAIL" // Command failed
	StatusData ResponseStatus = "DATA" // Ready for data transfer
	StatusInfo ResponseStatus = "INFO" // Informational message (more to come)
	StatusText ResponseStatus = "TEXT" // Unformatted text (more to come)
)

// Response represents a parsed fastboot response.
type Response struct {
	Status  ResponseStatus
	Message string
}

// Protocol handles fastboot command/response communication.
type Protocol struct {
	transport *udp.Transport
}

// NewProtocol creates a protocol handler using the given transport.
func NewProtocol(transport *udp.Transport) *Protocol {
	return &Protocol{transport: transport}
}

// parseResponse extracts status and message from raw response data.
func parseResponse(data []byte) (*Response, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("response too short: %d bytes", len(data))
	}

	status := ResponseStatus(data[:4])
	message := ""
	if len(data) > 4 {
		message = string(data[4:])
	}

	switch status {
	case StatusOkay, StatusFail, StatusData, StatusInfo, StatusText:
		return &Response{Status: status, Message: message}, nil
	default:
		return nil, fmt.Errorf("unknown response status: %q", status)
	}
}

// Command sends a fastboot command and collects INFO/TEXT messages until final response.
func (p *Protocol) Command(ctx context.Context, cmd string) (*Response, []string, error) {
	data, err := p.transport.Send(ctx, []byte(cmd))
	if err != nil {
		return nil, nil, fmt.Errorf("sending command %q: %w", cmd, err)
	}

	// If we got an empty ACK, request the actual response
	if len(data) == 0 {
		data, err = p.transport.Receive(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("receiving response: %w", err)
		}
	}

	resp, err := parseResponse(data)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing response: %w", err)
	}

	// Collect INFO/TEXT messages
	var infoMessages []string
	for resp.Status == StatusInfo || resp.Status == StatusText {
		infoMessages = append(infoMessages, resp.Message)

		// Request next response (empty packet)
		data, err = p.transport.Receive(ctx)
		if err != nil {
			return nil, infoMessages, fmt.Errorf("getting next response: %w", err)
		}

		resp, err = parseResponse(data)
		if err != nil {
			return nil, infoMessages, fmt.Errorf("parsing response: %w", err)
		}
	}

	return resp, infoMessages, nil
}

// expectOkay sends a command and returns an error if the response isn't OKAY.
func (p *Protocol) expectOkay(ctx context.Context, cmd, name string) error {
	resp, _, err := p.Command(ctx, cmd)
	if err != nil {
		return err
	}
	if resp.Status == StatusFail {
		return fmt.Errorf("%s failed: %s", name, resp.Message)
	}
	if resp.Status != StatusOkay {
		return fmt.Errorf("unexpected response: %s %s", resp.Status, resp.Message)
	}
	return nil
}

// GetVar retrieves a fastboot variable value.
func (p *Protocol) GetVar(ctx context.Context, name string) (string, error) {
	resp, _, err := p.Command(ctx, "getvar:"+name)
	if err != nil {
		return "", err
	}
	if resp.Status == StatusFail {
		return "", fmt.Errorf("getvar %s failed: %s", name, resp.Message)
	}
	if resp.Status != StatusOkay {
		return "", fmt.Errorf("unexpected response: %s %s", resp.Status, resp.Message)
	}
	return resp.Message, nil
}

// Download sends data to the device's memory for subsequent flash.
func (p *Protocol) Download(ctx context.Context, data []byte, progress func(sent, total int)) error {
	cmd := fmt.Sprintf("download:%08x", len(data))
	resp, _, err := p.Command(ctx, cmd)
	if err != nil {
		return fmt.Errorf("download command: %w", err)
	}

	if resp.Status == StatusFail {
		return fmt.Errorf("download rejected: %s", resp.Message)
	}

	if resp.Status != StatusData {
		return fmt.Errorf("unexpected response: %s %s", resp.Status, resp.Message)
	}

	// Parse expected size from DATA response
	expectedSize, err := strconv.ParseInt(resp.Message, 16, 64)
	if err != nil {
		return fmt.Errorf("parsing data size: %w", err)
	}

	if int(expectedSize) != len(data) {
		return fmt.Errorf("size mismatch: device expects %d, we have %d", expectedSize, len(data))
	}

	// Send the actual data
	if err := p.transport.SendRaw(ctx, data, progress); err != nil {
		return fmt.Errorf("sending data: %w", err)
	}

	// Get final response after data transfer
	finalData, err := p.transport.Send(ctx, nil)
	if err != nil {
		return fmt.Errorf("getting final response: %w", err)
	}

	finalResp, err := parseResponse(finalData)
	if err != nil {
		return fmt.Errorf("parsing final response: %w", err)
	}

	if finalResp.Status == StatusFail {
		return fmt.Errorf("download failed: %s", finalResp.Message)
	}

	if finalResp.Status != StatusOkay {
		return fmt.Errorf("unexpected final response: %s %s", finalResp.Status, finalResp.Message)
	}

	return nil
}

// Flash writes previously downloaded data to the named partition.
func (p *Protocol) Flash(ctx context.Context, partition string) error {
	return p.expectOkay(ctx, "flash:"+partition, "flash "+partition)
}

// Erase erases the named partition.
func (p *Protocol) Erase(ctx context.Context, partition string) error {
	return p.expectOkay(ctx, "erase:"+partition, "erase "+partition)
}

// Reboot reboots the device.
func (p *Protocol) Reboot(ctx context.Context) error {
	return p.expectOkay(ctx, "reboot", "reboot")
}

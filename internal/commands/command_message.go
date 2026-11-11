package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/exitcode"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/output"
	"github.com/portpowered/portos-cli/internal/transport"
	"go.uber.org/zap"
)

// MessageCommand handles the `port message <command> <endpoint-id> [args...]` command
type MessageCommand struct {
	apiClient      apiclient.Client
	resolver       transport.Resolver
	formatter      output.Formatter
	logger         *logging.LoggerRef
	configProvider *config.ManagerProvider
}

// NewMessageCommand creates a new message command
func NewMessageCommand(apiClient apiclient.Client, resolver transport.Resolver, formatter output.Formatter, logger *logging.LoggerRef, configProvider *config.ManagerProvider) *MessageCommand {
	return &MessageCommand{
		apiClient:      apiClient,
		resolver:       resolver,
		formatter:      formatter,
		logger:         logger,
		configProvider: configProvider,
	}
}

// RunParams contains parameters for the Run command
type RunParams struct {
	IdempotencyKey string
	EndpointID     string
	Command        string
	Args           []string
	// Wait asks the server to hold the response until every delivery is final (SYNC).
	Wait bool
	// Body is the whole message body as a JSON object; it cannot be combined with Args.
	Body string
	// BodySet reports that --body was given, even as an empty string.
	BodySet bool
}

// hasBody reports whether the caller supplied --body, including an explicit empty value.
func (p RunParams) hasBody() bool { return p.BodySet || p.Body != "" }

// Run executes the message command
// Args: command, endpointID, and optional args
func (c *MessageCommand) Run(ctx context.Context, params RunParams) error {
	if params.EndpointID == "" {
		return fmt.Errorf("endpoint ID is required")
	}

	if params.Command == "" {
		return fmt.Errorf("command is required")
	}

	request, err := c.buildRequest(params)
	if err != nil {
		return err
	}

	// Send message
	c.logger.Logger().Info("sending message",
		zap.String("endpoint_id", request.Target.Id),
		zap.String("original_id", params.EndpointID),
		zap.String("namespace", request.Message.Header.Namespace),
		zap.String("name", request.Message.Header.Name),
		zap.String("version", request.Message.Header.Version),
	)

	sender, err := c.resolver.For(ctx, request.Target.Id, request.Message.Header.Namespace, request.Message.Header.Name)
	if err != nil {
		return err
	}
	response, err := sender.Send(ctx, request, params.IdempotencyKey)
	if err != nil {
		c.logger.Logger().Error("failed to send message", zap.Error(err))
		var setupErr *apiclient.SetupError
		if errors.As(err, &setupErr) {
			return err
		}
		return fmt.Errorf("failed to send message: %w", err)
	}

	return c.printResult(params, response)
}

// buildRequest parses the command and arguments into a message creation request.
func (c *MessageCommand) buildRequest(params RunParams) (*api.CreateMessageRequest, error) {
	endpointID := params.EndpointID

	// Parse command to extract namespace, name, and version
	// Command format can be:
	// 1. Simple name: "turn-on" -> will try to infer namespace
	// 2. Full path: "port1/systems/zero/capability-interfaces/power/turn-on"
	// 3. Namespace/name: "power/turn-on"
	namespace, name, version, err := c.parseCommand(params.Command)
	if err != nil {
		return nil, fmt.Errorf("failed to parse command: %w", err)
	}

	if params.hasBody() && len(params.Args) > 0 {
		return nil, exitcode.NewInvalidArgument(errors.New("--body cannot be combined with key=value or JSON arguments; put every field in --body"))
	}

	// Parse args into message body
	var body map[string]interface{}
	if params.hasBody() {
		if err := json.Unmarshal([]byte(params.Body), &body); err != nil || body == nil {
			return nil, exitcode.NewInvalidArgument(errors.New("--body must be a JSON object"))
		}
	} else {
		body, err = c.parseArgs(params.Args)
		if err != nil {
			return nil, fmt.Errorf("failed to parse arguments: %w", err)
		}
	}

	// For speak: if body is not already a proper input payload, treat args as raw text to speak
	if namespace == "port1/systems/zero/capability-interfaces/speech-synthesizer" && name == "speak" {
		body = c.normalizeSpeakBody(params.Args, body)
	}

	// Construct message
	messageType := fmt.Sprintf("%s/%s", namespace, name)
	msg := api.Message{
		Type: &messageType,
		Header: &api.MessageHeader{
			Namespace: namespace,
			Name:      name,
			Version:   version,
		},
		Body: body,
	}

	// Construct request
	request := &api.CreateMessageRequest{
		Target: api.CreateMessageTarget{
			Type: api.EndpointTarget,
			Id:   endpointID,
		},
		Message: msg,
		// Default to FIRE_FORGET if not specified
		ResponseType: nil,
	}
	if params.Wait {
		sync := api.SyncResponseType
		request.ResponseType = &sync
	}
	return request, nil
}

// printResult reports the sent message in JSON or human-readable form.
func (c *MessageCommand) printResult(params RunParams, response *api.MessageRequest) error {
	if response == nil {
		return fmt.Errorf("message request was not returned")
	}
	var err error
	if c.formatter.IsJSON() {
		err = c.formatter.Print(response)
	} else {
		err = c.formatter.Print(fmt.Sprintf("Message request %s for %s -> %s: %s (%d successful dispatches, %d failed dispatches)", response.Id, params.Command, params.EndpointID, response.Status, len(response.Results), len(response.Errors)))
	}
	if err != nil {
		return err
	}
	return messageRequestFailure(response)
}

func messageRequestFailure(response *api.MessageRequest) error {
	if response != nil && (response.Status == "FAILED" || response.Status == "PARTIALLY_SUCCEEDED") {
		return &apiclient.MessageRequestFailed{Request: response}
	}
	return nil
}

// parseCommand parses a command string into namespace, name, and version
// Supports multiple formats:
// - "turn-on" -> tries to infer namespace from common patterns
// - "power/turn-on" -> namespace/name
// - "port1/systems/zero/capability-interfaces/power/turn-on" -> full path
func (c *MessageCommand) parseCommand(cmd string) (namespace, name, version string, err error) {
	// Default version
	version = "1.0"

	// Check if it's a full path
	if strings.Contains(cmd, "/") {
		parts := strings.Split(cmd, "/")
		if len(parts) >= 2 {
			// Check if it's a full namespace path
			if strings.HasPrefix(cmd, "port1/systems/zero/capability-interfaces/") {
				// Full path: port1/systems/zero/capability-interfaces/power/turn-on
				namespaceParts := parts[:len(parts)-1]
				namespace = strings.Join(namespaceParts, "/")
				name = parts[len(parts)-1]
				return namespace, name, version, nil
			} else {
				// Simple namespace/name format: power/turn-on
				namespace = parts[0]
				name = parts[1]
				// Construct full namespace
				namespace = fmt.Sprintf("port1/systems/zero/capability-interfaces/%s", namespace)
				return namespace, name, version, nil
			}
		}
	}

	// Simple command name - try to infer namespace from common patterns
	// This is a basic implementation that can be extended
	name = cmd

	// Try to infer namespace from command name patterns
	// This is a heuristic approach - in production, you might want to query
	// the endpoint to see what interfaces it supports
	switch {
	case strings.HasPrefix(cmd, "turn-on") || strings.HasPrefix(cmd, "turn-off"):
		namespace = "port1/systems/zero/capability-interfaces/power"
	case strings.HasPrefix(cmd, "set-brightness"):
		namespace = "port1/systems/zero/capability-interfaces/brightness"
	case strings.HasPrefix(cmd, "play") || strings.HasPrefix(cmd, "pause") ||
		strings.HasPrefix(cmd, "stop") || strings.HasPrefix(cmd, "next") ||
		strings.HasPrefix(cmd, "previous"):
		namespace = "port1/systems/zero/capability-interfaces/media-controls"
	case cmd == "unlock" || cmd == "lock":
		// lock interface: message name is "unlock" for unlock; "lock" for lock if present
		namespace = "port1/systems/zero/capability-interfaces/lock"
	case strings.HasPrefix(cmd, "set-target-temperature"):
		namespace = "port1/systems/zero/capability-interfaces/thermostat"
	case strings.HasPrefix(cmd, "set-thermostat-mode"):
		namespace = "port1/systems/zero/capability-interfaces/thermostat"
	case strings.HasPrefix(cmd, "set-volume"):
		namespace = "port1/systems/zero/capability-interfaces/speaker"
	case cmd == "speak":
		namespace = "port1/systems/zero/capability-interfaces/speech-synthesizer"
	default:
		// Default to power interface for unknown commands
		namespace = "port1/systems/zero/capability-interfaces/power"
		c.logger.Logger().Warn("unknown command, defaulting to power interface",
			zap.String("command", cmd),
		)
	}

	return namespace, name, version, nil
}

// normalizeSpeakBody converts raw text args into the speech-synthesizer speak payload.
// If body already has an "input" object with "ssml", body is returned unchanged.
// Otherwise args are joined as raw text and wrapped in SSML: <speak>...</speak>.
func (c *MessageCommand) normalizeSpeakBody(args []string, body map[string]interface{}) map[string]interface{} {
	if input, ok := body["input"]; ok {
		if inputMap, ok := input.(map[string]interface{}); ok && inputMap["ssml"] != nil {
			return body
		}
	}
	raw := strings.TrimSpace(strings.Join(args, " "))
	if raw == "" {
		return body
	}
	// Escape SSML/XML specials so raw text is safe inside <speak>
	raw = strings.ReplaceAll(raw, "&", "&amp;")
	raw = strings.ReplaceAll(raw, "<", "&lt;")
	raw = strings.ReplaceAll(raw, ">", "&gt;")
	ssml := "<speak>" + raw + "</speak>"
	return map[string]interface{}{
		"input": map[string]interface{}{
			"type": "SSML",
			"ssml": ssml,
		},
	}
}

// parseArgs parses command-line arguments into a message body
// Supports:
// - JSON string: '{"power": true}'
// - Key-value pairs: power=true, brightness=50
// - Empty: {}
func (c *MessageCommand) parseArgs(args []string) (map[string]interface{}, error) {
	body := make(map[string]interface{})

	if len(args) == 0 {
		return body, nil
	}

	// If there's a single argument that looks like JSON, try to parse it
	if len(args) == 1 && (strings.HasPrefix(args[0], "{") || strings.HasPrefix(args[0], "[")) {
		var jsonBody map[string]interface{}
		if err := json.Unmarshal([]byte(args[0]), &jsonBody); err == nil {
			return jsonBody, nil
		}
		// If JSON parsing fails, fall through to key-value parsing
	}

	// Parse as key-value pairs: key=value
	for _, arg := range args {
		if strings.Contains(arg, "=") {
			parts := strings.SplitN(arg, "=", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])

				// Try to parse as number or boolean
				if value == "true" {
					body[key] = true
				} else if value == "false" {
					body[key] = false
				} else if num, err := json.Number(value).Int64(); err == nil {
					body[key] = num
				} else if num, err := json.Number(value).Float64(); err == nil {
					body[key] = num
				} else {
					// Remove quotes if present
					value = strings.Trim(value, "\"'")
					body[key] = value
				}
			}
		} else {
			// Single value without =, treat as boolean true
			body[arg] = true
		}
	}

	return body, nil
}

// ListCommandsParams contains parameters for the ListCommands command
type ListCommandsParams struct {
	EndpointID string
}

// ListCommandsResult represents the result of listing available commands for an endpoint
type ListCommandsResult struct {
	EndpointID string                  `json:"endpoint_id"`
	Interfaces []ListCommandsInterface `json:"interfaces"`
}

// ListCommandsInterface represents an interface and its available commands
type ListCommandsInterface struct {
	Name     string   `json:"name"`
	Commands []string `json:"commands"`
}

// ListCommands queries an endpoint and lists its available message commands grouped by interface
func (c *MessageCommand) ListCommands(ctx context.Context, params ListCommandsParams) error {
	if params.EndpointID == "" {
		return fmt.Errorf("endpoint ID is required")
	}

	endpointID := params.EndpointID

	// Query the API for the endpoint
	response, err := c.apiClient.Endpoints().GetByID(ctx, endpointID)
	if err != nil {
		return fmt.Errorf("failed to get endpoint: %w", err)
	}

	if len(response.Results) == 0 {
		return fmt.Errorf("endpoint not found: %s", params.EndpointID)
	}

	ep := response.Results[0]

	// Build the result from the endpoint's interfaces
	result := ListCommandsResult{
		EndpointID: params.EndpointID,
		Interfaces: make([]ListCommandsInterface, 0, len(ep.Interfaces)),
	}

	for _, iface := range ep.Interfaces {
		// Strip prefix from interface name
		interfaceName := iface.Name
		if lastSlash := strings.LastIndex(interfaceName, "/"); lastSlash >= 0 {
			interfaceName = interfaceName[lastSlash+1:]
		}

		var cmds []string
		if iface.Messages != nil {
			for _, msg := range *iface.Messages {
				cmds = append(cmds, msg.Name)
			}
		}

		if len(cmds) > 0 {
			result.Interfaces = append(result.Interfaces, ListCommandsInterface{
				Name:     interfaceName,
				Commands: cmds,
			})
		}
	}

	if c.formatter.IsJSON() {
		return c.formatter.Print(result)
	}

	// Human-readable output
	return c.printListCommandsHumanReadable(result)
}

// printListCommandsHumanReadable prints the list-commands result in human-readable format
func (c *MessageCommand) printListCommandsHumanReadable(result ListCommandsResult) error {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Available commands for %s:\n\n", result.EndpointID))

	if len(result.Interfaces) == 0 {
		sb.WriteString("  No message commands found for this endpoint.\n")
		return c.formatter.Print(sb.String())
	}

	for _, iface := range result.Interfaces {
		sb.WriteString(fmt.Sprintf("  %s:\n", iface.Name))
		for _, cmd := range iface.Commands {
			sb.WriteString(fmt.Sprintf("    %s\n", cmd))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Tip: Send a command with 'port message <command> " + result.EndpointID + "'\n")

	return c.formatter.Print(sb.String())
}

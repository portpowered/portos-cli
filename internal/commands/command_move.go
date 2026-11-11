package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/exitcode"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/movegrammar"
	"github.com/portpowered/portos-cli/internal/output"
	"github.com/portpowered/portos-cli/internal/transport"
	"go.uber.org/zap"
)

const (
	rcNamespace = "port1/systems/zero/capability-interfaces/remote-control"
	rcVersion   = "1.0"

	// endSessionTimeout bounds the cleanup end-session sent after a failure.
	endSessionTimeout = 10 * time.Second
)

// MoveCommand handles the `port message move <endpoint-id> <instructions...>` command.
type MoveCommand struct {
	resolver       transport.Resolver
	formatter      output.Formatter
	logger         *logging.LoggerRef
	configProvider *config.ManagerProvider
}

// NewMoveCommand creates a new move command.
func NewMoveCommand(resolver transport.Resolver, formatter output.Formatter, logger *logging.LoggerRef, configProvider *config.ManagerProvider) *MoveCommand {
	return &MoveCommand{
		resolver:       resolver,
		formatter:      formatter,
		logger:         logger,
		configProvider: configProvider,
	}
}

// MoveParams contains parameters for the move command.
type MoveParams struct {
	EndpointID   string
	Instructions []string // Raw grammar strings, e.g. ["forward 1m", "rotate 90"]
}

// MoveResult is what a move run measured: one reply per move the device carried out.
type MoveResult struct {
	EndpointID string                                          `json:"endpoint_id"`
	Replies    []api.RemoteControlInterfaceMoveResponseMessage `json:"replies"`
}

// Run executes the move command. It parses the grammar strings into
// instructions, then drives the endpoint through one remote-control session:
// start-session, one synchronous move per instruction, and end-session. Each
// reply carries the measured distance and turn. The run stops at the first
// move that is not carried out, and the session is ended unless it never started.
// With --json the MoveResult is always printed, with the replies measured before any failure.
// A move or start-session whose request is not SUCCEEDED, or whose reply lacks a
// measurement, is a plain error: the outcome is unknown.
func (c *MoveCommand) Run(ctx context.Context, params MoveParams) error {
	if params.EndpointID == "" {
		return fmt.Errorf("endpoint ID is required")
	}
	if len(params.Instructions) == 0 {
		return fmt.Errorf("at least one movement instruction is required")
	}

	instructions, err := movegrammar.Parse(joinInstructions(params.Instructions), movegrammar.DefaultBounds)
	if err != nil {
		return exitcode.NewInvalidArgument(fmt.Errorf("invalid move grammar: %w", err))
	}

	c.logger.Logger().Info("executing move command",
		zap.String("endpoint_id", params.EndpointID),
		zap.Int("instruction_count", len(instructions)),
	)

	result, err := c.drive(ctx, params.EndpointID, instructions)
	if printErr := c.printResult(result); printErr != nil && err == nil {
		err = printErr
	}
	return err
}

// drive runs the session and returns the replies measured so far with the first error.
func (c *MoveCommand) drive(ctx context.Context, endpointID string, instructions []movegrammar.Instruction) (MoveResult, error) {
	result := MoveResult{EndpointID: endpointID, Replies: []api.RemoteControlInterfaceMoveResponseMessage{}}

	sender, err := c.resolver.For(ctx, endpointID, rcNamespace, "move")
	if err != nil {
		return result, err
	}
	if transport.Held(sender) {
		// A motion hold owns the session: send only the moves.
		err := c.runMoves(ctx, sender, endpointID, instructions, &result)
		return result, err
	}

	if _, err := c.sendMessage(ctx, sender, endpointID, "start-session", nil); err != nil {
		return result, err
	}
	err = c.runMoves(ctx, sender, endpointID, instructions, &result)

	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), endSessionTimeout)
	defer cancel()
	if _, endErr := c.sendMessage(cleanup, sender, endpointID, "end-session", nil); endErr != nil {
		if err == nil {
			err = endErr
		} else {
			c.logger.Logger().Warn("end-session failed after a failed move", zap.Error(endErr))
		}
	}
	return result, err
}

func (c *MoveCommand) runMoves(ctx context.Context, sender transport.Sender, endpointID string, instructions []movegrammar.Instruction, result *MoveResult) error {
	for i, inst := range instructions {
		body := map[string]interface{}{
			"velocity": float32(inst.Velocity),
			"omega":    float32(inst.Omega),
			"duration": inst.Duration,
			"seqNum":   i + 1,
		}
		response, err := c.sendMessage(ctx, sender, endpointID, "move", body)
		if err != nil {
			return err
		}
		reply, err := decodeMoveReply(response)
		if err != nil {
			return fmt.Errorf("move %d (request %s, status %s): %w; the outcome is unknown", i+1, response.Id, response.Status, err)
		}
		result.Replies = append(result.Replies, reply)
	}
	return nil
}

// decodeMoveReply reads the measured move-response out of a synchronous move's results.
// Every measurement must be present: a missing one is an error, never a zero.
func decodeMoveReply(response *api.MessageRequest) (api.RemoteControlInterfaceMoveResponseMessage, error) {
	var reply api.RemoteControlInterfaceMoveResponseMessage
	for _, r := range response.Results {
		if r.Message == nil || r.Message.Header == nil || r.Message.Header.Name != api.INTERFACE_PORTOS_REMOTE_CONTROL_MOVE_RESPONSE {
			continue
		}
		for _, field := range []string{"distanceMetres", "turnedDegrees"} {
			if _, ok := r.Message.Body[field]; !ok {
				return reply, fmt.Errorf("the move reply has no %s", field)
			}
		}
		encoded, err := json.Marshal(r.Message.Body)
		if err != nil {
			return reply, err
		}
		if err := json.Unmarshal(encoded, &reply); err != nil {
			return reply, fmt.Errorf("reading the move reply: %w", err)
		}
		return reply, nil
	}
	return reply, errors.New("the move returned no measured reply")
}

func (c *MoveCommand) printResult(result MoveResult) error {
	if c.formatter.IsJSON() {
		return c.formatter.Print(result)
	}
	for i, reply := range result.Replies {
		line := fmt.Sprintf("move %d: distanceMetres %g turnedDegrees %g", i+1, reply.DistanceMetres, reply.TurnedDegrees)
		if err := c.formatter.Print(line); err != nil {
			return err
		}
	}
	return nil
}

// sendMessage dispatches one remote-control message to the endpoint and waits for its outcome.
func (c *MoveCommand) sendMessage(ctx context.Context, sender transport.Sender, endpointID, name string, body map[string]interface{}) (*api.MessageRequest, error) {
	if body == nil {
		// The server validates the body as an object, so a bodyless command sends {} (nil marshals to null).
		body = map[string]interface{}{}
	}
	messageType := rcNamespace + "/" + name
	sync := api.SyncResponseType
	request := &api.CreateMessageRequest{
		Target: api.CreateMessageTarget{
			Type: api.EndpointTarget,
			Id:   endpointID,
		},
		Message: api.Message{
			Type: &messageType,
			Header: &api.MessageHeader{
				Namespace: rcNamespace,
				Name:      name,
				Version:   rcVersion,
			},
			Body: body,
		},
		ResponseType: &sync,
	}

	response, err := sender.Send(ctx, request, uuid.NewString())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if err := messageRequestFailure(response); err != nil {
		return response, fmt.Errorf("%s: %w", name, err)
	}
	if response == nil {
		return nil, fmt.Errorf("%s: no message request was returned", name)
	}
	if response.Status != api.SUCCEEDED {
		return response, fmt.Errorf("%s: request %s is %s, not SUCCEEDED; the outcome is unknown", name, response.Id, response.Status)
	}
	return response, nil
}

// joinInstructions combines multiple instruction strings into a single
// comma-separated grammar expression.
func joinInstructions(parts []string) string {
	return strings.Join(parts, ", ")
}

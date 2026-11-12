package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/exitcode"
	"github.com/portpowered/portos-cli/internal/services/holds"
	"github.com/portpowered/portos-cli/internal/services/messages"
	"github.com/portpowered/portos-cli/internal/transport"
)

// StepPTZParams contains parameters for the step-ptz command.
type StepPTZParams struct {
	EndpointID string
	// Direction is LEFT, RIGHT, UP or DOWN, in any case.
	Direction string
}

// StepPTZResult is what step-ptz prints with --json.
type StepPTZResult struct {
	EndpointID string `json:"endpointId"`
	Direction  string `json:"direction"`
}

// stepPTZDirections are the directions camera/step-ptz takes.
var stepPTZDirections = []api.CameraInterfaceStepPtzMessageDirection{
	api.CameraInterfaceStepPtzMessageDirectionLEFT,
	api.CameraInterfaceStepPtzMessageDirectionRIGHT,
	api.CameraInterfaceStepPtzMessageDirectionUP,
	api.CameraInterfaceStepPtzMessageDirectionDOWN,
}

func parseStepDirection(text string) (api.CameraInterfaceStepPtzMessageDirection, error) {
	names := make([]string, len(stepPTZDirections))
	for i, direction := range stepPTZDirections {
		if strings.EqualFold(text, string(direction)) {
			return direction, nil
		}
		names[i] = string(direction)
	}
	return "", exitcode.NewInvalidArgument(fmt.Errorf("direction %q is not one of %s", text, strings.Join(names, ", ")))
}

// StepPTZ moves a camera one pan or tilt step. A step needs the caller's own camera session, which
// only a camera hold has: the host sends the step in the hold's session and fills the session ID,
// so the command takes the hold's lease. Without a live hold behind the lease the step is refused
// with hold_lease_required instead of opening a camera session of its own.
func (c *MoveCommand) StepPTZ(ctx context.Context, params StepPTZParams) error {
	if params.EndpointID == "" {
		return errors.New("endpoint ID is required")
	}
	direction, err := parseStepDirection(params.Direction)
	if err != nil {
		return err
	}
	sender, err := c.resolver.For(ctx, params.EndpointID, api.INTERFACE_PORTOS_CAMERA, api.INTERFACE_PORTOS_CAMERA_STEP_PTZ)
	if err != nil {
		return err
	}
	if !transport.Held(sender) {
		return &apiclient.MessageRequestFailed{Request: messages.Refusal(time.Now(), params.EndpointID, holds.CodeHoldLeaseRequired,
			"step-ptz moves the camera in your own camera session; hold the camera with `port session hold --camera` and pass its lease")}
	}
	messageType := api.INTERFACE_PORTOS_CAMERA + "/" + api.INTERFACE_PORTOS_CAMERA_STEP_PTZ
	sync := api.SyncResponseType
	response, err := sender.Send(ctx, &api.CreateMessageRequest{
		Target: api.CreateMessageTarget{Type: api.EndpointTarget, Id: params.EndpointID},
		Message: api.Message{
			Type:   &messageType,
			Header: &api.MessageHeader{Namespace: api.INTERFACE_PORTOS_CAMERA, Name: api.INTERFACE_PORTOS_CAMERA_STEP_PTZ, Version: "1.0"},
			Body:   map[string]interface{}{"direction": string(direction)},
		},
		ResponseType: &sync,
	}, uuid.NewString())
	if err != nil {
		return fmt.Errorf("step-ptz: %w", err)
	}
	if err := messageRequestFailure(response); err != nil {
		return fmt.Errorf("step-ptz: %w", err)
	}
	if c.formatter.IsJSON() {
		return c.formatter.Print(StepPTZResult{EndpointID: params.EndpointID, Direction: string(direction)})
	}
	return c.formatter.Print(fmt.Sprintf("Stepped %s %s", params.EndpointID, direction))
}

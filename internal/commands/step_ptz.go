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
	// Degrees is about how far to turn, 1 to 360; 0 turns one step and 360 turns to the end.
	Degrees float64
}

// StepPTZResult is what step-ptz prints with --json. Position is the provider's ptz-position
// answer, absent when the provider sent none.
type StepPTZResult struct {
	EndpointID string                                 `json:"endpointId"`
	Direction  string                                 `json:"direction"`
	Degrees    float64                                `json:"degrees,omitempty"`
	Position   *api.CameraInterfacePtzPositionMessage `json:"position,omitempty"`
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

// StepPTZ moves a camera by one pan or tilt step or by about params.Degrees, in the session of the
// camera hold behind the lease (see sendPTZ).
func (c *MoveCommand) StepPTZ(ctx context.Context, params StepPTZParams) error {
	if params.EndpointID == "" {
		return errors.New("endpoint ID is required")
	}
	direction, err := parseStepDirection(params.Direction)
	if err != nil {
		return err
	}
	if params.Degrees != 0 && (params.Degrees < 1 || params.Degrees > 360) {
		return exitcode.NewInvalidArgument(fmt.Errorf("--degrees %g is not between 1 and 360", params.Degrees))
	}
	body := map[string]interface{}{"direction": string(direction)}
	if params.Degrees != 0 {
		body["degrees"] = params.Degrees
	}
	position, err := c.sendPTZ(ctx, params.EndpointID, api.INTERFACE_PORTOS_CAMERA_STEP_PTZ, body)
	if err != nil {
		return err
	}
	result := StepPTZResult{EndpointID: params.EndpointID, Direction: string(direction), Degrees: params.Degrees, Position: position}
	if c.formatter.IsJSON() {
		return c.formatter.Print(result)
	}
	return c.formatter.Print(describeStepPTZ(result))
}

// sendPTZ sends a camera PTZ message (step-ptz, move-ptz or stop-ptz) through the camera hold
// behind the lease and returns the provider's ptz-position answer. PTZ needs the caller's own
// camera session, which only a camera hold has: the host sends the message in the hold's session
// and fills the session ID. Without a live hold behind the lease it is refused with
// hold_lease_required instead of opening a camera session of its own.
func (c *MoveCommand) sendPTZ(ctx context.Context, endpointID, name string, body map[string]any) (*api.CameraInterfacePtzPositionMessage, error) {
	sender, err := c.resolver.For(ctx, endpointID, api.INTERFACE_PORTOS_CAMERA, name)
	if err != nil {
		return nil, err
	}
	if !transport.Held(sender) {
		return nil, &apiclient.MessageRequestFailed{Request: messages.Refusal(time.Now(), endpointID, holds.CodeHoldLeaseRequired,
			name+" turns the camera in your own camera session; hold the camera with `port session hold --camera` and pass its lease")}
	}
	messageType := api.INTERFACE_PORTOS_CAMERA + "/" + name
	sync := api.SyncResponseType
	response, err := sender.Send(ctx, &api.CreateMessageRequest{
		Target: api.CreateMessageTarget{Type: api.EndpointTarget, Id: endpointID},
		Message: api.Message{
			Type:   &messageType,
			Header: &api.MessageHeader{Namespace: api.INTERFACE_PORTOS_CAMERA, Name: name, Version: "1.0"},
			Body:   body,
		},
		ResponseType: &sync,
	}, uuid.NewString())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if err := messageRequestFailure(response); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	position, err := ptzPosition(response)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return position, nil
}

// ptzPosition reads the provider's ptz-position answer from a step's reply, or nil without one.
func ptzPosition(response *api.MessageRequest) (*api.CameraInterfacePtzPositionMessage, error) {
	for _, r := range response.Results {
		if r.Message == nil || r.Message.Header == nil || r.Message.Header.Name != api.INTERFACE_PORTOS_CAMERA_PTZ_POSITION {
			continue
		}
		encoded, err := json.Marshal(r.Message.Body)
		if err != nil {
			return nil, err
		}
		var position api.CameraInterfacePtzPositionMessage
		if err := json.Unmarshal(encoded, &position); err != nil {
			return nil, fmt.Errorf("reading the ptz-position answer: %w", err)
		}
		return &position, nil
	}
	return nil, nil
}

// describeStepPTZ says how far the camera turned and where it points, in words an agent can plan
// the next move from.
func describeStepPTZ(result StepPTZResult) string {
	p := result.Position
	if p == nil {
		return fmt.Sprintf("Stepped %s %s; the camera reported no position", result.EndpointID, result.Direction)
	}
	return fmt.Sprintf("Turned %s %s %.0f°. Now %s.", result.EndpointID, result.Direction, p.MovedDegrees, describePosition(p))
}

// describePosition says where the camera points, in words an agent can plan the next move from,
// and which axes are still turning.
func describePosition(p *api.CameraInterfacePtzPositionMessage) string {
	pan := fmt.Sprintf("pan %.0f° right of the left end (range 0 to %.0f°)", p.PanDegrees, p.PanRangeDegrees)
	if p.PanReference != api.CameraInterfacePtzPositionMessagePanReferenceLEFTEND {
		pan = fmt.Sprintf("pan %s the session start (left end unknown until you turn to an end)", signedDegrees(p.PanDegrees, "right of", "left of"))
	}
	if p.PanMoving {
		pan += ", still turning"
	}
	tilt := fmt.Sprintf("tilt %.0f° up from the bottom end (range 0 to %.0f°)", p.TiltDegrees, p.TiltRangeDegrees)
	if p.TiltReference != api.CameraInterfacePtzPositionMessageTiltReferenceBOTTOMEND {
		tilt = fmt.Sprintf("tilt %s the session start (bottom end unknown until you turn to an end)", signedDegrees(p.TiltDegrees, "above", "below"))
	}
	if p.TiltMoving {
		tilt += ", still turning"
	}
	return pan + ", " + tilt
}

// signedDegrees words a signed angle: "15° right of" or "15° left of".
func signedDegrees(degrees float32, positive, negative string) string {
	if degrees < 0 {
		return fmt.Sprintf("%.0f° %s", -degrees, negative)
	}
	return fmt.Sprintf("%.0f° %s", degrees, positive)
}

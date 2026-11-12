package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/exitcode"
)

// MovePTZParams contains parameters for the move-ptz command.
type MovePTZParams struct {
	EndpointID string
	// Direction is LEFT, RIGHT, UP or DOWN, in any case.
	Direction string
	// Speed is the fraction of full speed, 0.1 to 1; 0 turns at full speed.
	Speed float64
	// Seconds is how long to turn, 0.1 to 60; 0 turns until stop-ptz or the end of the range.
	Seconds float64
}

// MovePTZResult is what move-ptz prints with --json. Position is the provider's ptz-position
// answer, absent when the provider sent none.
type MovePTZResult struct {
	EndpointID string                                 `json:"endpointId"`
	Direction  string                                 `json:"direction"`
	Speed      float64                                `json:"speed,omitempty"`
	Seconds    float64                                `json:"seconds,omitempty"`
	Position   *api.CameraInterfacePtzPositionMessage `json:"position,omitempty"`
}

// StopPTZParams contains parameters for the stop-ptz command.
type StopPTZParams struct {
	EndpointID string
	// Axis is PAN or TILT, in any case; empty stops both.
	Axis string
}

// StopPTZResult is what stop-ptz prints with --json.
type StopPTZResult struct {
	EndpointID string                                 `json:"endpointId"`
	Axis       string                                 `json:"axis,omitempty"`
	Position   *api.CameraInterfacePtzPositionMessage `json:"position,omitempty"`
}

// MovePTZ turns a camera continuously in the session of the camera hold behind the lease (see
// sendPTZ). With Seconds it answers once the camera has stopped; without, it answers as soon as
// the camera turns, and the camera keeps turning until stop-ptz or the end of its range.
func (c *MoveCommand) MovePTZ(ctx context.Context, params MovePTZParams) error {
	if params.EndpointID == "" {
		return errors.New("endpoint ID is required")
	}
	direction, err := parseStepDirection(params.Direction)
	if err != nil {
		return err
	}
	if params.Speed != 0 && (params.Speed < 0.1 || params.Speed > 1) {
		return exitcode.NewInvalidArgument(fmt.Errorf("--speed %g is not between 0.1 and 1", params.Speed))
	}
	if params.Seconds != 0 && (params.Seconds < 0.1 || params.Seconds > 60) {
		return exitcode.NewInvalidArgument(fmt.Errorf("--seconds %g is not between 0.1 and 60", params.Seconds))
	}
	body := map[string]any{"direction": string(direction)}
	if params.Speed != 0 {
		body["speed"] = params.Speed
	}
	if params.Seconds != 0 {
		body["seconds"] = params.Seconds
	}
	position, err := c.sendPTZ(ctx, params.EndpointID, api.INTERFACE_PORTOS_CAMERA_MOVE_PTZ, body)
	if err != nil {
		return err
	}
	result := MovePTZResult{EndpointID: params.EndpointID, Direction: string(direction), Speed: params.Speed, Seconds: params.Seconds, Position: position}
	if c.formatter.IsJSON() {
		return c.formatter.Print(result)
	}
	return c.formatter.Print(describeMovePTZ(result))
}

// StopPTZ stops a continuous turn on one axis, or on both, in the session of the camera hold
// behind the lease (see sendPTZ).
func (c *MoveCommand) StopPTZ(ctx context.Context, params StopPTZParams) error {
	if params.EndpointID == "" {
		return errors.New("endpoint ID is required")
	}
	body := map[string]any{}
	axis := ""
	if params.Axis != "" {
		switch {
		case strings.EqualFold(params.Axis, string(api.PAN)):
			axis = string(api.PAN)
		case strings.EqualFold(params.Axis, string(api.TILT)):
			axis = string(api.TILT)
		default:
			return exitcode.NewInvalidArgument(fmt.Errorf("--axis %q is not one of PAN, TILT", params.Axis))
		}
		body["axis"] = axis
	}
	position, err := c.sendPTZ(ctx, params.EndpointID, api.INTERFACE_PORTOS_CAMERA_STOP_PTZ, body)
	if err != nil {
		return err
	}
	result := StopPTZResult{EndpointID: params.EndpointID, Axis: axis, Position: position}
	if c.formatter.IsJSON() {
		return c.formatter.Print(result)
	}
	if position == nil {
		return c.formatter.Print(fmt.Sprintf("Stopped %s; the camera reported no position", params.EndpointID))
	}
	return c.formatter.Print(fmt.Sprintf("Stopped %s after %.0f°. Now %s.", params.EndpointID, position.MovedDegrees, describePosition(position)))
}

// describeMovePTZ says how far a timed turn went, or that an open turn is under way, and where
// the camera points.
func describeMovePTZ(result MovePTZResult) string {
	p := result.Position
	if p == nil {
		return fmt.Sprintf("Turned %s %s; the camera reported no position", result.EndpointID, result.Direction)
	}
	if result.Seconds == 0 {
		return fmt.Sprintf("Turning %s %s until stop-ptz or the end of its range. Now %s.", result.EndpointID, result.Direction, describePosition(p))
	}
	return fmt.Sprintf("Turned %s %s %.0f° in %gs. Now %s.", result.EndpointID, result.Direction, p.MovedDegrees, result.Seconds, describePosition(p))
}

package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/exitcode"
	"github.com/portpowered/portos-cli/internal/transport"
)

const goToPointVersion = "1.0"

// GoToPointParams contains parameters for the go-to-point command.
type GoToPointParams struct {
	EndpointID string
	// X and Y are the point in metres, in the frame the robot's pose reports.
	X, Y float64
	// Frame is the pose's relativeToGroupRouteId. Under a motion hold it may be
	// empty; the host then takes it from the robot's pose.
	Frame string
}

// GoToPointResult is what a finished trip reports: the arrival pose and the
// operational state the robot stopped in.
type GoToPointResult struct {
	EndpointID       string                             `json:"endpoint_id"`
	OperationalState string                             `json:"operationalState"`
	Pose             *api.SpatialInterfacePoseAttribute `json:"pose,omitempty"`
}

// GoToPoint sends the robot to a point by its own path planning. Under a
// motion hold (a lease) it is one operation: the host ends the remote-control
// session, sends robotic-vacuum-cleaner/go-to-point, waits for the robot to
// arrive, starts the session again and replies with the arrival pose. Without
// a hold it sends the message to Port OS as is, which replies once the robot
// has started travelling; the robot must not be under remote control and the
// frame is required.
func (c *MoveCommand) GoToPoint(ctx context.Context, params GoToPointParams) error {
	if params.EndpointID == "" {
		return errors.New("endpoint ID is required")
	}
	if math.IsNaN(params.X) || math.IsInf(params.X, 0) || math.IsNaN(params.Y) || math.IsInf(params.Y, 0) {
		return exitcode.NewInvalidArgument(errors.New("the point must be two finite numbers in metres"))
	}
	c.logger.Logger().Info("executing go-to-point command",
		zap.String("endpoint_id", params.EndpointID), zap.Float64("x", params.X), zap.Float64("y", params.Y))

	sender, err := c.resolver.For(ctx, params.EndpointID, api.INTERFACE_PORTOS_ROBOTIC_VACUUM_CLEANER, api.INTERFACE_PORTOS_ROBOTIC_VACUUM_CLEANER_GO_TO_POINT)
	if err != nil {
		return err
	}
	held := transport.Held(sender)
	if !held && params.Frame == "" {
		return exitcode.NewInvalidArgument(errors.New("--frame is required without a hold: send the relativeToGroupRouteId the robot's pose reports (a hold reads it for you)"))
	}

	body := map[string]interface{}{
		"point": map[string]interface{}{"type": "Point", "coordinates": []float64{params.X, params.Y}},
	}
	if params.Frame != "" {
		body["relativeToGroupRouteId"] = params.Frame
	}
	messageType := api.INTERFACE_PORTOS_ROBOTIC_VACUUM_CLEANER + "/" + api.INTERFACE_PORTOS_ROBOTIC_VACUUM_CLEANER_GO_TO_POINT
	sync := api.SyncResponseType
	request := &api.CreateMessageRequest{
		Target: api.CreateMessageTarget{Type: api.EndpointTarget, Id: params.EndpointID},
		Message: api.Message{
			Type: &messageType,
			Header: &api.MessageHeader{
				Namespace: api.INTERFACE_PORTOS_ROBOTIC_VACUUM_CLEANER,
				Name:      api.INTERFACE_PORTOS_ROBOTIC_VACUUM_CLEANER_GO_TO_POINT,
				Version:   goToPointVersion,
			},
			Body: body,
		},
		ResponseType: &sync,
	}
	response, err := sender.Send(ctx, request, uuid.NewString())
	if err != nil {
		return fmt.Errorf("go-to-point: %w", err)
	}
	if response == nil {
		return errors.New("go-to-point: no message request was returned")
	}
	if c.formatter.IsJSON() {
		if printErr := c.formatter.Print(response); printErr != nil {
			return printErr
		}
		return messageRequestFailure(response)
	}
	if err := messageRequestFailure(response); err != nil {
		return fmt.Errorf("go-to-point: %w", err)
	}
	return c.printGoToPoint(params, held, response)
}

func (c *MoveCommand) printGoToPoint(params GoToPointParams, held bool, response *api.MessageRequest) error {
	if !held {
		return c.formatter.Print(fmt.Sprintf("go-to-point %s: request %s %s; the robot is travelling to (%g, %g); read its pose and operational state to see it arrive",
			params.EndpointID, response.Id, response.Status, params.X, params.Y))
	}
	result, err := decodeGoToPointReply(params.EndpointID, response)
	if err != nil {
		return err
	}
	line := fmt.Sprintf("go-to-point %s: arrived, %s", params.EndpointID, result.OperationalState)
	if result.Pose != nil && len(result.Pose.Point.Coordinates) >= 2 {
		line += fmt.Sprintf(" at (%.3f, %.3f) in %s", result.Pose.Point.Coordinates[0], result.Pose.Point.Coordinates[1], result.Pose.RelativeToGroupRouteId)
	}
	return c.formatter.Print(line)
}

// decodeGoToPointReply reads the host's arrival reply.
func decodeGoToPointReply(endpointID string, response *api.MessageRequest) (GoToPointResult, error) {
	result := GoToPointResult{EndpointID: endpointID}
	for _, r := range response.Results {
		if r.Message == nil {
			continue
		}
		encoded, err := json.Marshal(r.Message.Body)
		if err != nil {
			return result, err
		}
		var body struct {
			OperationalState string                             `json:"operationalState"`
			Pose             *api.SpatialInterfacePoseAttribute `json:"pose"`
		}
		if err := json.Unmarshal(encoded, &body); err != nil {
			return result, fmt.Errorf("reading the go-to-point reply: %w", err)
		}
		result.OperationalState, result.Pose = body.OperationalState, body.Pose
		return result, nil
	}
	return result, errors.New("the go-to-point returned no arrival reply; the robot's position is unknown")
}

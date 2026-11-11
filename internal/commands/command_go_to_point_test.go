package commands

import (
	"errors"
	"testing"

	apiclient "github.com/portpowered/portos-cli/internal/api_client"

	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/exitcode"
	"github.com/portpowered/portos-cli/internal/logging"
)

const vacuumNamespace = "port1/systems/zero/capability-interfaces/robotic-vacuum-cleaner"

func arrivalReply(x, y float64) *api.MessageRequest {
	return &api.MessageRequest{
		Id:     "host-1",
		Status: api.SUCCEEDED,
		Results: []api.MessageRequestResult{{
			TargetId: "ep/1",
			Message: &api.CapabilityResponseMessage{
				Header: &api.MessageHeader{Namespace: vacuumNamespace, Name: "go-to-point-response", Version: "1.0"},
				Body: map[string]interface{}{
					"operationalState": "STOPPED",
					"pose": map[string]interface{}{
						"point":                  map[string]interface{}{"type": "Point", "coordinates": []float64{x, y}},
						"relativeToGroupRouteId": "sim/floor",
						"observedAt":             "2026-11-11T09:00:00Z",
					},
				},
			},
		}},
	}
}

func TestGoToPointUnderAHoldSendsOneSyncMessageAndPrintsTheReply(t *testing.T) {
	messages := &scriptedMessages{responses: []*api.MessageRequest{arrivalReply(1.5, 0)}}
	formatter := &recordingFormatter{json: true}
	cmd := NewMoveCommand(fixedResolver{&heldSender{messages}}, formatter, logging.NewLoggerRef(), nil)
	if err := cmd.GoToPoint(t.Context(), GoToPointParams{EndpointID: "ep/1", X: 1.5, Y: 0}); err != nil {
		t.Fatal(err)
	}
	if got := sentNames(messages); len(got) != 1 || got[0] != "go-to-point" {
		t.Fatalf("sent = %v, want only go-to-point: the host runs the session", got)
	}
	request := messages.sent[0]
	if request.Message.Header.Namespace != vacuumNamespace || request.ResponseType == nil || *request.ResponseType != api.SyncResponseType {
		t.Fatalf("request = %+v", request)
	}
	if _, ok := request.Message.Body["relativeToGroupRouteId"]; ok {
		t.Fatal("the frame was not given, so the body leaves it to the host")
	}
	if len(formatter.printed) != 1 || formatter.printed[0] != messages.responses[0] {
		t.Fatalf("printed = %v, want the reply", formatter.printed)
	}
}

func TestGoToPointSendsTheFrameWhenGiven(t *testing.T) {
	messages := &scriptedMessages{responses: []*api.MessageRequest{arrivalReply(-2, 3.25)}}
	cmd := NewMoveCommand(fixedResolver{&heldSender{messages}}, &recordingFormatter{}, logging.NewLoggerRef(), nil)
	if err := cmd.GoToPoint(t.Context(), GoToPointParams{EndpointID: "ep/1", X: -2, Y: 3.25, Frame: "sim/floor"}); err != nil {
		t.Fatal(err)
	}
	body := messages.sent[0].Message.Body
	if body["relativeToGroupRouteId"] != "sim/floor" {
		t.Fatalf("body = %v", body)
	}
	point := body["point"].(map[string]interface{})
	if coords := point["coordinates"].([]float64); point["type"] != "Point" || coords[0] != -2 || coords[1] != 3.25 {
		t.Fatalf("point = %v", point)
	}
}

func TestGoToPointWithoutAHoldNeedsAFrame(t *testing.T) {
	cmd, messages, _ := newTestMoveCommand()
	err := cmd.GoToPoint(t.Context(), GoToPointParams{EndpointID: "ep/1", X: 1, Y: 1})
	if exitcode.Code(err) != exitcode.ExitInvalid {
		t.Fatalf("err = %v, want an invalid-argument error", err)
	}
	if len(messages.sent) != 0 {
		t.Fatalf("sent = %v, want nothing", sentNames(messages))
	}
	if err := cmd.GoToPoint(t.Context(), GoToPointParams{EndpointID: "ep/1", X: 1, Y: 1, Frame: "sim/floor"}); err != nil {
		t.Fatal(err)
	}
	if got := sentNames(messages); len(got) != 1 || got[0] != "go-to-point" {
		t.Fatalf("sent = %v", got)
	}
}

func TestGoToPointRefusalKeepsItsCode(t *testing.T) {
	code := "value_out_of_range"
	refusal := &api.MessageRequest{
		Id: "req", Status: api.FAILED,
		Errors: []api.MessageRequestError{{TargetId: "ep/1", Error: &api.Error{Message: "outside the map", Details: &api.ErrorDetails{ProviderCode: &code}}}},
	}
	messages := &scriptedMessages{responses: []*api.MessageRequest{refusal}}
	cmd := NewMoveCommand(fixedResolver{&heldSender{messages}}, &recordingFormatter{json: true}, logging.NewLoggerRef(), nil)
	err := cmd.GoToPoint(t.Context(), GoToPointParams{EndpointID: "ep/1", X: 40, Y: 0})
	var failed *apiclient.MessageRequestFailed
	if !errors.As(err, &failed) || exitcode.ProviderCode(failed.Request) != code {
		t.Fatalf("err = %v, want the request failure with %s", err, code)
	}
}

func TestGoToPointRejectsNonFinitePoints(t *testing.T) {
	cmd, _, _ := newTestMoveCommand()
	nan := 0.0
	nan = nan / nan
	if err := cmd.GoToPoint(t.Context(), GoToPointParams{EndpointID: "ep/1", X: nan, Y: 0, Frame: "f"}); exitcode.Code(err) != exitcode.ExitInvalid {
		t.Fatalf("err = %v", err)
	}
}

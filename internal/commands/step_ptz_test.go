package commands

import (
	"context"
	"errors"
	"testing"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/exitcode"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/services/holds"
)

// plainSender is the cloud path: it is not a hold.
type plainSender struct{ messages *scriptedMessages }

func (p *plainSender) Send(ctx context.Context, request *api.CreateMessageRequest, _ string) (*api.MessageRequest, error) {
	return p.messages.Send(ctx, request)
}

func TestStepPTZUnderAHoldSendsOnlyTheDirection(t *testing.T) {
	messages := &scriptedMessages{}
	formatter := &recordingFormatter{json: true}
	cmd := NewMoveCommand(fixedResolver{&heldSender{messages}}, formatter, logging.NewLoggerRef(), nil)
	if err := cmd.StepPTZ(t.Context(), StepPTZParams{EndpointID: "ep/1", Direction: "left"}); err != nil {
		t.Fatal(err)
	}
	if len(messages.sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(messages.sent))
	}
	request := messages.sent[0]
	header := request.Message.Header
	if header.Namespace != api.INTERFACE_PORTOS_CAMERA || header.Name != api.INTERFACE_PORTOS_CAMERA_STEP_PTZ {
		t.Fatalf("header = %+v", header)
	}
	if body := request.Message.Body; len(body) != 1 || body["direction"] != "LEFT" {
		t.Fatalf("body = %v, want only the direction: the host fills the session ID", body)
	}
	if request.ResponseType == nil || *request.ResponseType != api.SyncResponseType {
		t.Fatal("step-ptz was not sent synchronously")
	}
	if got, ok := formatter.printed[0].(StepPTZResult); !ok || got.Direction != "LEFT" {
		t.Fatalf("printed = %v", formatter.printed)
	}
}

func TestStepPTZWithoutAHoldLeaseIsRefusedBeforeAnythingIsSent(t *testing.T) {
	messages := &scriptedMessages{}
	cmd := NewMoveCommand(fixedResolver{&plainSender{messages}}, &recordingFormatter{}, logging.NewLoggerRef(), nil)
	err := cmd.StepPTZ(t.Context(), StepPTZParams{EndpointID: "ep/1", Direction: "UP"})
	if err == nil {
		t.Fatal("step-ptz without a hold succeeded")
	}
	if code := exitcode.Code(err); code != exitcode.ExitHold {
		t.Fatalf("exit code = %d, want %d", code, exitcode.ExitHold)
	}
	var failed *apiclient.MessageRequestFailed
	if !errors.As(err, &failed) || exitcode.ProviderCode(failed.Request) != holds.CodeHoldLeaseRequired {
		t.Fatalf("err = %v, want hold_lease_required", err)
	}
	if len(messages.sent) != 0 {
		t.Fatalf("sent %d messages without a hold", len(messages.sent))
	}
}

func TestStepPTZRejectsAnUnknownDirectionWithoutSending(t *testing.T) {
	messages := &scriptedMessages{}
	cmd := NewMoveCommand(fixedResolver{&heldSender{messages}}, &recordingFormatter{}, logging.NewLoggerRef(), nil)
	err := cmd.StepPTZ(t.Context(), StepPTZParams{EndpointID: "ep/1", Direction: "SIDEWAYS"})
	if code := exitcode.Code(err); code != exitcode.ExitInvalid {
		t.Fatalf("exit code = %d (%v), want %d", code, err, exitcode.ExitInvalid)
	}
	if len(messages.sent) != 0 {
		t.Fatal("an invalid direction was sent")
	}
}

func TestStepPTZPassesTheHostsRefusalToTheExitCode(t *testing.T) {
	cameraUnavailable := holds.CodeCameraUnavailable
	refusal := &api.MessageRequest{Id: "host-1", Status: api.FAILED, Errors: []api.MessageRequestError{{
		Code: api.REJECTED, TargetId: "ep/1",
		Error: &api.Error{Code: api.ErrorCodeSERVICEUNAVAILABLE, Family: api.ErrorFamilySERVICEUNAVAILABLE, Message: "the held camera stopped",
			Details: &api.ErrorDetails{ProviderCode: &cameraUnavailable}},
	}}}
	cmd := NewMoveCommand(fixedResolver{&heldSender{&scriptedMessages{responses: []*api.MessageRequest{refusal}}}}, &recordingFormatter{}, logging.NewLoggerRef(), nil)
	err := cmd.StepPTZ(t.Context(), StepPTZParams{EndpointID: "ep/1", Direction: "DOWN"})
	if code := exitcode.Code(err); code != exitcode.ExitCamera {
		t.Fatalf("exit code = %d (%v), want %d", code, err, exitcode.ExitCamera)
	}
}

// ptzPositionReply is a host reply carrying the provider's ptz-position answer.
func ptzPositionReply(position map[string]any) *api.MessageRequest {
	name := api.INTERFACE_PORTOS_CAMERA_PTZ_POSITION
	return &api.MessageRequest{Id: "req", Status: api.SUCCEEDED, Results: []api.MessageRequestResult{{TargetId: "ep/1", Message: &api.CapabilityResponseMessage{
		Header: &api.MessageHeader{Namespace: api.INTERFACE_PORTOS_CAMERA, Name: name, Version: "1.0"}, Body: position,
	}}}}
}

func TestStepPTZSendsDegreesAndPrintsWhereTheCameraPoints(t *testing.T) {
	messages := &scriptedMessages{responses: []*api.MessageRequest{ptzPositionReply(map[string]any{
		"movedDegrees": 46, "panDegrees": 0, "panReference": "LEFT_END", "panRangeDegrees": 340,
		"tiltDegrees": -15, "tiltReference": "SESSION_START", "tiltRangeDegrees": 169,
	})}}
	formatter := &recordingFormatter{}
	cmd := NewMoveCommand(fixedResolver{&heldSender{messages}}, formatter, logging.NewLoggerRef(), nil)
	if err := cmd.StepPTZ(t.Context(), StepPTZParams{EndpointID: "ep/1", Direction: "left", Degrees: 360}); err != nil {
		t.Fatal(err)
	}
	if body := messages.sent[0].Message.Body; body["direction"] != "LEFT" || body["degrees"] != float64(360) || len(body) != 2 {
		t.Fatalf("body = %v, want the direction and degrees", body)
	}
	want := "Turned ep/1 LEFT 46°. Now pan 0° right of the left end (range 0 to 340°), " +
		"tilt 15° below the session start (bottom end unknown until you turn to an end)."
	if len(formatter.printed) != 1 || formatter.printed[0] != want {
		t.Fatalf("printed %q\nwant    %q", formatter.printed, want)
	}
}

func TestStepPTZJSONCarriesThePosition(t *testing.T) {
	messages := &scriptedMessages{responses: []*api.MessageRequest{ptzPositionReply(map[string]any{
		"movedDegrees": 31, "panDegrees": -15, "panReference": "SESSION_START", "panRangeDegrees": 340,
		"tiltDegrees": 31, "tiltReference": "BOTTOM_END", "tiltRangeDegrees": 169,
	})}}
	formatter := &recordingFormatter{json: true}
	cmd := NewMoveCommand(fixedResolver{&heldSender{messages}}, formatter, logging.NewLoggerRef(), nil)
	if err := cmd.StepPTZ(t.Context(), StepPTZParams{EndpointID: "ep/1", Direction: "UP", Degrees: 30}); err != nil {
		t.Fatal(err)
	}
	got, ok := formatter.printed[0].(StepPTZResult)
	if !ok || got.Degrees != 30 || got.Position == nil || got.Position.TiltDegrees != 31 ||
		got.Position.TiltReference != api.CameraInterfacePtzPositionMessageTiltReferenceBOTTOMEND || got.Position.PanDegrees != -15 {
		t.Fatalf("printed = %+v", formatter.printed)
	}
	if text := describeStepPTZ(got); text != "Turned ep/1 UP 31°. Now pan 15° left of the session start (left end unknown until you turn to an end), tilt 31° up from the bottom end (range 0 to 169°)." {
		t.Fatalf("text = %q", text)
	}
}

func TestStepPTZRejectsDegreesOutOfRangeWithoutSending(t *testing.T) {
	for _, degrees := range []float64{0.5, -10, 361} {
		messages := &scriptedMessages{}
		cmd := NewMoveCommand(fixedResolver{&heldSender{messages}}, &recordingFormatter{}, logging.NewLoggerRef(), nil)
		err := cmd.StepPTZ(t.Context(), StepPTZParams{EndpointID: "ep/1", Direction: "UP", Degrees: degrees})
		if code := exitcode.Code(err); code != exitcode.ExitInvalid || len(messages.sent) != 0 {
			t.Fatalf("degrees %g: exit code %d (%v), sent %d", degrees, code, err, len(messages.sent))
		}
	}
}

func TestStepPTZWithoutAPositionAnswerSaysSo(t *testing.T) {
	formatter := &recordingFormatter{}
	cmd := NewMoveCommand(fixedResolver{&heldSender{&scriptedMessages{}}}, formatter, logging.NewLoggerRef(), nil)
	if err := cmd.StepPTZ(t.Context(), StepPTZParams{EndpointID: "ep/1", Direction: "RIGHT"}); err != nil {
		t.Fatal(err)
	}
	if formatter.printed[0] != "Stepped ep/1 RIGHT; the camera reported no position" {
		t.Fatalf("printed %q", formatter.printed)
	}
}

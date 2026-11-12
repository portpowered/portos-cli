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

package commands

import (
	"context"
	"errors"
	"testing"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/exitcode"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/transport"
)

type fakeMessages struct {
	sent     *api.CreateMessageRequest
	response *api.MessageRequest
}

func (f *fakeMessages) Send(_ context.Context, request *api.CreateMessageRequest, _ ...string) (*api.MessageRequest, error) {
	f.sent = request
	return f.response, nil
}

type messageTestClient struct {
	apiclient.Client
	messages *fakeMessages
}

func (f *messageTestClient) Messages() apiclient.MessagesClient { return f.messages }
func (f *messageTestClient) Relay() apiclient.RelayClient {
	return relayClient{err: errors.New("no relay in tests")}
}

type recordingFormatter struct {
	json    bool
	printed []interface{}
}

func (r *recordingFormatter) Print(data interface{}) error {
	r.printed = append(r.printed, data)
	return nil
}
func (r *recordingFormatter) PrintError(error) error { return nil }
func (r *recordingFormatter) IsJSON() bool           { return r.json }
func (r *recordingFormatter) IsQuiet() bool          { return false }

func newTestMessageCommand(response *api.MessageRequest, json bool) (*MessageCommand, *fakeMessages, *recordingFormatter) {
	messages := &fakeMessages{response: response}
	formatter := &recordingFormatter{json: json}
	return NewMessageCommand(&messageTestClient{messages: messages}, transport.CloudOnly(&messageTestClient{messages: messages}), formatter, logging.NewLoggerRef(), nil), messages, formatter
}

func succeeded() *api.MessageRequest {
	return &api.MessageRequest{Id: "req-1", Status: api.SUCCEEDED}
}

func TestSendWaitSetsSync(t *testing.T) {
	cmd, messages, _ := newTestMessageCommand(succeeded(), false)
	if err := cmd.Run(t.Context(), RunParams{EndpointID: "ep/1", Command: "turn-on", Wait: true}); err != nil {
		t.Fatal(err)
	}
	if got := messages.sent.ResponseType; got == nil || *got != api.SyncResponseType {
		t.Fatalf("ResponseType = %v, want SYNC", got)
	}

	cmd, messages, _ = newTestMessageCommand(succeeded(), false)
	if err := cmd.Run(t.Context(), RunParams{EndpointID: "ep/1", Command: "turn-on"}); err != nil {
		t.Fatal(err)
	}
	if messages.sent.ResponseType != nil {
		t.Fatalf("ResponseType = %v without --wait, want unset", *messages.sent.ResponseType)
	}
}

func TestSendBodyJSON(t *testing.T) {
	cmd, messages, _ := newTestMessageCommand(succeeded(), false)
	body := `{"velocity":0.2,"omega":0,"duration":5000}`
	if err := cmd.Run(t.Context(), RunParams{EndpointID: "ep/1", Command: "remote-control/move", Body: body}); err != nil {
		t.Fatal(err)
	}
	got := messages.sent.Message.Body
	if got["velocity"] != 0.2 || got["omega"] != float64(0) || got["duration"] != float64(5000) || len(got) != 3 {
		t.Fatalf("body = %#v", got)
	}
}

func TestBodyAndArgsConflict(t *testing.T) {
	cmd, messages, _ := newTestMessageCommand(succeeded(), false)
	err := cmd.Run(t.Context(), RunParams{EndpointID: "ep/1", Command: "turn-on", Body: `{"a":1}`, Args: []string{"b=2"}})
	if err == nil || exitcode.Code(err) != exitcode.ExitInvalid {
		t.Fatalf("err = %v, code %d, want invalid argument", err, exitcode.Code(err))
	}
	if messages.sent != nil {
		t.Fatal("request must not be sent")
	}
}

func TestBodyMustBeJSONObject(t *testing.T) {
	cmd, _, _ := newTestMessageCommand(succeeded(), false)
	err := cmd.Run(t.Context(), RunParams{EndpointID: "ep/1", Command: "turn-on", Body: `[1]`})
	if exitcode.Code(err) != exitcode.ExitInvalid {
		t.Fatalf("err = %v, code %d", err, exitcode.Code(err))
	}
}

func TestFailedRequestPrintsJSONThenReturnsTypedError(t *testing.T) {
	code := "remote_control_move_not_carried_out"
	failed := &api.MessageRequest{Id: "req-2", Status: api.FAILED, Errors: []api.MessageRequestError{{
		TargetId: "ep/1",
		Error:    &api.Error{Message: "did not move", Details: &api.ErrorDetails{ProviderCode: &code}},
	}}}
	cmd, _, formatter := newTestMessageCommand(failed, true)
	err := cmd.Run(t.Context(), RunParams{EndpointID: "ep/1", Command: "move", Wait: true})
	var typed *apiclient.MessageRequestFailed
	if !errors.As(err, &typed) || typed.Request != failed {
		t.Fatalf("err = %v, want MessageRequestFailed", err)
	}
	if exitcode.Code(err) != exitcode.ExitNotCarriedOut {
		t.Fatalf("exit code %d", exitcode.Code(err))
	}
	if len(formatter.printed) != 1 || formatter.printed[0] != failed {
		t.Fatalf("printed = %#v, want the full request", formatter.printed)
	}
}

func TestEmptyBodyFlagIsInvalid(t *testing.T) {
	cmd, messages, _ := newTestMessageCommand(succeeded(), false)
	err := cmd.Run(t.Context(), RunParams{EndpointID: "ep/1", Command: "turn-on", Body: "", BodySet: true})
	if exitcode.Code(err) != exitcode.ExitInvalid {
		t.Fatalf("err = %v, code %d, want invalid argument", err, exitcode.Code(err))
	}
	if messages.sent != nil {
		t.Fatal("request must not be sent")
	}
}

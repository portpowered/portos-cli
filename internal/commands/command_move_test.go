package commands

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/exitcode"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/transport"
)

// scriptedMessages answers each Send with the next scripted response and records the requests.
type scriptedMessages struct {
	sent      []*api.CreateMessageRequest
	responses []*api.MessageRequest
}

func (s *scriptedMessages) Send(_ context.Context, request *api.CreateMessageRequest, _ ...string) (*api.MessageRequest, error) {
	s.sent = append(s.sent, request)
	if len(s.responses) < len(s.sent) {
		return &api.MessageRequest{Id: "req", Status: api.SUCCEEDED}, nil
	}
	return s.responses[len(s.sent)-1], nil
}

type scriptedMessageClient struct {
	apiclient.Client
	messages *scriptedMessages
}

func (c *scriptedMessageClient) Messages() apiclient.MessagesClient { return c.messages }
func (c *scriptedMessageClient) Relay() apiclient.RelayClient {
	return relayClient{err: errors.New("no relay in tests")}
}

func startOK() *api.MessageRequest {
	return &api.MessageRequest{Id: "req-start", Status: api.SUCCEEDED}
}

func moveReply(distance, turned float64) *api.MessageRequest {
	return &api.MessageRequest{
		Id:     "req-move",
		Status: api.SUCCEEDED,
		Results: []api.MessageRequestResult{{
			RouteId:  "route-1",
			TargetId: "ep/1",
			Message: &api.CapabilityResponseMessage{
				Header: &api.MessageHeader{Namespace: rcNamespace, Name: "move-response", Version: rcVersion},
				Body: map[string]interface{}{
					"distanceMetres": distance,
					"turnedDegrees":  turned,
					"pose": map[string]interface{}{
						"point":                  map[string]interface{}{"type": "Point", "coordinates": []float64{1, 2}},
						"relativeToGroupRouteId": "map-1",
						"observedAt":             "2026-11-10T09:00:00Z",
					},
				},
			},
		}},
	}
}

func notCarriedOut() *api.MessageRequest {
	code := "remote_control_move_not_carried_out"
	return &api.MessageRequest{
		Id:     "req-fail",
		Status: api.FAILED,
		Errors: []api.MessageRequestError{{
			TargetId: "ep/1",
			Error:    &api.Error{Message: "the move was not carried out", Details: &api.ErrorDetails{ProviderCode: &code}},
		}},
	}
}

func newTestMoveCommand(responses ...*api.MessageRequest) (*MoveCommand, *scriptedMessages, *recordingFormatter) {
	messages := &scriptedMessages{responses: responses}
	formatter := &recordingFormatter{json: true}
	return NewMoveCommand(transport.CloudOnly(&scriptedMessageClient{messages: messages}), formatter, logging.NewLoggerRef(), nil), messages, formatter
}

func sentNames(messages *scriptedMessages) []string {
	names := make([]string, len(messages.sent))
	for i, request := range messages.sent {
		names[i] = request.Message.Header.Name
	}
	return names
}

func TestMoveSendsSyncAndPrintsReplies(t *testing.T) {
	cmd, messages, formatter := newTestMoveCommand(startOK(), moveReply(0.98, 0), moveReply(0.02, 88))
	err := cmd.Run(t.Context(), MoveParams{EndpointID: "ep/1", Instructions: []string{"forward 1m, rotate 90"}})
	if err != nil {
		t.Fatal(err)
	}
	got := sentNames(messages)
	if len(got) != 4 || got[0] != "start-session" || got[1] != "move" || got[2] != "move" || got[3] != "end-session" {
		t.Fatalf("sent = %v", got)
	}
	for i, request := range messages.sent {
		if request.ResponseType == nil || *request.ResponseType != api.SyncResponseType {
			t.Errorf("request %d (%s) ResponseType = %v, want SYNC", i, request.Message.Header.Name, request.ResponseType)
		}
	}
	if first, second := messages.sent[1].Message.Body["seqNum"], messages.sent[2].Message.Body["seqNum"]; first != 1 || second != 2 {
		t.Fatalf("seqNum = %v, %v, want 1, 2", first, second)
	}

	if len(formatter.printed) != 1 {
		t.Fatalf("printed %d values, want 1", len(formatter.printed))
	}
	result, ok := formatter.printed[0].(MoveResult)
	if !ok {
		t.Fatalf("printed %T, want MoveResult", formatter.printed[0])
	}
	if result.EndpointID != "ep/1" || len(result.Replies) != 2 {
		t.Fatalf("result = %+v", result)
	}
	if result.Replies[0].DistanceMetres != 0.98 || result.Replies[1].TurnedDegrees != 88 {
		t.Fatalf("replies = %+v", result.Replies)
	}
}

func TestMoveHumanOutputOneLinePerReply(t *testing.T) {
	cmd, _, formatter := newTestMoveCommand(startOK(), moveReply(0.98, 0), moveReply(0.02, 88))
	formatter.json = false
	if err := cmd.Run(t.Context(), MoveParams{EndpointID: "ep/1", Instructions: []string{"forward 1m, rotate 90"}}); err != nil {
		t.Fatal(err)
	}
	if len(formatter.printed) != 2 {
		t.Fatalf("printed %v, want one line per reply", formatter.printed)
	}
	if want := "move 1: distanceMetres 0.98 turnedDegrees 0"; formatter.printed[0] != want {
		t.Errorf("line 1 = %q, want %q", formatter.printed[0], want)
	}
	if want := "move 2: distanceMetres 0.02 turnedDegrees 88"; formatter.printed[1] != want {
		t.Errorf("line 2 = %q, want %q", formatter.printed[1], want)
	}
}

func TestMoveStopsOnNotCarriedOut(t *testing.T) {
	cmd, messages, formatter := newTestMoveCommand(startOK(), moveReply(0.98, 0), notCarriedOut())
	err := cmd.Run(t.Context(), MoveParams{EndpointID: "ep/1", Instructions: []string{"forward 1m, forward 0.05m, forward 1m"}})
	if err == nil {
		t.Fatal("expected an error")
	}
	var failed *apiclient.MessageRequestFailed
	if !errors.As(err, &failed) {
		t.Fatalf("error %v is not a MessageRequestFailed", err)
	}
	if code := exitcode.Code(err); code != exitcode.ExitNotCarriedOut {
		t.Fatalf("exit code = %d, want %d", code, exitcode.ExitNotCarriedOut)
	}
	if got := sentNames(messages); len(got) != 4 || got[3] != "end-session" {
		t.Fatalf("sent = %v, want start, move, move, end (third move unsent)", got)
	}
	result, ok := formatter.printed[0].(MoveResult)
	if !ok || len(result.Replies) != 1 {
		t.Fatalf("printed = %+v, want the one reply measured before the failure", formatter.printed)
	}
}

func TestMoveEndSessionFailureDoesNotMaskMoveFailure(t *testing.T) {
	cmd, _, _ := newTestMoveCommand(startOK(), notCarriedOut(), &api.MessageRequest{Id: "end", Status: api.FAILED})
	err := cmd.Run(t.Context(), MoveParams{EndpointID: "ep/1", Instructions: []string{"forward 1m"}})
	if code := exitcode.Code(err); code != exitcode.ExitNotCarriedOut {
		t.Fatalf("exit code = %d (%v), want %d", code, err, exitcode.ExitNotCarriedOut)
	}
}

func TestMoveSkipsEndSessionWhenStartFails(t *testing.T) {
	cmd, messages, _ := newTestMoveCommand(&api.MessageRequest{Id: "start", Status: api.FAILED})
	if err := cmd.Run(t.Context(), MoveParams{EndpointID: "ep/1", Instructions: []string{"forward 1m"}}); err == nil {
		t.Fatal("expected an error")
	}
	if got := sentNames(messages); len(got) != 1 {
		t.Fatalf("sent = %v, want only start-session", got)
	}
}

func TestMoveResultJSON(t *testing.T) {
	cmd, _, formatter := newTestMoveCommand(startOK(), moveReply(0.5, 0))
	if err := cmd.Run(t.Context(), MoveParams{EndpointID: "ep/1", Instructions: []string{"forward 0.5m"}}); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(formatter.printed[0])
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	replies, _ := decoded["replies"].([]interface{})
	if decoded["endpoint_id"] != "ep/1" || len(replies) != 1 || replies[0].(map[string]interface{})["distanceMetres"] != 0.5 {
		t.Fatalf("json = %s", encoded)
	}
}

func TestMoveQueuedReplyIsAnErrorAndStopsTheRun(t *testing.T) {
	queued := &api.MessageRequest{Id: "req-q", Status: api.QUEUED}
	cmd, messages, formatter := newTestMoveCommand(startOK(), moveReply(0.98, 0), queued)
	err := cmd.Run(t.Context(), MoveParams{EndpointID: "ep/1", Instructions: []string{"forward 1m, forward 1m, forward 1m"}})
	if err == nil || !strings.Contains(err.Error(), "move") || !strings.Contains(err.Error(), "QUEUED") {
		t.Fatalf("err = %v, want a move error naming QUEUED", err)
	}
	var failed *apiclient.MessageRequestFailed
	if errors.As(err, &failed) || exitcode.Code(err) != exitcode.ExitFailure {
		t.Fatalf("err = %v (exit %d), want a plain exit-1 error", err, exitcode.Code(err))
	}
	if got := sentNames(messages); len(got) != 4 || got[3] != "end-session" {
		t.Fatalf("sent = %v, want start, move, move, end", got)
	}
	if result := formatter.printed[0].(MoveResult); len(result.Replies) != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestMoveSucceededWithoutReplyBodyIsAnError(t *testing.T) {
	bare := &api.MessageRequest{Id: "req-bare", Status: api.SUCCEEDED}
	cmd, messages, _ := newTestMoveCommand(startOK(), bare)
	err := cmd.Run(t.Context(), MoveParams{EndpointID: "ep/1", Instructions: []string{"forward 1m, forward 1m"}})
	if err == nil || exitcode.Code(err) != exitcode.ExitFailure {
		t.Fatalf("err = %v, want exit-1 error", err)
	}
	if got := sentNames(messages); len(got) != 3 || got[2] != "end-session" {
		t.Fatalf("sent = %v, want start, move, end", got)
	}
}

func TestMoveReplyMissingMeasurementsIsAnError(t *testing.T) {
	partial := moveReply(0.5, 0)
	delete(partial.Results[0].Message.Body, "turnedDegrees")
	cmd, _, _ := newTestMoveCommand(startOK(), partial)
	err := cmd.Run(t.Context(), MoveParams{EndpointID: "ep/1", Instructions: []string{"forward 1m"}})
	if err == nil || !strings.Contains(err.Error(), "turnedDegrees") {
		t.Fatalf("err = %v, want an error naming turnedDegrees", err)
	}
}

func TestMoveDecodesTheMoveResponseAmongSeveralResults(t *testing.T) {
	reply := moveReply(0.7, 0)
	other := api.MessageRequestResult{RouteId: "route-2", TargetId: "ep/1", Message: &api.CapabilityResponseMessage{
		Header: &api.MessageHeader{Namespace: rcNamespace, Name: "other-response", Version: rcVersion},
		Body:   map[string]interface{}{"unrelated": true},
	}}
	reply.Results = append([]api.MessageRequestResult{other}, reply.Results...)
	cmd, _, formatter := newTestMoveCommand(startOK(), reply)
	if err := cmd.Run(t.Context(), MoveParams{EndpointID: "ep/1", Instructions: []string{"forward 0.7m"}}); err != nil {
		t.Fatal(err)
	}
	if result := formatter.printed[0].(MoveResult); len(result.Replies) != 1 || result.Replies[0].DistanceMetres != 0.7 {
		t.Fatalf("result = %+v", result)
	}
}

func TestMoveQueuedStartSessionIsAnError(t *testing.T) {
	cmd, messages, _ := newTestMoveCommand(&api.MessageRequest{Id: "start", Status: api.INPROGRESS})
	err := cmd.Run(t.Context(), MoveParams{EndpointID: "ep/1", Instructions: []string{"forward 1m"}})
	if err == nil || exitcode.Code(err) != exitcode.ExitFailure {
		t.Fatalf("err = %v", err)
	}
	if got := sentNames(messages); len(got) != 1 {
		t.Fatalf("sent = %v", got)
	}
}

func TestMoveJSONAlwaysPrintsAResult(t *testing.T) {
	cmd, _, formatter := newTestMoveCommand(startOK(), notCarriedOut())
	if err := cmd.Run(t.Context(), MoveParams{EndpointID: "ep/1", Instructions: []string{"forward 1m"}}); err == nil {
		t.Fatal("expected an error")
	}
	if len(formatter.printed) != 1 {
		t.Fatalf("printed %v, want one MoveResult", formatter.printed)
	}
	result := formatter.printed[0].(MoveResult)
	if result.EndpointID != "ep/1" || result.Replies == nil || len(result.Replies) != 0 {
		t.Fatalf("result = %+v, want empty non-nil replies", result)
	}
}

// heldSender is a Sender that reports a motion hold and records what it was sent.
type heldSender struct{ messages *scriptedMessages }

func (h *heldSender) Held() bool { return true }
func (h *heldSender) Send(ctx context.Context, request *api.CreateMessageRequest, _ string) (*api.MessageRequest, error) {
	return h.messages.Send(ctx, request)
}

type fixedResolver struct{ sender transport.Sender }

func (f fixedResolver) For(context.Context, string, string) (transport.Sender, error) {
	return f.sender, nil
}

func TestMoveGrammarUnderHoldSkipsSession(t *testing.T) {
	messages := &scriptedMessages{responses: []*api.MessageRequest{moveReply(0.98, 0), moveReply(0.02, 88)}}
	formatter := &recordingFormatter{json: true}
	cmd := NewMoveCommand(fixedResolver{&heldSender{messages}}, formatter, logging.NewLoggerRef(), nil)
	if err := cmd.Run(t.Context(), MoveParams{EndpointID: "ep/1", Instructions: []string{"forward 1m, rotate 90"}}); err != nil {
		t.Fatal(err)
	}
	if got := sentNames(messages); len(got) != 2 || got[0] != "move" || got[1] != "move" {
		t.Fatalf("sent = %v, want only moves under a hold", got)
	}
}

func TestBodylessSessionCommandsSendAnEmptyObject(t *testing.T) {
	cmd, messages, _ := newTestMoveCommand(startOK(), moveReply(0.98, 0))
	if err := cmd.Run(t.Context(), MoveParams{EndpointID: "ep/1", Instructions: []string{"forward 1m"}}); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{0, 2} {
		request := messages.sent[i]
		raw, err := json.Marshal(request.Message.Body)
		if err != nil || string(raw) != "{}" {
			t.Errorf("%s body marshals to %s (err %v), want {}", request.Message.Header.Name, raw, err)
		}
	}
}

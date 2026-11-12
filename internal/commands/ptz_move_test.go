package commands

import (
	"testing"

	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/exitcode"
	"github.com/portpowered/portos-cli/internal/logging"
)

func TestMovePTZSendsTheTurnAndPrintsWhereATimedTurnStopped(t *testing.T) {
	messages := &scriptedMessages{responses: []*api.MessageRequest{ptzPositionReply(map[string]any{
		"movedDegrees": 20, "panDegrees": 120, "panReference": "LEFT_END", "panRangeDegrees": 340,
		"tiltDegrees": 0, "tiltReference": "SESSION_START", "tiltRangeDegrees": 169,
	})}}
	formatter := &recordingFormatter{}
	cmd := NewMoveCommand(fixedResolver{&heldSender{messages}}, formatter, logging.NewLoggerRef(), nil)
	if err := cmd.MovePTZ(t.Context(), MovePTZParams{EndpointID: "ep/1", Direction: "right", Speed: 0.5, Seconds: 2}); err != nil {
		t.Fatal(err)
	}
	request := messages.sent[0]
	if request.Message.Header.Name != api.INTERFACE_PORTOS_CAMERA_MOVE_PTZ || *request.ResponseType != api.SyncResponseType {
		t.Fatalf("request = %+v", request.Message.Header)
	}
	if body := request.Message.Body; len(body) != 3 || body["direction"] != "RIGHT" || body["speed"] != 0.5 || body["seconds"] != float64(2) {
		t.Fatalf("body = %v, want direction, speed and seconds only: the host fills the session ID", body)
	}
	want := "Turned ep/1 RIGHT 20° in 2s. Now pan 120° right of the left end (range 0 to 340°), " +
		"tilt 0° above the session start (bottom end unknown until you turn to an end)."
	if formatter.printed[0] != want {
		t.Fatalf("printed %q\nwant    %q", formatter.printed, want)
	}
}

func TestMovePTZWithoutSecondsSaysTheCameraIsStillTurning(t *testing.T) {
	messages := &scriptedMessages{responses: []*api.MessageRequest{ptzPositionReply(map[string]any{
		"movedDegrees": 0, "panDegrees": 0, "panReference": "SESSION_START", "panRangeDegrees": 340, "panMoving": true,
		"tiltDegrees": 0, "tiltReference": "SESSION_START", "tiltRangeDegrees": 169,
	})}}
	formatter := &recordingFormatter{}
	cmd := NewMoveCommand(fixedResolver{&heldSender{messages}}, formatter, logging.NewLoggerRef(), nil)
	if err := cmd.MovePTZ(t.Context(), MovePTZParams{EndpointID: "ep/1", Direction: "LEFT"}); err != nil {
		t.Fatal(err)
	}
	if body := messages.sent[0].Message.Body; len(body) != 1 || body["direction"] != "LEFT" {
		t.Fatalf("body = %v, want only the direction", body)
	}
	want := "Turning ep/1 LEFT until stop-ptz or the end of its range. Now pan 0° right of the session start " +
		"(left end unknown until you turn to an end), still turning, tilt 0° above the session start (bottom end unknown until you turn to an end)."
	if formatter.printed[0] != want {
		t.Fatalf("printed %q\nwant    %q", formatter.printed, want)
	}
}

func TestMovePTZRejectsBadInputWithoutSending(t *testing.T) {
	for _, params := range []MovePTZParams{
		{Direction: "LEFT"},
		{EndpointID: "ep/1", Direction: "AROUND"},
		{EndpointID: "ep/1", Direction: "LEFT", Speed: 0.05},
		{EndpointID: "ep/1", Direction: "LEFT", Speed: 1.5},
		{EndpointID: "ep/1", Direction: "LEFT", Seconds: 0.05},
		{EndpointID: "ep/1", Direction: "LEFT", Seconds: 61},
	} {
		messages := &scriptedMessages{}
		cmd := NewMoveCommand(fixedResolver{&heldSender{messages}}, &recordingFormatter{}, logging.NewLoggerRef(), nil)
		if err := cmd.MovePTZ(t.Context(), params); err == nil || len(messages.sent) != 0 {
			t.Fatalf("%+v: err %v, sent %d", params, err, len(messages.sent))
		}
	}
}

func TestMovePTZWithoutAHoldLeaseIsRefused(t *testing.T) {
	messages := &scriptedMessages{}
	cmd := NewMoveCommand(fixedResolver{&plainSender{messages}}, &recordingFormatter{}, logging.NewLoggerRef(), nil)
	err := cmd.MovePTZ(t.Context(), MovePTZParams{EndpointID: "ep/1", Direction: "UP"})
	if code := exitcode.Code(err); code != exitcode.ExitHold || len(messages.sent) != 0 {
		t.Fatalf("exit code %d (%v), sent %d", code, err, len(messages.sent))
	}
}

func TestMovePTZJSONAndNoPosition(t *testing.T) {
	formatter := &recordingFormatter{json: true}
	cmd := NewMoveCommand(fixedResolver{&heldSender{&scriptedMessages{}}}, formatter, logging.NewLoggerRef(), nil)
	if err := cmd.MovePTZ(t.Context(), MovePTZParams{EndpointID: "ep/1", Direction: "down", Seconds: 0.5}); err != nil {
		t.Fatal(err)
	}
	got, ok := formatter.printed[0].(MovePTZResult)
	if !ok || got.Direction != "DOWN" || got.Seconds != 0.5 || got.Position != nil {
		t.Fatalf("printed = %+v", formatter.printed)
	}
	if text := describeMovePTZ(got); text != "Turned ep/1 DOWN; the camera reported no position" {
		t.Fatalf("text = %q", text)
	}
}

func TestStopPTZStopsOneAxisOrBoth(t *testing.T) {
	messages := &scriptedMessages{responses: []*api.MessageRequest{ptzPositionReply(map[string]any{
		"movedDegrees": 42, "panDegrees": 42, "panReference": "LEFT_END", "panRangeDegrees": 340,
		"tiltDegrees": 30, "tiltReference": "BOTTOM_END", "tiltRangeDegrees": 169,
	})}}
	formatter := &recordingFormatter{}
	cmd := NewMoveCommand(fixedResolver{&heldSender{messages}}, formatter, logging.NewLoggerRef(), nil)
	if err := cmd.StopPTZ(t.Context(), StopPTZParams{EndpointID: "ep/1", Axis: "pan"}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.StopPTZ(t.Context(), StopPTZParams{EndpointID: "ep/1"}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.StopPTZ(t.Context(), StopPTZParams{EndpointID: "ep/1", Axis: "tilt"}); err != nil {
		t.Fatal(err)
	}
	if name := messages.sent[0].Message.Header.Name; name != api.INTERFACE_PORTOS_CAMERA_STOP_PTZ {
		t.Fatalf("name = %s", name)
	}
	if body := messages.sent[0].Message.Body; len(body) != 1 || body["axis"] != "PAN" {
		t.Fatalf("body = %v", body)
	}
	if body := messages.sent[1].Message.Body; body == nil || len(body) != 0 {
		t.Fatalf("body = %v, want {} to stop both axes", body)
	}
	if body := messages.sent[2].Message.Body; body["axis"] != "TILT" {
		t.Fatalf("body = %v", body)
	}
	want := "Stopped ep/1 after 42°. Now pan 42° right of the left end (range 0 to 340°), tilt 30° up from the bottom end (range 0 to 169°)."
	if formatter.printed[0] != want {
		t.Fatalf("printed %q\nwant    %q", formatter.printed[0], want)
	}
	if formatter.printed[1] != "Stopped ep/1; the camera reported no position" {
		t.Fatalf("printed %q", formatter.printed[1])
	}
}

func TestStopPTZRejectsBadInputAndPrintsJSON(t *testing.T) {
	messages := &scriptedMessages{}
	formatter := &recordingFormatter{json: true}
	cmd := NewMoveCommand(fixedResolver{&heldSender{messages}}, formatter, logging.NewLoggerRef(), nil)
	if err := cmd.StopPTZ(t.Context(), StopPTZParams{}); err == nil {
		t.Fatal("stop-ptz without an endpoint succeeded")
	}
	if code := exitcode.Code(cmd.StopPTZ(t.Context(), StopPTZParams{EndpointID: "ep/1", Axis: "ZOOM"})); code != exitcode.ExitInvalid || len(messages.sent) != 0 {
		t.Fatalf("exit code %d, sent %d", code, len(messages.sent))
	}
	if err := cmd.StopPTZ(t.Context(), StopPTZParams{EndpointID: "ep/1", Axis: "Tilt"}); err != nil {
		t.Fatal(err)
	}
	if got, ok := formatter.printed[0].(StopPTZResult); !ok || got.Axis != "TILT" {
		t.Fatalf("printed = %+v", formatter.printed)
	}
	plain := &scriptedMessages{}
	refused := NewMoveCommand(fixedResolver{&plainSender{plain}}, &recordingFormatter{}, logging.NewLoggerRef(), nil)
	if code := exitcode.Code(refused.StopPTZ(t.Context(), StopPTZParams{EndpointID: "ep/1"})); code != exitcode.ExitHold || len(plain.sent) != 0 {
		t.Fatalf("exit code %d, sent %d", code, len(plain.sent))
	}
}

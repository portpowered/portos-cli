package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/exitcode"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/services/messages"
)

// heldSnapshotReply is the session host's get-snapshot reply as it arrives over the socket.
func heldSnapshotReply(t *testing.T, jpeg []byte, capturedAt time.Time) *api.MessageRequest {
	t.Helper()
	size := len(jpeg)
	message := api.NewSnapshotInterfaceGetSnapshotResponseMessage(api.SnapshotInterfaceGetSnapshotResponseMessage{
		Image:        api.MediaContent{MimeType: api.Imagejpeg, Data: &jpeg, ByteLength: &size},
		TimeOfSample: capturedAt,
	})
	raw, err := json.Marshal(api.MessageRequest{Id: "host-1", Status: api.SUCCEEDED, Results: []api.MessageRequestResult{{TargetId: "ep/1", Message: &message}}})
	if err != nil {
		t.Fatal(err)
	}
	var wire api.MessageRequest
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	return &wire
}

func TestSnapshotUnderAHoldReadsTheHeldCamera(t *testing.T) {
	frame := []byte{0xff, 0xd8, 0xff, 0xe0, 1, 2, 3}
	capturedAt := time.Date(2026, 11, 11, 9, 0, 0, 250_000_000, time.UTC)
	sent := &scriptedMessages{responses: []*api.MessageRequest{heldSnapshotReply(t, frame, capturedAt)}}
	formatter := &recordingFormatter{json: true}
	cmd := NewSnapshotCommand(nil, fixedResolver{&heldSender{sent}}, formatter, logging.NewLoggerRef(), nil)
	out := filepath.Join(t.TempDir(), "frame.jpg")

	if err := cmd.Run(t.Context(), SnapshotParams{EndpointID: "ep/1", OutputPath: out}); err != nil {
		t.Fatal(err)
	}
	if len(sent.sent) != 1 {
		t.Fatalf("sent %d messages, want one get-snapshot", len(sent.sent))
	}
	request := sent.sent[0]
	if request.Message.Header.Namespace != api.INTERFACE_PORTOS_SNAPSHOT || request.Message.Header.Name != api.INTERFACE_PORTOS_SNAPSHOT_GET_SNAPSHOT {
		t.Fatalf("sent %+v, want get-snapshot", request.Message.Header)
	}
	if got := request.Message.Body["responseFormat"]; got != "BASE64" {
		t.Fatalf("responseFormat = %v, want BASE64", got)
	}
	written, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(written, frame) {
		t.Fatalf("wrote %v (%v), want the held frame", written, err)
	}
	result := formatter.printed[0].(SnapshotResult)
	if !result.Success || result.CapturedAt == nil || !result.CapturedAt.Equal(capturedAt) {
		t.Fatalf("result = %+v", result)
	}
}

func TestSnapshotUnderAHoldReportsALostCamera(t *testing.T) {
	refusal := messages.Refusal(time.Now(), "ep/1", "camera_unavailable", "the held camera stopped (preempted)")
	sent := &scriptedMessages{responses: []*api.MessageRequest{refusal}}
	cmd := NewSnapshotCommand(nil, fixedResolver{&heldSender{sent}}, &recordingFormatter{json: true}, logging.NewLoggerRef(), nil)

	err := cmd.Run(t.Context(), SnapshotParams{EndpointID: "ep/1", OutputPath: filepath.Join(t.TempDir(), "frame.jpg")})
	if err == nil {
		t.Fatal("a lost camera returned no error")
	}
	if got := exitcode.Code(err); got != exitcode.ExitCamera {
		t.Fatalf("exit code = %d, want %d (camera)", got, exitcode.ExitCamera)
	}
}

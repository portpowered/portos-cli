package commands

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/portpowered/portos-cli/internal/api"
)

func TestRecordingContinuesAfterTrackArrivalWhenStartupTimeoutExpires(t *testing.T) {
	started := make(chan struct{})
	close(started)
	timeout := make(chan time.Time, 1)
	completed := make(chan recordingResult, 1)
	timeout <- time.Now()
	completed <- recordingResult{path: "recording.ivf"}
	result, err := waitForRecording(t.Context(), started, completed, timeout)
	if err != nil || result.path != "recording.ivf" {
		t.Fatalf("started recording was cut short: result=%+v err=%v", result, err)
	}
}

func TestRecordingOfferMatchesSchemaAndKeepsSessionAliveForThirtySeconds(t *testing.T) {
	request := recordingSessionRequest(RecorderParams{EndpointID: "camera", SessionID: "test", SessionDomain: "cli", Duration: 30 * time.Second}, "synthetic SDP")
	data, err := json.Marshal(request.Message.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body api.RtcSessionInterfaceInitiateSessionWithOfferMessage
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	if body.Offer.Format == nil || *body.Offer.Format != api.RtcOfferFormatSdp {
		t.Fatal("recording offer uses a format rejected by the RTC schema")
	}
	if body.SessionDuration == nil || *body.SessionDuration < 80 {
		t.Fatal("session lease leaves insufficient time for video startup and 30-second capture")
	}
}

func TestRecordingWaitHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := waitForRecording(ctx, make(chan struct{}), make(chan recordingResult), make(chan time.Time))
	if err != context.Canceled {
		t.Fatalf("cancellation: %v", err)
	}
}

package commands

import (
	"context"
	"fmt"
	"time"
)

type recordingResult struct {
	path      string
	err       error
	codec     string        // videoCodecH264 or videoCodecVP8
	frameRate float64       // from the RTP clock; 0 when unmeasurable
	start     time.Time     // arrival of the first recorded frame
	span      time.Duration // RTP duration of the recorded frames
}

func waitForRecording(ctx context.Context, started <-chan struct{}, completed <-chan recordingResult, firstTrackTimeout <-chan time.Time) (recordingResult, error) {
	// A track already received must win over a simultaneously expired startup timer.
	select {
	case <-started:
		firstTrackTimeout = nil
	default:
	}
	for {
		select {
		case <-ctx.Done():
			return recordingResult{}, ctx.Err()
		case <-firstTrackTimeout:
			return recordingResult{}, fmt.Errorf("timeout waiting for video")
		case result := <-completed:
			return result, result.err
		case <-started:
			firstTrackTimeout = nil
			started = nil
		}
	}
}

package commands

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

const testFrameTicks = 3000 // 30 fps on the 90 kHz clock

// packetizeFrames turns each frame into RTP packets on a 30 fps clock. packets[i] holds frame i's.
func packetizeFrames(t *testing.T, payloader rtp.Payloader, frames [][]byte) [][]*rtp.Packet {
	t.Helper()
	packetizer := rtp.NewPacketizer(600, 96, 1234, payloader, rtp.NewRandomSequencer(), videoRTPClock)
	out := make([][]*rtp.Packet, len(frames))
	for i, frame := range frames {
		out[i] = packetizer.Packetize(frame, testFrameTicks)
	}
	return out
}

func h264Frames(count, keyframeAt, bigAt int) [][]byte {
	frames := make([][]byte, count)
	for i := range frames {
		switch {
		case i == keyframeAt:
			frames[i] = append(append(nal(0x67, 0x42, 0xe0, 0x1f, 0xac), nal(0x68, 0xce, 0x3c, 0x80)...), nal(0x65, 0x88, 0x84, 0x00)...)
		case i == bigAt:
			frames[i] = nal(0x41, make([]byte, 2400)...) // several packets, so one can go missing
		default:
			frames[i] = nal(0x41, 0x9a, 0x24, 0x6c)
		}
	}
	return frames
}

// Packets before the first keyframe and an incomplete frame mid-clip never reach the file, so
// the rate must come from the frames that did: the file then plays for the span it was written.
func TestRateComesFromFramesWrittenNotPacketsReceived(t *testing.T) {
	const keyframeAt, bigAt = 6, 40
	frames := packetizeFrames(t, &codecs.H264Payloader{}, h264Frames(90, keyframeAt, bigAt))
	if len(frames[bigAt]) < 3 {
		t.Fatalf("test frame is only %d packets", len(frames[bigAt]))
	}
	tmp, err := os.Create(filepath.Join(t.TempDir(), "out.h264"))
	if err != nil {
		t.Fatal(err)
	}
	writer := newRecordingH264Writer(tmp)
	for i, packets := range frames {
		for j, packet := range packets {
			if i == bigAt && j == 1 {
				continue // lost in transit
			}
			if err := writer.WriteRTP(packet); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	stamps := writer.WrittenStamps()
	wantFirst, wantLast := frames[keyframeAt][0].Timestamp, frames[89][0].Timestamp
	if len(stamps) == 0 || stamps[0] != wantFirst || stamps[len(stamps)-1] != wantLast {
		t.Fatalf("written %d frames %v..., want first %d last %d", len(stamps), stamps[:min(3, len(stamps))], wantFirst, wantLast)
	}
	// The incomplete frame is dropped (the writer may drop its neighbour too, never more).
	if n := len(stamps); n < 90-keyframeAt-2 || n > 90-keyframeAt-1 {
		t.Fatalf("wrote %d frames of %d after the keyframe", n, 90-keyframeAt)
	}
	for _, stamp := range stamps {
		if stamp == frames[bigAt][0].Timestamp {
			t.Fatal("the incomplete frame was written")
		}
		if stamp < wantFirst {
			t.Fatal("a frame before the first keyframe was written")
		}
	}
	var clock frameClock
	for _, stamp := range stamps {
		clock.observe(stamp)
	}
	rate := clock.frameRate()
	played := float64(len(stamps)) / rate
	truth := float64(wantLast-wantFirst)/videoRTPClock + 1.0/30
	if played < truth-0.1 || played > truth+0.1 {
		t.Fatalf("file plays %.3f s at %.2f fps, written span is %.3f s", played, rate, truth)
	}
	// Counting every received packet's timestamp would have taken the lost frame's slot and the
	// pre-keyframe frames into the count and rate; the written frames do not.
	if rate >= 30 || rate < 28.5 {
		t.Fatalf("rate %.2f, want a little under 30 for the missing frames", rate)
	}
}

func TestVP8WriterReportsOnlyFramesWritten(t *testing.T) {
	key := []byte{0x10, 0x02, 0x00, 0x9d, 0x01, 0x2a, 0x40, 0x01, 0xf0, 0x00, 0x01, 0x02}
	inter := []byte{0x11, 0x02, 0x00, 0x01, 0x02, 0x03}
	var raw [][]byte
	for i := 0; i < 12; i++ {
		switch i {
		case 4:
			raw = append(raw, key)
		default:
			raw = append(raw, inter)
		}
	}
	frames := packetizeFrames(t, &codecs.VP8Payloader{}, raw)
	tmp, err := os.Create(filepath.Join(t.TempDir(), "out.ivf"))
	if err != nil {
		t.Fatal(err)
	}
	writer, err := newRecordingVP8Writer(tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, packets := range frames {
		for _, packet := range packets {
			if err := writer.WriteRTP(packet); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	stamps := writer.WrittenStamps()
	if len(stamps) != 8 || stamps[0] != frames[4][0].Timestamp {
		t.Fatalf("stamps=%v, want 8 frames starting at the keyframe's %d", stamps, frames[4][0].Timestamp)
	}
}

func TestSafePathArgKeepsFFmpegFromReadingOptionsAndProtocols(t *testing.T) {
	for path, want := range map[string]string{
		"-foo.mp4":        "./-foo.mp4",
		"rtmp:x.mp4":      "file:rtmp:x.mp4",
		"C:/x/y.mp4":      "C:/x/y.mp4",
		`C:\x\y.mp4`:      `C:\x\y.mp4`,
		"clip.mp4":        "clip.mp4",
		"./clip.mp4":      "./clip.mp4",
		"dir/a:b.mp4":     "dir/a:b.mp4", // the colon follows a separator, so it is no scheme
		"/tmp/clip.mp4":   "/tmp/clip.mp4",
		"http://h/x.mp4":  "file:http://h/x.mp4",
		"-rf/../../x.mp4": "./-rf/../../x.mp4",
	} {
		if got := safePathArg(path); got != want {
			t.Errorf("safePathArg(%q) = %q, want %q", path, got, want)
		}
	}
	args, err := buildMuxArgs(muxPlan{VideoPath: "v.h264", VideoCodec: videoCodecH264, FrameRate: 25, OutputPath: "-foo.mp4"})
	if err != nil || args[len(args)-1] != "./-foo.mp4" {
		t.Fatalf("mux output argument: %v err=%v", args, err)
	}
}

func TestRecordFailsBeforeCapturingWhenFFmpegIsMissing(t *testing.T) {
	t.Setenv("PATH", "")
	command := &RecordCommand{} // no recorder or config: reaching either would panic
	err := command.Run(t.Context(), RecordParams{EndpointID: "ep", OutputPath: "x.mp4", Duration: 30 * time.Second})
	if !errors.Is(err, errFFmpegMissing) {
		t.Fatalf("err=%v", err)
	}
}

func TestSnapshotNeverRequestsAudio(t *testing.T) {
	params := snapshotRecorderParams("ep", "cli", "s", false)
	if params.Audio || params.EndpointID != "ep" || params.Duration != snapshotRecordDur {
		t.Fatalf("params=%+v", params)
	}
}

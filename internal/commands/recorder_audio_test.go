package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/pion/webrtc/v4/pkg/media/h264reader"
	"github.com/pion/webrtc/v4/pkg/media/oggreader"
	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/logging"
)

// clipSource is the media a fake camera sends: one entry per video access unit (Annex B) and per
// 20 ms Opus packet.
type clipSource struct {
	video [][]byte
	audio [][]byte
	fps   int
}

var startCode = []byte{0, 0, 0, 1}

func nal(header byte, payload ...byte) []byte {
	return append(append(append([]byte{}, startCode...), header), payload...)
}

// syntheticSource has valid-looking NAL framing but no decodable pictures: enough for the
// recorder, which only repackages packets.
func syntheticSource(seconds, fps int) clipSource {
	src := clipSource{fps: fps}
	for i := 0; i < seconds*fps; i++ {
		var unit []byte
		if i%fps == 0 {
			unit = append(append(nal(0x67, 0x42, 0xe0, 0x1f, 0xac), nal(0x68, 0xce, 0x3c, 0x80)...), nal(0x65, 0x88, 0x84, 0x00)...)
		} else {
			unit = nal(0x41, 0x9a, 0x24, 0x6c)
		}
		src.video = append(src.video, unit)
	}
	for i := 0; i < seconds*50; i++ {
		src.audio = append(src.audio, []byte{0xfc, 0xff, 0xfe, byte(i)})
	}
	return src
}

func needFFmpeg(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not on PATH; the muxed-file check needs it", tool)
		}
	}
}

// ffmpegSource encodes real H264 and Opus so the muxed file can be probed.
func ffmpegSource(t *testing.T, seconds, fps int) clipSource {
	t.Helper()
	needFFmpeg(t)
	dir := t.TempDir()
	d := strconv.Itoa(seconds)
	run := func(args ...string) {
		if out, err := exec.Command("ffmpeg", append([]string{"-hide_banner", "-loglevel", "error", "-y"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
		}
	}
	h264Path, oggPath := filepath.Join(dir, "v.h264"), filepath.Join(dir, "a.ogg")
	run("-f", "lavfi", "-i", "testsrc=s=320x240:r="+strconv.Itoa(fps)+":d="+d, "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
		"-g", strconv.Itoa(fps), "-x264-params", "repeat-headers=1:threads=1:slices=1", "-f", "h264", h264Path)
	run("-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:d="+d, "-ac", "2", "-c:a", "libopus", "-frame_duration", "20", "-page_duration", "20000", oggPath)

	src := clipSource{fps: fps}
	data, err := os.ReadFile(h264Path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := h264reader.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	var prefix []byte
	for {
		unit, err := reader.NextNAL()
		if err != nil {
			break
		}
		wire := append(append([]byte{}, startCode...), unit.Data...)
		if code := unit.Data[0] & 31; code == 1 || code == 5 {
			src.video = append(src.video, append(prefix, wire...))
			prefix = nil
		} else {
			prefix = append(prefix, wire...)
		}
	}
	file, err := os.Open(oggPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	ogg, _, err := oggreader.NewWith(file)
	if err != nil {
		t.Fatal(err)
	}
	for {
		page, _, err := ogg.ParseNextPage()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(page) > 0 && !bytes.HasPrefix(page, []byte("OpusTags")) {
			src.audio = append(src.audio, page)
		}
	}
	if len(src.video) < seconds*fps-2 || len(src.audio) < seconds*45 {
		t.Fatalf("source too short: %d frames, %d audio packets", len(src.video), len(src.audio))
	}
	return src
}

// pionCamera answers the recorder's offer in-process and streams src once connected.
type pionCamera struct {
	t          *testing.T
	src        clipSource
	withAudio  bool
	audioDelay time.Duration
	// dropEvery skips every Nth frame but keeps the RTP clock moving, like a camera that stamps
	// 25 fps and delivers fewer frames.
	dropEvery int
	// heard counts the audio packets the client sends the camera (talkback).
	heard atomic.Int64
}

func (c *pionCamera) Send(ctx context.Context, request *api.CreateMessageRequest, _ ...string) (*api.MessageRequest, error) {
	if request.Message.Header.Name != rtcSessionInitiate {
		return &api.MessageRequest{Id: "disconnect", Status: api.SUCCEEDED}, nil
	}
	offer := request.Message.Body["offer"].(map[string]interface{})["value"].(string)
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, err
	}
	video, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeH264, ClockRate: 90000,
		SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
	}, "video", "camera")
	if err != nil {
		return nil, err
	}
	if _, err = pc.AddTrack(video); err != nil {
		return nil, err
	}
	var audio *webrtc.TrackLocalStaticSample
	if c.withAudio {
		if audio, err = webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "audio", "camera"); err != nil {
			return nil, err
		}
		if _, err = pc.AddTrack(audio); err != nil {
			return nil, err
		}
	}
	started := make(chan struct{})
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateConnected {
			close(started)
		}
	})
	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if track.Kind() != webrtc.RTPCodecTypeAudio {
			return
		}
		for {
			if _, _, err := track.ReadRTP(); err != nil {
				return
			}
			c.heard.Add(1)
		}
	})
	if err = pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer}); err != nil {
		return nil, err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return nil, err
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err = pc.SetLocalDescription(answer); err != nil {
		return nil, err
	}
	<-gathered
	c.t.Cleanup(func() { _ = pc.Close() })
	go c.pump(started, video, audio)
	return &api.MessageRequest{Id: "initiate", Status: api.SUCCEEDED, Results: []api.MessageRequestResult{{Message: &api.Message{
		Header: &api.MessageHeader{Namespace: rtcSessionNamespace, Name: rtcAnswerGenerated, Version: "1.0"},
		Body:   map[string]interface{}{"answer": map[string]interface{}{"value": pc.LocalDescription().SDP}},
	}}}}, nil
}

// Hold waits until the caller gives up, as a real session hold does while the subscriber is attached.
func (c *pionCamera) Hold(ctx context.Context, _ string) (*api.MessageRequestHoldEnd, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (c *pionCamera) pump(started <-chan struct{}, video, audio *webrtc.TrackLocalStaticSample) {
	<-started
	frame := time.Second / time.Duration(c.src.fps)
	go func() {
		tick := time.NewTicker(frame)
		defer tick.Stop()
		pending := time.Duration(0)
		for i, unit := range c.src.video {
			<-tick.C
			pending += frame
			if c.dropEvery > 0 && i%c.dropEvery == c.dropEvery-1 {
				continue
			}
			if video.WriteSample(media.Sample{Data: unit, Duration: pending}) != nil {
				return
			}
			pending = 0
		}
	}()
	if audio == nil {
		return
	}
	time.Sleep(c.audioDelay)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for _, packet := range c.src.audio {
		<-tick.C
		if audio.WriteSample(media.Sample{Data: packet, Duration: 20 * time.Millisecond}) != nil {
			return
		}
	}
}

type cameraClient struct {
	apiclient.Client
	camera *pionCamera
}

func (c cameraClient) Messages() apiclient.MessagesClient { return c.camera }

// Relay reports no relay, so the in-process camera connects directly.
func (c cameraClient) Relay() apiclient.RelayClient {
	return relayClient{err: errors.New("no relay in tests")}
}

func recordFromCamera(t *testing.T, camera *pionCamera, params RecorderParams) (*RecorderResult, error) {
	t.Helper()
	camera.t = t
	recorder := NewRecorder(cameraClient{camera: camera}, logging.NewLoggerRef())
	recorder.iceServers = nil
	params.EndpointID, params.SessionID, params.SessionDomain = "camera", "test", "cli"
	return recorder.Run(t.Context(), params)
}

func TestRecorderWritesVideoAndOpusAudioTracks(t *testing.T) {
	camera := &pionCamera{src: syntheticSource(6, 25), withAudio: true, audioDelay: 300 * time.Millisecond}
	result, err := recordFromCamera(t, camera, RecorderParams{Duration: 3 * time.Second, Audio: true})
	if err != nil {
		t.Fatal(err)
	}
	defer result.Remove()
	if !result.HasAudio() || result.VideoCodec != videoCodecH264 {
		t.Fatalf("result=%+v", result)
	}
	if result.FrameRate < 24 || result.FrameRate > 26 {
		t.Fatalf("frame rate %v, want about 25", result.FrameRate)
	}
	if result.VideoSpan < 2*time.Second || result.VideoSpan > 3500*time.Millisecond {
		t.Fatalf("video span %v for a 3 s capture", result.VideoSpan)
	}
	if head, _ := os.ReadFile(result.AudioPath); !bytes.HasPrefix(head, []byte("OggS")) || len(head) < 2000 {
		t.Fatalf("audio file is not a populated Ogg stream (%d bytes)", len(head))
	}
	if offset := result.AudioStart.Sub(result.VideoStart); offset < 150*time.Millisecond || offset > 600*time.Millisecond {
		t.Fatalf("audio started %v after video, want about 300 ms", offset)
	}
}

// A camera slow to send its first keyframe still yields the full duration: the window runs
// from the first recorded frame, and the audio keeps recording to the same end.
func TestRecorderCountsTheDurationFromTheFirstKeyframe(t *testing.T) {
	src := syntheticSource(8, 25)
	for i := 0; i < 50; i++ { // the first keyframe arrives 2 s in
		src.video[i] = nal(0x41, 0x9a, 0x24, 0x6c)
	}
	camera := &pionCamera{src: src, withAudio: true}
	result, err := recordFromCamera(t, camera, RecorderParams{Duration: 3 * time.Second, Audio: true})
	if err != nil {
		t.Fatal(err)
	}
	defer result.Remove()
	if result.VideoSpan < 2800*time.Millisecond || result.VideoSpan > 3500*time.Millisecond {
		t.Fatalf("video span %v for a 3 s capture whose first keyframe came 2 s late", result.VideoSpan)
	}
	if lead := result.VideoStart.Sub(result.AudioStart); lead < 1500*time.Millisecond {
		t.Fatalf("the video started %v after the audio, want about 2 s", lead)
	}
}

func TestRecorderWithoutDeviceAudioSavesVideoOnly(t *testing.T) {
	camera := &pionCamera{src: syntheticSource(6, 25), withAudio: false}
	result, err := recordFromCamera(t, camera, RecorderParams{Duration: 2 * time.Second, Audio: true})
	if err != nil {
		t.Fatalf("a camera without audio must not fail the recording: %v", err)
	}
	defer result.Remove()
	if result.HasAudio() || result.TempPath == "" {
		t.Fatalf("result=%+v", result)
	}
}

func TestRecorderAudioIsNotOfferedUnlessRequested(t *testing.T) {
	camera := &pionCamera{src: syntheticSource(6, 25), withAudio: true}
	result, err := recordFromCamera(t, camera, RecorderParams{Duration: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer result.Remove()
	if result.HasAudio() {
		t.Fatal("snapshot-style recordings must not receive audio")
	}
}

func TestRecorderRejectsAnUnsupportedContainerBeforeCapturing(t *testing.T) {
	camera := &pionCamera{src: syntheticSource(6, 25)}
	start := time.Now()
	_, err := recordFromCamera(t, camera, RecorderParams{Duration: 30 * time.Second, CheckVideoCodec: func(codec string) error {
		return checkContainerSupportsVideo("clip.webm", codec)
	}})
	if err == nil || !strings.Contains(err.Error(), ".mp4") {
		t.Fatalf("err=%v, want a message naming .mp4", err)
	}
	if time.Since(start) > 20*time.Second {
		t.Fatal("the codec check waited for the whole capture")
	}
}

type probed struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		StartTime string `json:"start_time"`
		Duration  string `json:"duration"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func ffprobeFile(t *testing.T, path string) probed {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", "-show_format", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	var p probed
	if err := json.Unmarshal(out, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func seconds(t *testing.T, value string) float64 {
	t.Helper()
	f, err := strconv.ParseFloat(value, 64)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return f
}

func TestRecordedClipPlaysAtRealSpeedWithBothStreams(t *testing.T) {
	needFFmpeg(t)
	want := 3
	if v, err := strconv.Atoi(os.Getenv("PORTOS_RECORD_TEST_SECONDS")); err == nil && v > 0 {
		want = v // set to 30 to prove a full-length clip by hand
	}
	for _, tc := range []struct {
		name, ext, audio string
		dropEvery        int
	}{{"mp4", "mp4", "aac", 0}, {"mkv", "mkv", "opus", 0}, {"mp4 with every 5th frame dropped", "mp4", "aac", 5}} {
		t.Run(tc.name, func(t *testing.T) {
			camera := &pionCamera{src: ffmpegSource(t, want+2, 25), withAudio: true, audioDelay: 200 * time.Millisecond, dropEvery: tc.dropEvery}
			result, err := recordFromCamera(t, camera, RecorderParams{Duration: time.Duration(want) * time.Second, Audio: true})
			if err != nil {
				t.Fatal(err)
			}
			defer result.Remove()
			t.Logf("recorder: fps=%v span=%v video-start-to-audio=%v", result.FrameRate, result.VideoSpan, result.AudioStart.Sub(result.VideoStart))
			out := filepath.Join(t.TempDir(), "clip."+tc.ext)
			command := &RecordCommand{logger: logging.NewLoggerRef()}
			res, err := command.mux(t.Context(), result, out, time.Duration(want)*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if !res.HasAudio || res.VideoCodec != videoCodecH264 || res.AudioCodec != tc.audio {
				t.Fatalf("result=%+v", res)
			}
			info := ffprobeFile(t, out)
			var videoDur, audioDur float64
			names := map[string]string{}
			for _, s := range info.Streams {
				names[s.CodecType] = s.CodecName
				if s.Duration == "" { // Matroska keeps durations in tags, not in the stream
					continue
				}
				if s.CodecType == "video" {
					videoDur = seconds(t, s.Duration)
				} else {
					audioDur = seconds(t, s.Duration)
				}
			}
			if names["video"] != "h264" || names["audio"] != tc.audio {
				t.Fatalf("streams %v, want h264 + %s", names, tc.audio)
			}
			total := seconds(t, info.Format.Duration)
			t.Logf("%s: total %.2f s, video %.2f s, audio %.2f s; ffprobe streams %+v", tc.ext, total, videoDur, audioDur, info.Streams)
			if total < float64(want)-1 || total > float64(want)+1 {
				t.Errorf("duration %.2f s, want %d s +/- 1", total, want)
			}
			if videoDur == 0 || audioDur == 0 {
				return
			}
			if videoDur < float64(want)-0.7 || videoDur > float64(want)+0.7 {
				t.Errorf("video plays %.2f s for a %d s capture", videoDur, want)
			}
			if diff := videoDur - audioDur; diff > 0.5 || diff < -0.5 {
				t.Errorf("video %.2f s and audio %.2f s differ by %.2f s", videoDur, audioDur, diff)
			}
		})
	}
}

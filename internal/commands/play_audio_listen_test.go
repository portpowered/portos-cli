package commands

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/exitcode"
	"github.com/portpowered/portos-cli/internal/hostclient"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/transport"
)

// noon pins play-audio's quiet-hours clock to midday.
func noon(cmd *PlayAudioCommand) *PlayAudioCommand {
	cmd.now = func() time.Time { return time.Date(2026, 11, 13, 12, 0, 0, 0, time.Local) }
	return cmd
}

func newPlayAudio(sender transport.Sender, formatter *recordingFormatter) *PlayAudioCommand {
	return noon(NewPlayAudioCommand(fixedResolver{sender}, formatter, logging.NewLoggerRef()))
}

// listeningHost is a held sender whose hold listens: Capture writes the prepared Ogg file.
type listeningHost struct {
	heldSender
	ogg      []byte
	from     time.Time
	duration time.Duration
}

func (h *listeningHost) Capture(_ context.Context, _ string, from time.Time, d time.Duration) (hostclient.AudioCapture, error) {
	h.from, h.duration = from, d
	file, err := os.CreateTemp("", "listen-*.ogg")
	if err != nil {
		return hostclient.AudioCapture{}, err
	}
	_, _ = file.Write(h.ogg)
	_ = file.Close()
	return hostclient.AudioCapture{Path: file.Name(), From: from, Until: from.Add(d), FirstPacketAt: from.Add(30 * time.Millisecond), Packets: 25}, nil
}

func TestPlayAudioChecksItsFlags(t *testing.T) {
	cmd := newPlayAudio(cloudSender{&scriptedMessages{}}, &recordingFormatter{})
	ogg := writeFile(t, "hi.ogg", []byte("OggS"))
	for name, params := range map[string]PlayAudioParams{
		"no audio":             {EndpointID: "ep/1"},
		"two sources":          {EndpointID: "ep/1", FilePath: ogg, Chirp: true},
		"text without a model": {EndpointID: "ep/1", Text: "hi", SpeechEndpoint: "ep/tts"},
		"voice without text":   {EndpointID: "ep/1", FilePath: ogg, Voice: "alloy"},
		"listen without out":   {EndpointID: "ep/1", FilePath: ogg, Listen: time.Second},
		"out without listen":   {EndpointID: "ep/1", FilePath: ogg, OutputPath: "x.wav"},
		"listen too long":      {EndpointID: "ep/1", FilePath: ogg, Listen: ListenMax + time.Second, OutputPath: "x.wav"},
		"bad output":           {EndpointID: "ep/1", FilePath: ogg, Listen: time.Second, OutputPath: "x.txt"},
		"listen with no hold":  {EndpointID: "ep/1", FilePath: ogg, Listen: time.Second, OutputPath: "x.wav"},
	} {
		if err := cmd.Run(t.Context(), params); exitcode.Code(err) != exitcode.ExitInvalid {
			t.Errorf("%s: %v (exit %d), want exit %d", name, err, exitcode.Code(err), exitcode.ExitInvalid)
		}
	}
}

func TestPlayAudioStaysQuietBeforeNine(t *testing.T) {
	sent := &scriptedMessages{}
	cmd := NewPlayAudioCommand(fixedResolver{cloudSender{sent}}, &recordingFormatter{}, logging.NewLoggerRef())
	cmd.now = func() time.Time { return time.Date(2026, 11, 13, 8, 59, 0, 0, time.Local) }
	file := writeFile(t, "hi.mp3", []byte("ID3"))
	err := cmd.Run(t.Context(), PlayAudioParams{EndpointID: "ep/1", FilePath: file})
	if exitcode.Code(err) != exitcode.ExitInvalid || !strings.Contains(err.Error(), "--allow-quiet-hours") {
		t.Fatalf("08:59: %v, want a quiet-hours refusal", err)
	}
	if len(sent.sent) != 0 {
		t.Fatal("a clip was sent in quiet hours")
	}
	if err := cmd.Run(t.Context(), PlayAudioParams{EndpointID: "ep/1", FilePath: file, AllowQuietHours: true}); err != nil {
		t.Fatalf("--allow-quiet-hours: %v", err)
	}
	cmd.now = func() time.Time { return time.Date(2026, 11, 13, 9, 0, 0, 0, time.Local) }
	if err := cmd.Run(t.Context(), PlayAudioParams{EndpointID: "ep/1", FilePath: file}); err != nil {
		t.Fatalf("09:00: %v", err)
	}
}

func audioGenerated(data []byte, mime string) *api.MessageRequest {
	return &api.MessageRequest{Id: "tts", Status: api.SUCCEEDED, Results: []api.MessageRequestResult{{
		RouteId: "route-1", TargetId: "ep/tts",
		Message: &api.CapabilityResponseMessage{
			Header: &api.MessageHeader{Namespace: api.INTERFACE_PORTOS_SPEECH_AUDIO_GENERATION, Name: api.INTERFACE_PORTOS_SPEECH_AUDIO_GENERATION_AUDIO_GENERATED, Version: "1.0"},
			Body:   map[string]interface{}{"audio": map[string]interface{}{"data": base64.StdEncoding.EncodeToString(data), "mimeType": mime}},
		},
	}}}
}

func TestPlayAudioSpeaksText(t *testing.T) {
	speech := []byte("ID3 spoken")
	sent := &scriptedMessages{responses: []*api.MessageRequest{audioGenerated(speech, "audio/mpeg")}}
	cmd := newPlayAudio(cloudSender{sent}, &recordingFormatter{json: true})
	err := cmd.Run(t.Context(), PlayAudioParams{EndpointID: "ep/1", Text: "Alexa, what time is it?", Voice: "alloy", SpeechEndpoint: "ep/tts", SpeechModel: "tts-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sent.sent) != 2 {
		t.Fatalf("sent %d messages, want generate-speech then play-audio", len(sent.sent))
	}
	ask := sent.sent[0]
	if ask.Target.Id != "ep/tts" || ask.Message.Header.Name != api.INTERFACE_PORTOS_SPEECH_AUDIO_GENERATION_GENERATE_SPEECH {
		t.Fatalf("first message = %+v to %s", ask.Message.Header, ask.Target.Id)
	}
	body := ask.Message.Body
	if body["model"] != "tts-1" || body["text"] != "Alexa, what time is it?" || body["voice"] != "alloy" || body["responseFormat"] != string(api.BinaryResponseFormatBase64) {
		t.Fatalf("generate-speech body = %v", body)
	}
	sent.sent = sent.sent[1:]
	data, mime := sentAudio(t, sent)
	if mime != "audio/mpeg" || string(data) != string(speech) {
		t.Fatalf("played %q as %s, want the speech", data, mime)
	}
}

func TestGeneratedSpeechReadsTheInlineAudio(t *testing.T) {
	data, mime, err := generatedSpeech(audioGenerated([]byte("opus"), "audio/opus; codecs=opus"))
	if err != nil || string(data) != "opus" || mime != "audio/ogg" {
		t.Fatalf("got %q %s %v", data, mime, err)
	}
	if _, _, err := generatedSpeech(audioGenerated(nil, "audio/mpeg")); err == nil {
		t.Fatal("an empty reply was accepted")
	}
	if _, _, err := generatedSpeech(&api.MessageRequest{}); err == nil {
		t.Fatal("a reply without audio-generated was accepted")
	}
}

func TestPlayAudioListensForTheReply(t *testing.T) {
	clip := oggClip(t, 1)
	host := &listeningHost{heldSender: heldSender{&scriptedMessages{}}, ogg: clip}
	formatter := &recordingFormatter{json: true}
	out := filepath.Join(t.TempDir(), "reply.ogg")
	before := time.Now()
	err := newPlayAudio(host, formatter).Run(t.Context(), PlayAudioParams{EndpointID: "ep/1", FilePath: writeFile(t, "hi.ogg", clip), Listen: 3 * time.Second, OutputPath: out})
	if err != nil {
		t.Fatal(err)
	}
	if host.from.Before(before) || host.from.After(time.Now()) {
		t.Fatalf("captured from %v, want the moment the clip was sent", host.from)
	}
	if host.duration < 4*time.Second || host.duration > 4500*time.Millisecond {
		t.Fatalf("captured %v, want the 1 s clip plus 3 s", host.duration)
	}
	result := formatter.printed[0].(PlayAudioResult)
	listen := result.ListenResult
	if listen == nil || listen.OutputPath != out || listen.AudioPackets != 25 || listen.FirstAudioAt == nil {
		t.Fatalf("listen = %+v", listen)
	}
	if result.PlayStartedAt == nil || listen.RecordStartedAt.After(*result.PlayStartedAt) || listen.RecordEndedAt.Before(*result.PlayEndedAt) {
		t.Fatalf("the recording %v..%v does not cover the clip %v..%v", listen.RecordStartedAt, listen.RecordEndedAt, result.PlayStartedAt, result.PlayEndedAt)
	}
	if listen.MaxVolumeDB == nil {
		t.Fatal("no peak level measured")
	}
	written, err := os.ReadFile(out)
	if err != nil || string(written) != string(clip) {
		t.Fatalf("the output is not the captured Ogg: %v", err)
	}
}

func TestPlayAudioChirpIsARisingTone(t *testing.T) {
	needFFmpeg(t)
	data, err := chirpClip(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, length, err := parseOggOpus(data)
	if err != nil || length < 700*time.Millisecond || length > 900*time.Millisecond {
		t.Fatalf("the chirp lasts %v (%v), want the 0.5 s sweep plus 0.3 s of silence", length, err)
	}
	path := writeFile(t, "chirp.ogg", data)
	mean, peak := audioLevels(t.Context(), path)
	if mean == nil || peak == nil || *peak < -12 {
		t.Fatalf("chirp levels mean=%v peak=%v, want a loud tone", mean, peak)
	}
}

func TestParseVolumes(t *testing.T) {
	mean, peak := parseVolumes("[Parsed_volumedetect_0 @ 0x1] mean_volume: -27.3 dB\n[Parsed_volumedetect_0 @ 0x1] max_volume: -6.0 dB\n")
	if mean == nil || peak == nil || *mean != -27.3 || *peak != -6.0 {
		t.Fatalf("mean=%v peak=%v", mean, peak)
	}
	mean, peak = parseVolumes("mean_volume: -inf dB\nmax_volume: -inf dB\n")
	if mean != nil || peak != nil {
		t.Fatal("a silent file reported a level")
	}
}

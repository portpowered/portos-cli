package commands

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/exitcode"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/services/messages"
)

// cloudSender is the cloud path: a Sender that is not a hold.
type cloudSender struct{ messages *scriptedMessages }

func (c cloudSender) Send(ctx context.Context, request *api.CreateMessageRequest, _ string) (*api.MessageRequest, error) {
	return c.messages.Send(ctx, request)
}

func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func sentAudio(t *testing.T, sent *scriptedMessages) ([]byte, string) {
	t.Helper()
	if len(sent.sent) != 1 {
		t.Fatalf("sent %d messages, want one play-audio", len(sent.sent))
	}
	request := sent.sent[0]
	if request.Message.Header.Namespace != api.INTERFACE_PORTOS_MEDIA_PLAYER || request.Message.Header.Name != api.INTERFACE_PORTOS_MEDIA_PLAYER_PLAY_AUDIO {
		t.Fatalf("sent %+v, want play-audio", request.Message.Header)
	}
	audio := request.Message.Body["audio"].(map[string]interface{})
	data, err := base64.StdEncoding.DecodeString(audio["data"].(string))
	if err != nil {
		t.Fatal(err)
	}
	return data, audio["mimeType"].(string)
}

func TestPlayAudioUnderAHoldSendsTheOggClip(t *testing.T) {
	clip := oggClip(t, 1)
	sent := &scriptedMessages{}
	formatter := &recordingFormatter{json: true}
	cmd := noon(NewPlayAudioCommand(fixedResolver{&heldSender{sent}}, formatter, logging.NewLoggerRef()))
	if err := cmd.Run(t.Context(), PlayAudioParams{EndpointID: "ep/1", FilePath: writeFile(t, "hi.ogg", clip)}); err != nil {
		t.Fatal(err)
	}
	data, mime := sentAudio(t, sent)
	if mime != "audio/ogg" || !bytes.Equal(data, clip) {
		t.Fatalf("sent %d bytes of %s, want the Ogg clip as is", len(data), mime)
	}
	result := formatter.printed[0].(PlayAudioResult)
	if !result.Held || result.Seconds < 1 || result.Seconds > 1.1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestPlayAudioUnderAHoldConvertsOtherFormats(t *testing.T) {
	needFFmpeg(t)
	wav := filepath.Join(t.TempDir(), "hi.wav")
	if out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=16000:d=1", wav).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
	sent := &scriptedMessages{}
	cmd := noon(NewPlayAudioCommand(fixedResolver{&heldSender{sent}}, &recordingFormatter{json: true}, logging.NewLoggerRef()))
	if err := cmd.Run(t.Context(), PlayAudioParams{EndpointID: "ep/1", FilePath: wav}); err != nil {
		t.Fatal(err)
	}
	data, mime := sentAudio(t, sent)
	if mime != "audio/ogg" {
		t.Fatalf("sent %s, want audio/ogg", mime)
	}
	if _, length, err := parseOggOpus(data); err != nil || length < time.Second {
		t.Fatalf("the converted clip: %s, %v", length, err)
	}
}

func TestPlayAudioWithoutAHoldSendsTheFileAsIs(t *testing.T) {
	mp3 := []byte("ID3 not really an mp3")
	sent := &scriptedMessages{}
	cmd := noon(NewPlayAudioCommand(fixedResolver{cloudSender{sent}}, &recordingFormatter{json: true}, logging.NewLoggerRef()))
	if err := cmd.Run(t.Context(), PlayAudioParams{EndpointID: "ep/1", FilePath: writeFile(t, "hi.mp3", mp3), Title: "hello"}); err != nil {
		t.Fatal(err)
	}
	data, mime := sentAudio(t, sent)
	if mime != "audio/mpeg" || !bytes.Equal(data, mp3) {
		t.Fatalf("sent %d bytes of %s, want the mp3 as is", len(data), mime)
	}
	if got := sent.sent[0].Message.Body["title"]; got != "hello" {
		t.Fatalf("title = %v", got)
	}
}

func TestPlayAudioRefusals(t *testing.T) {
	cmd := noon(NewPlayAudioCommand(fixedResolver{cloudSender{&scriptedMessages{}}}, &recordingFormatter{}, logging.NewLoggerRef()))
	err := cmd.Run(t.Context(), PlayAudioParams{EndpointID: "ep/1", FilePath: writeFile(t, "hi.txt", []byte("hi"))})
	if exitcode.Code(err) != exitcode.ExitInvalid {
		t.Fatalf("a .txt file: %v (exit %d), want exit %d", err, exitcode.Code(err), exitcode.ExitInvalid)
	}

	busy := messages.Refusal(time.Now(), "ep/1", "talkback_busy", "a clip is already playing")
	cmd = noon(NewPlayAudioCommand(fixedResolver{&heldSender{&scriptedMessages{responses: []*api.MessageRequest{busy}}}}, &recordingFormatter{}, logging.NewLoggerRef()))
	err = cmd.Run(t.Context(), PlayAudioParams{EndpointID: "ep/1", FilePath: writeFile(t, "hi.ogg", oggClip(t, 1))})
	if exitcode.Code(err) != exitcode.ExitBusy {
		t.Fatalf("a busy talkback track: %v (exit %d), want exit %d", err, exitcode.Code(err), exitcode.ExitBusy)
	}
}

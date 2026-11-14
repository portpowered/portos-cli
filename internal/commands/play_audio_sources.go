package commands

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/exitcode"
)

// The chirp is a rising sine sweep, easy to find in a recording, so a bench can measure the
// delay between sending a clip and the camera hearing it.
const (
	ChirpFromHz    = 500
	ChirpToHz      = 4000
	ChirpSeconds   = 0.5
	chirpAmplitude = 0.5
)

// audioMimeTypes maps the file extensions play-audio sends to their MIME types.
var audioMimeTypes = map[string]string{
	".ogg":  "audio/ogg",
	".opus": "audio/ogg",
	".mp3":  "audio/mpeg",
	".wav":  "audio/wav",
	".m4a":  "audio/mp4",
	".aac":  "audio/aac",
	".flac": "audio/flac",
}

// playClip is the audio play-audio sends and its MIME type.
type playClip struct {
	data []byte
	mime string
}

// clip reads, speaks or generates the audio. Under a hold it is always Ogg/Opus.
func (c *PlayAudioCommand) clip(ctx context.Context, params PlayAudioParams, held bool) (playClip, error) {
	switch {
	case params.Chirp:
		data, err := chirpClip(ctx)
		return playClip{data: data, mime: "audio/ogg"}, err
	case params.Text != "":
		data, mime, err := c.speak(ctx, params)
		if err != nil || !held || mime == "audio/ogg" {
			return playClip{data: data, mime: mime}, err
		}
		data, err = bytesToOggOpus(ctx, data, mime)
		return playClip{data: data, mime: "audio/ogg"}, err
	}
	mime, ok := audioMimeTypes[strings.ToLower(filepath.Ext(params.FilePath))]
	if !ok {
		return playClip{}, exitcode.NewInvalidArgument(fmt.Errorf("play-audio takes .ogg, .opus, .mp3, .wav, .m4a, .aac or .flac, not %q", filepath.Ext(params.FilePath)))
	}
	if held && mime != "audio/ogg" {
		data, err := toOggOpus(ctx, params.FilePath)
		return playClip{data: data, mime: "audio/ogg"}, err
	}
	data, err := os.ReadFile(params.FilePath)
	return playClip{data: data, mime: mime}, err
}

// speak asks the speech endpoint to say the text and returns the audio inline.
func (c *PlayAudioCommand) speak(ctx context.Context, params PlayAudioParams) ([]byte, string, error) {
	sender, err := c.resolver.For(ctx, params.SpeechEndpoint, api.INTERFACE_PORTOS_SPEECH_AUDIO_GENERATION, api.INTERFACE_PORTOS_SPEECH_AUDIO_GENERATION_GENERATE_SPEECH)
	if err != nil {
		return nil, "", err
	}
	body := map[string]interface{}{"model": params.SpeechModel, "text": params.Text, "responseFormat": string(api.BinaryResponseFormatBase64)}
	if params.Voice != "" {
		body["voice"] = params.Voice
	}
	messageType := api.INTERFACE_PORTOS_SPEECH_AUDIO_GENERATION_GENERATE_SPEECH_MESSAGE_TYPE
	sync := api.SyncResponseType
	response, err := sender.Send(ctx, &api.CreateMessageRequest{
		Target: api.CreateMessageTarget{Type: api.EndpointTarget, Id: params.SpeechEndpoint},
		Message: api.Message{
			Type:   &messageType,
			Header: &api.MessageHeader{Namespace: api.INTERFACE_PORTOS_SPEECH_AUDIO_GENERATION, Name: api.INTERFACE_PORTOS_SPEECH_AUDIO_GENERATION_GENERATE_SPEECH, Version: "1.0"},
			Body:   body,
		},
		ResponseType: &sync,
	}, uuid.NewString())
	if err != nil {
		return nil, "", fmt.Errorf("generate-speech: %w", err)
	}
	if err := messageRequestFailure(response); err != nil {
		return nil, "", fmt.Errorf("generate-speech: %w", err)
	}
	return generatedSpeech(response)
}

// generatedSpeech reads the inline audio from an audio-generated reply.
func generatedSpeech(response *api.MessageRequest) ([]byte, string, error) {
	for _, result := range response.Results {
		if result.Message == nil || result.Message.Header == nil || result.Message.Header.Name != api.INTERFACE_PORTOS_SPEECH_AUDIO_GENERATION_AUDIO_GENERATED {
			continue
		}
		audio, _ := result.Message.Body["audio"].(map[string]interface{})
		data, _ := audio["data"].(string)
		mime, _ := audio["mimeType"].(string)
		decoded, err := base64.StdEncoding.DecodeString(data)
		if err != nil || len(decoded) == 0 {
			return nil, "", errors.New("generate-speech replied with no inline audio")
		}
		mime = strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0]))
		if mime == "audio/opus" {
			mime = "audio/ogg"
		}
		return decoded, mime, nil
	}
	return nil, "", errors.New("generate-speech replied with no audio-generated result")
}

// chirpClip renders the chirp as 48 kHz stereo Ogg/Opus with ffmpeg, with a little silence after
// it so the encoder flushes the sweep's end.
func chirpClip(ctx context.Context) ([]byte, error) {
	sweep := float64(ChirpToHz-ChirpFromHz) / (2 * ChirpSeconds)
	source := fmt.Sprintf("aevalsrc=%g*sin(2*PI*(%d*t+%g*t*t)):s=48000:d=%g", chirpAmplitude, ChirpFromHz, sweep, ChirpSeconds)
	return renderOggOpus(ctx, "the chirp", []string{"-f", "lavfi", "-i", source, "-af", "apad=pad_dur=0.3"})
}

// bytesToOggOpus converts audio held in memory, such as generated speech, to Ogg/Opus.
func bytesToOggOpus(ctx context.Context, data []byte, mime string) ([]byte, error) {
	ext := ".audio"
	for candidate, known := range audioMimeTypes {
		if known == mime {
			ext = candidate
			break
		}
	}
	dir, err := os.MkdirTemp("", "portos-speech-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "speech"+ext)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, err
	}
	return toOggOpus(ctx, path)
}

// toOggOpus converts an audio file to the 48 kHz stereo Ogg/Opus the held camera plays.
func toOggOpus(ctx context.Context, path string) ([]byte, error) {
	return renderOggOpus(ctx, filepath.Ext(path), []string{"-i", safePathArg(path)})
}

// renderOggOpus runs ffmpeg with the given input arguments and returns the 48 kHz stereo Ogg/Opus
// it writes. what names the input in errors.
func renderOggOpus(ctx context.Context, what string, input []string) ([]byte, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf("play-audio makes Ogg/Opus from %s with ffmpeg, which is not on PATH; install ffmpeg or convert it yourself", what)
	}
	dir, err := os.MkdirTemp("", "portos-play-audio-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	out := filepath.Join(dir, "clip.ogg")
	args := append([]string{"-hide_banner", "-loglevel", "error", "-y"}, input...)
	args = append(args, "-vn", "-ac", "2", "-ar", "48000", "-c:a", "libopus", "-b:a", "64k", "-frame_duration", "20", "-f", "ogg", out)
	if output, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("convert to Ogg/Opus: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return os.ReadFile(out)
}

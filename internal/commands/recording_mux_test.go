package commands

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMuxArgsPerExtension(t *testing.T) {
	h264 := muxPlan{VideoPath: "v.h264", VideoCodec: videoCodecH264, FrameRate: 29.97}
	vp8 := muxPlan{VideoPath: "v.ivf", VideoCodec: videoCodecVP8, FrameRate: 15}
	withAudio := func(p muxPlan) muxPlan { p.AudioPath = "a.ogg"; return p }
	cases := []struct {
		name string
		plan muxPlan
		out  string
		want []string
	}{
		{"mp4 video only", h264, "o.mp4", []string{"-y", "-r", "29.97", "-i", "v.h264", "-map", "0:v:0", "-c:v", "copy", "-movflags", "+faststart", "o.mp4"}},
		{"mp4 with audio becomes AAC", withAudio(h264), "o.MP4", []string{"-y", "-r", "29.97", "-i", "v.h264", "-i", "a.ogg", "-map", "0:v:0", "-map", "1:a:0", "-c:v", "copy", "-c:a", "aac", "-movflags", "+faststart", "o.MP4"}},
		{"mov with audio becomes AAC", withAudio(h264), "o.mov", []string{"-y", "-r", "29.97", "-i", "v.h264", "-i", "a.ogg", "-map", "0:v:0", "-map", "1:a:0", "-c:v", "copy", "-c:a", "aac", "-movflags", "+faststart", "o.mov"}},
		{"mkv copies both", withAudio(h264), "o.mkv", []string{"-y", "-r", "29.97", "-i", "v.h264", "-i", "a.ogg", "-map", "0:v:0", "-map", "1:a:0", "-c:v", "copy", "-c:a", "copy", "o.mkv"}},
		{"webm vp8 keeps Opus and sets the IVF rate with -r", withAudio(vp8), "o.webm", []string{"-y", "-r", "15", "-i", "v.ivf", "-i", "a.ogg", "-map", "0:v:0", "-map", "1:a:0", "-c:v", "copy", "-c:a", "copy", "o.webm"}},
		{"unknown extension encodes audio to AAC", withAudio(h264), "o.mpg", []string{"-y", "-r", "29.97", "-i", "v.h264", "-i", "a.ogg", "-map", "0:v:0", "-map", "1:a:0", "-c:v", "copy", "-c:a", "aac", "o.mpg"}},
	}
	for _, tc := range cases {
		tc.plan.OutputPath = tc.out
		got, err := buildMuxArgs(tc.plan)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got %v\nwant %v", tc.name, got, tc.want)
		}
	}
}

func TestMuxArgsApplyStartOffsetsToTheLaterTrack(t *testing.T) {
	plan := muxPlan{VideoPath: "v.h264", VideoCodec: videoCodecH264, FrameRate: 25, AudioPath: "a.ogg", OutputPath: "o.mp4", AudioOffset: 250 * time.Millisecond}
	got, _ := buildMuxArgs(plan)
	if joined := strings.Join(got, " "); !strings.Contains(joined, "-itsoffset 0.250 -i a.ogg") || strings.Contains(joined, "-itsoffset 0.250 -i v.h264") {
		t.Fatalf("audio offset not applied to the audio input: %v", got)
	}
	plan.AudioOffset, plan.AudioSkip = 0, 400*time.Millisecond
	got, _ = buildMuxArgs(plan)
	if joined := strings.Join(got, " "); !strings.Contains(joined, "-i v.h264 -ss 0.400 -i a.ogg") || strings.Contains(joined, "-itsoffset") {
		t.Fatalf("early audio is not trimmed to the first video frame: %v", got)
	}
}

func TestMuxArgsCapOutputAtTheRequestedDuration(t *testing.T) {
	plan := muxPlan{VideoPath: "v.h264", VideoCodec: videoCodecH264, FrameRate: 25, AudioPath: "a.ogg", OutputPath: "o.mp4", Duration: 30 * time.Second}
	got, _ := buildMuxArgs(plan)
	if n := len(got); n < 3 || got[n-3] != "-t" || got[n-2] != "30.000" || got[n-1] != "o.mp4" {
		t.Fatalf("-t is not an output option before the output path: %v", got)
	}
	plan.Duration = 0
	got, _ = buildMuxArgs(plan)
	if strings.Contains(strings.Join(got, " "), "-t ") {
		t.Fatalf("unexpected cap: %v", got)
	}
}

func TestMuxRejectsVideoTheContainerCannotHold(t *testing.T) {
	for _, tc := range []struct{ out, codec, mention string }{
		{"o.webm", videoCodecH264, ".mp4"},
		{"o.webm", videoCodecH264, ".mkv"},
		{"o.mp4", videoCodecVP8, ".webm"},
		{"o.mov", videoCodecVP8, ".mkv"},
	} {
		err := checkContainerSupportsVideo(tc.out, tc.codec)
		if err == nil || !strings.Contains(err.Error(), tc.mention) {
			t.Errorf("%s with %s: err=%v, want a message naming %s", tc.out, tc.codec, err, tc.mention)
		}
		if _, buildErr := buildMuxArgs(muxPlan{VideoPath: "v", VideoCodec: tc.codec, FrameRate: 25, OutputPath: tc.out}); buildErr == nil {
			t.Errorf("%s with %s: buildMuxArgs accepted it", tc.out, tc.codec)
		}
	}
	for _, ok := range [][2]string{{"o.mp4", videoCodecH264}, {"o.mkv", videoCodecVP8}, {"o.webm", videoCodecVP8}, {"o.mkv", videoCodecH264}} {
		if err := checkContainerSupportsVideo(ok[0], ok[1]); err != nil {
			t.Errorf("%v: %v", ok, err)
		}
	}
}

func TestAudioAlignsToTheFirstVideoFrame(t *testing.T) {
	base := time.Now()
	if offset, skip := audioAlignment(base, base.Add(300*time.Millisecond)); offset != 300*time.Millisecond || skip != 0 {
		t.Fatalf("audio later: offset=%v skip=%v", offset, skip)
	}
	if offset, skip := audioAlignment(base.Add(120*time.Millisecond), base); offset != 0 || skip != 120*time.Millisecond {
		t.Fatalf("audio earlier: offset=%v skip=%v", offset, skip)
	}
}

func TestAudioStoredCodecAndSummary(t *testing.T) {
	if audioStoredCodec("o.mp4") != "aac" || audioStoredCodec("o.mkv") != "opus" || audioStoredCodec("o.webm") != "opus" {
		t.Fatal("stored audio codec per extension")
	}
	if got := recordingSummary("o.mp4", 30.04, videoCodecH264, true); got != "30.0 s, video H264, audio Opus→AAC" {
		t.Fatal(got)
	}
	if got := recordingSummary("o.mkv", 29.96, videoCodecVP8, true); got != "30.0 s, video VP8, audio Opus" {
		t.Fatal(got)
	}
	if got := recordingSummary("o.mp4", 30, videoCodecH264, false); got != "30.0 s, video H264, no audio track" {
		t.Fatal(got)
	}
}

package commands

import (
	"math"
	"testing"
	"time"
)

func stampsAt(start uint32, step uint32, n int) []uint32 {
	out := make([]uint32, n)
	for i := range out {
		out[i] = start + uint32(i)*step
	}
	return out
}

func TestMeasureFrameRateFromRTPTimestamps(t *testing.T) {
	cases := []struct {
		name   string
		stamps []uint32
		want   float64
	}{
		{"30 fps", stampsAt(1000, 3000, 60), 30},
		{"15 fps", stampsAt(0, 6000, 30), 15},
		{"29.97 fps", stampsAt(500, 3003, 60), 29.97},
		{"timestamp wraps", stampsAt(math.MaxUint32-6000, 3000, 20), 30},
		{"a gap of lost frames lowers the mean rate", append(stampsAt(0, 3000, 10), stampsAt(60000, 3000, 10)...), 19.66},
		{"reordered frame is not counted", []uint32{0, 3000, 2000, 6000, 9000}, 30},
		{"one frame", []uint32{5}, 0},
		{"none", nil, 0},
		{"implausible rate", stampsAt(0, 10, 10), 0},
	}
	for _, tc := range cases {
		if got := measureFrameRate(tc.stamps); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestFrameClockDedupesPacketsOfOneFrame(t *testing.T) {
	var clock frameClock
	for _, ts := range []uint32{0, 0, 0, 3000, 3000, 6000, 9000, 9000} {
		clock.observe(ts)
	}
	if len(clock.stamps) != 4 || clock.frameRate() != 30 {
		t.Fatalf("stamps=%v rate=%v", clock.stamps, clock.frameRate())
	}
	if clock.span() != 100*time.Millisecond {
		t.Fatalf("span=%v", clock.span())
	}
}

// A camera that stamps a 30 fps clock but skips every 5th frame delivers 24 fps; the median
// interval says 30 and would play a 30 s capture in 24 s.
func TestFrameRateIsTheMeanOverTheCaptureWhenFramesAreSkipped(t *testing.T) {
	var clock frameClock
	for i := 0; i < 30*30; i++ {
		if i%5 == 4 {
			continue
		}
		clock.observe(uint32(1000 + i*3000))
	}
	if got := clock.frameRate(); got < 23.9 || got > 24.1 {
		t.Fatalf("rate %v, want about 24", got)
	}
	if span := clock.span(); span < 29*time.Second || span > 30*time.Second {
		t.Fatalf("span %v", span)
	}
	// 24 fps over the captured frames lasts the real capture.
	if played := float64(len(clock.stamps)) / clock.frameRate(); played < 29.5 || played > 31 {
		t.Fatalf("playback %v s for a 30 s capture", played)
	}
}

func TestFrameRateIsUnknownWithoutASpan(t *testing.T) {
	if measureFrameRate([]uint32{7, 7}) != 0 || measureFrameRate([]uint32{7}) != 0 {
		t.Fatal("no span, no rate")
	}
}

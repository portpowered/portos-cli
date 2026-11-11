package commands

import "time"

const (
	videoRTPClock = 90000
	minFrameRate  = 1.0
	maxFrameRate  = 120.0
)

// frameClock collects the RTP timestamp of each video frame so the recorder can tell ffmpeg the
// camera's real frame rate. Raw H264 and IVF output carry no usable timestamps of their own.
type frameClock struct {
	stamps []uint32
}

// observe records a packet's timestamp; packets of one frame share it.
func (c *frameClock) observe(timestamp uint32) {
	if n := len(c.stamps); n > 0 && c.stamps[n-1] == timestamp {
		return
	}
	c.stamps = append(c.stamps, timestamp)
}

// frameRate is the mean rate over the whole capture (see measureFrameRate).
func (c *frameClock) frameRate() float64 {
	return measureFrameRate(c.stamps)
}

// span is the RTP time between the first and last observed frame.
func (c *frameClock) span() time.Duration {
	_, ticks := unwrappedSpan(c.stamps)
	return time.Duration(float64(ticks) / videoRTPClock * float64(time.Second))
}

// unwrappedSpan counts the frames that moved the clock forward and the RTP ticks they cover.
// Differences use unsigned 32-bit subtraction, so a wrapped clock is unwrapped; a frame that
// arrives out of order (a backwards step) is not counted.
func unwrappedSpan(stamps []uint32) (frames int, ticks uint64) {
	if len(stamps) == 0 {
		return 0, 0
	}
	frames = 1
	last := stamps[0]
	for _, ts := range stamps[1:] {
		if d := ts - last; d > 0 && d < 1<<31 {
			ticks += uint64(d)
			frames++
			last = ts
		}
	}
	return frames, ticks
}

// measureFrameRate is (frames-1) over the RTP time the frames span. Callers pass the stamps of
// the frames actually written to the file, so the file plays for that span. A camera may stamp
// a 30 fps clock yet deliver fewer frames (a Ring delivers about 24.5), and frames can be lost
// mid-clip; the mean over the span still plays at the true length, where the typical interval
// would not. It reports 0 when there are fewer than two frames, no span, or an implausible rate.
func measureFrameRate(stamps []uint32) float64 {
	frames, ticks := unwrappedSpan(stamps)
	if frames < 2 || ticks == 0 {
		return 0
	}
	return plausibleRate(float64(frames-1) * videoRTPClock / float64(ticks))
}

func plausibleRate(rate float64) float64 {
	if rate < minFrameRate || rate > maxFrameRate {
		return 0
	}
	return float64(int(rate*100+0.5)) / 100
}

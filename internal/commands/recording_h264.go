package commands

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media/ivfwriter"
	"github.com/pion/webrtc/v4/pkg/media/samplebuilder"
)

// recordingH264Writer reorders RTP and drops incomplete access units instead of
// appending missing or late fragments to an unrelated NAL unit.
type recordingH264Writer struct {
	output  io.Writer
	samples *samplebuilder.SampleBuilder
	started bool
	closed  bool
	// written holds the RTP timestamp of every frame that reached the output: packets before the
	// first keyframe and incomplete access units are not in it.
	written []uint32
}

func newRecordingH264Writer(output io.Writer) *recordingH264Writer {
	return &recordingH264Writer{output: output, samples: samplebuilder.New(128, &codecs.H264Packet{}, 90000)}
}

func (w *recordingH264Writer) WriteRTP(packet *rtp.Packet) error {
	if w.closed {
		return io.ErrClosedPipe
	}
	w.samples.Push(packet.Clone())
	return w.writeSamples()
}

func (w *recordingH264Writer) writeSamples() error {
	for sample := w.samples.Pop(); sample != nil; sample = w.samples.Pop() {
		if !w.started {
			// Parameter sets are injected with every keyframe by the RTSP bridge.
			for _, nalu := range bytes.Split(sample.Data, []byte{0, 0, 0, 1}) {
				if len(nalu) > 0 && nalu[0]&31 == 7 {
					w.started = true
					break
				}
			}
			if !w.started {
				continue
			}
		}
		if _, err := w.output.Write(sample.Data); err != nil {
			return err
		}
		w.written = append(w.written, sample.PacketTimestamp)
	}
	return nil
}

// WrittenStamps are the RTP timestamps of the frames written so far, in output order.
func (w *recordingH264Writer) WrittenStamps() []uint32 { return w.written }

func (w *recordingH264Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	w.samples.Flush()
	err := w.writeSamples()
	if closer, ok := w.output.(io.Closer); ok {
		if closeErr := closer.Close(); err == nil {
			err = closeErr
		}
	}
	return err
}

const ivfFrameHeaderSize = 12

// frameCountingFile reports each IVF frame the pion writer emits: it writes a 32 byte file
// header once, then a 12 byte frame header before every frame.
type frameCountingFile struct {
	*os.File
	frames int
}

func (f *frameCountingFile) Write(p []byte) (int, error) {
	if len(p) == ivfFrameHeaderSize {
		f.frames++
	}
	return f.File.Write(p)
}

// recordingVP8Writer is the IVF writer plus the RTP timestamp of each frame it wrote (it drops
// everything before the first keyframe and any frame whose packets were lost).
type recordingVP8Writer struct {
	ivf     *ivfwriter.IVFWriter
	file    *frameCountingFile
	written []uint32
}

func newRecordingVP8Writer(file *os.File) (*recordingVP8Writer, error) {
	counting := &frameCountingFile{File: file}
	ivf, err := ivfwriter.NewWith(counting, ivfwriter.WithCodec(webrtc.MimeTypeVP8))
	if err != nil {
		return nil, fmt.Errorf("create IVF writer: %w", err)
	}
	return &recordingVP8Writer{ivf: ivf, file: counting}, nil
}

func (w *recordingVP8Writer) WriteRTP(packet *rtp.Packet) error {
	before := w.file.frames
	if err := w.ivf.WriteRTP(packet); err != nil {
		return err
	}
	if w.file.frames > before {
		w.written = append(w.written, packet.Timestamp)
	}
	return nil
}

func (w *recordingVP8Writer) Close() error { return w.ivf.Close() }

// WrittenStamps are the RTP timestamps of the frames written so far, in output order.
func (w *recordingVP8Writer) WrittenStamps() []uint32 { return w.written }

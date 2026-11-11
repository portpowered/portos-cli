package commands

import (
	"bytes"
	"testing"

	"github.com/pion/rtp"
)

func TestRecordingReordersFragmentedH264BeforeWriting(t *testing.T) {
	var output bytes.Buffer
	writer := newRecordingH264Writer(&output)
	packets := []*rtp.Packet{
		{Header: rtp.Header{SequenceNumber: 1, Timestamp: 0, Marker: true}, Payload: []byte{0x67, 0x42, 0, 0x1f}},
		{Header: rtp.Header{SequenceNumber: 2, Timestamp: 3000}, Payload: []byte{0x7c, 0x85, 1, 2}},
		{Header: rtp.Header{SequenceNumber: 4, Timestamp: 3000, Marker: true}, Payload: []byte{0x7c, 0x45, 5, 6}},
		{Header: rtp.Header{SequenceNumber: 3, Timestamp: 3000}, Payload: []byte{0x7c, 0x05, 3, 4}},
	}
	for _, packet := range packets {
		if err := writer.WriteRTP(packet); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	want := []byte{0, 0, 0, 1, 0x67, 0x42, 0, 0x1f, 0, 0, 0, 1, 0x65, 1, 2, 3, 4, 5, 6}
	if !bytes.Equal(output.Bytes(), want) {
		t.Fatalf("fragment order corrupts H264: got %x want %x", output.Bytes(), want)
	}
}

func TestRecordingDropsIncompleteH264AccessUnit(t *testing.T) {
	var output bytes.Buffer
	writer := newRecordingH264Writer(&output)
	packets := []*rtp.Packet{
		{Header: rtp.Header{SequenceNumber: 1, Timestamp: 0, Marker: true}, Payload: []byte{0x67, 0x42, 0, 0x1f}},
		{Header: rtp.Header{SequenceNumber: 2, Timestamp: 3000}, Payload: []byte{0x7c, 0x85, 1, 2}},
		{Header: rtp.Header{SequenceNumber: 4, Timestamp: 3000, Marker: true}, Payload: []byte{0x7c, 0x45, 5, 6}},
		{Header: rtp.Header{SequenceNumber: 5, Timestamp: 6000, Marker: true}, Payload: []byte{0x65, 7, 8}},
	}
	for _, packet := range packets {
		if err := writer.WriteRTP(packet); err != nil {
			t.Fatal(err)
		}
	}
	// Advance the bounded reorder window past the missing fragment.
	for sequence := uint16(6); sequence < 140; sequence++ {
		if err := writer.WriteRTP(&rtp.Packet{Header: rtp.Header{SequenceNumber: sequence, Timestamp: uint32(sequence) * 3000, Marker: true}, Payload: []byte{0x65, 7, 8}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(output.Bytes(), []byte{0x65, 1, 2}) || !bytes.Contains(output.Bytes(), []byte{0x65, 7, 8}) {
		t.Fatal("incomplete frame escaped or complete later frames were lost")
	}
}

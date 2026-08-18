package mssqltypes

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

// recordingWriter keeps each Write separate: CopyBuffer's contract is that
// write boundaries carry TDS packet boundaries, since the agent-side proxy
// decodes packets from the buffer it is handed.
type recordingWriter struct {
	writes [][]byte
	failAt int // 1-based write index to fail on; 0 never fails
}

var errWriteRejected = errors.New("write rejected")

func (w *recordingWriter) Write(p []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), p...))
	if w.failAt > 0 && len(w.writes) == w.failAt {
		return 0, errWriteRejected
	}
	return len(p), nil
}

func TestCopyBufferSplitsOnPacketBoundaries(t *testing.T) {
	first := New(PacketSQLBatchType, []byte("SELECT 1")).Encode()
	second := New(PacketSQLBatchType, []byte("SELECT 2")).Encode()

	var dst recordingWriter
	if err := CopyBuffer(&dst, bytes.NewReader(append(first, second...))); err != nil {
		t.Fatalf("CopyBuffer: %v", err)
	}
	if len(dst.writes) != 2 {
		t.Fatalf("want one write per packet (2), got %d", len(dst.writes))
	}
	if !bytes.Equal(dst.writes[0], first) || !bytes.Equal(dst.writes[1], second) {
		t.Error("packets were not forwarded byte-for-byte on their own writes")
	}
}

// Unlike DecodeFull, which slices by a caller-supplied maximum size, CopyBuffer
// honours each packet's own length header. Packets of differing sizes in one
// stream must therefore split correctly.
func TestCopyBufferHonoursPerPacketLength(t *testing.T) {
	small := New(PacketSQLBatchType, []byte("a")).Encode()
	large := New(PacketSQLBatchType, bytes.Repeat([]byte("b"), 900)).Encode()

	var dst recordingWriter
	if err := CopyBuffer(&dst, bytes.NewReader(append(small, large...))); err != nil {
		t.Fatalf("CopyBuffer: %v", err)
	}
	if len(dst.writes) != 2 {
		t.Fatalf("want 2 writes, got %d", len(dst.writes))
	}
	if len(dst.writes[0]) != len(small) || len(dst.writes[1]) != len(large) {
		t.Errorf("packet sizes not preserved: got %d and %d, want %d and %d",
			len(dst.writes[0]), len(dst.writes[1]), len(small), len(large))
	}
}

// A clean end-of-stream on a packet boundary is the client closing its socket.
func TestCopyBufferCleanEOF(t *testing.T) {
	var dst recordingWriter
	if err := CopyBuffer(&dst, bytes.NewReader(nil)); err != nil {
		t.Fatalf("clean EOF should not error, got %v", err)
	}
	if len(dst.writes) != 0 {
		t.Fatalf("want no writes, got %d", len(dst.writes))
	}
}

// A stream cut mid-packet must error rather than forward partial bytes.
func TestCopyBufferTruncatedPacketErrors(t *testing.T) {
	full := New(PacketSQLBatchType, []byte("SELECT 1")).Encode()
	var dst recordingWriter
	if err := CopyBuffer(&dst, bytes.NewReader(full[:len(full)-3])); err == nil {
		t.Fatal("want an error for a truncated packet, got nil")
	}
	if len(dst.writes) != 0 {
		t.Fatalf("truncated packet must not be forwarded, got %d writes", len(dst.writes))
	}
}

// A length field below the 8-byte header used to underflow the unsigned
// subtraction, leaving the reader blocked waiting for ~64 KiB of payload that
// never arrives. It must be rejected outright.
func TestDecodeRejectsUndersizedLength(t *testing.T) {
	for _, total := range []uint16{0, 1, 7} {
		pkt := New(PacketSQLBatchType, nil).Encode()
		binary.BigEndian.PutUint16(pkt[2:4], total)
		if _, err := Decode(bytes.NewReader(pkt)); err == nil {
			t.Errorf("length=%d: want an error, got nil", total)
		}
	}
}

// A write failure must abort the copy rather than silently dropping the rest
// of the client's stream.
func TestCopyBufferPropagatesWriteError(t *testing.T) {
	first := New(PacketSQLBatchType, []byte("one")).Encode()
	second := New(PacketSQLBatchType, []byte("two")).Encode()

	dst := recordingWriter{failAt: 1}
	if err := CopyBuffer(&dst, bytes.NewReader(append(first, second...))); !errors.Is(err, errWriteRejected) {
		t.Fatalf("want the writer's error, got %v", err)
	}
	if len(dst.writes) != 1 {
		t.Fatalf("copy continued past a failed write: %d writes", len(dst.writes))
	}
}

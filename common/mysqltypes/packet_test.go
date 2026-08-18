package mysqltypes

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// packetWriter records each Write as a separate entry. The whole point of
// CopyBuffer is that write boundaries carry protocol packet boundaries, so
// asserting on the boundaries is asserting on the contract.
type packetWriter struct {
	writes [][]byte
	failAt int // 1-based write index to fail on; 0 never fails
}

var errWriteRejected = errors.New("write rejected")

func (w *packetWriter) Write(p []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), p...))
	if w.failAt > 0 && len(w.writes) == w.failAt {
		return 0, errWriteRejected
	}
	return len(p), nil
}

// encode builds a wire packet with the given sequence id and payload.
func encode(seq byte, payload []byte) []byte {
	p := &Packet{Seq: seq, Frame: payload}
	return p.Encode()
}

func TestCopyBufferSplitsOnPacketBoundaries(t *testing.T) {
	// Three packets arriving as one contiguous read, which is what a real
	// TCP stream does when a client pipelines commands.
	stream := bytes.Join([][]byte{
		encode(0, []byte("first")),
		encode(1, []byte("second-packet")),
		encode(2, nil),
	}, nil)

	var dst packetWriter
	if err := CopyBuffer(&dst, bytes.NewReader(stream)); err != nil {
		t.Fatalf("CopyBuffer: %v", err)
	}

	if len(dst.writes) != 3 {
		t.Fatalf("want one write per packet (3), got %d", len(dst.writes))
	}
	want := [][]byte{
		encode(0, []byte("first")),
		encode(1, []byte("second-packet")),
		encode(2, nil),
	}
	for i := range want {
		if !bytes.Equal(dst.writes[i], want[i]) {
			t.Errorf("write[%d] = % X, want % X", i, dst.writes[i], want[i])
		}
	}
}

// A packet split across several reads must still emit exactly one write:
// the agent-side proxy decodes one packet per write, so a partial write
// desynchronises it.
func TestCopyBufferReassemblesSplitPacket(t *testing.T) {
	full := encode(7, bytes.Repeat([]byte("x"), 300))

	var dst packetWriter
	// iotest-style drip feed: one byte at a time.
	if err := CopyBuffer(&dst, iotestOneByteReader(full)); err != nil {
		t.Fatalf("CopyBuffer: %v", err)
	}
	if len(dst.writes) != 1 {
		t.Fatalf("want 1 write for 1 packet, got %d", len(dst.writes))
	}
	if !bytes.Equal(dst.writes[0], full) {
		t.Errorf("reassembled packet mismatch")
	}
}

// Sequence ids must survive the round trip: MySQL rejects a handshake whose
// sequence does not advance as expected.
func TestPacketRoundTripPreservesSequence(t *testing.T) {
	for _, seq := range []byte{0, 1, 42, 255} {
		pkt, err := Decode(bytes.NewReader(encode(seq, []byte("payload"))))
		if err != nil {
			t.Fatalf("Decode(seq=%d): %v", seq, err)
		}
		if pkt.Seq != seq {
			t.Errorf("Seq = %d, want %d", pkt.Seq, seq)
		}
		if string(pkt.Frame) != "payload" {
			t.Errorf("Frame = %q, want %q", pkt.Frame, "payload")
		}
	}
}

// A zero-length payload is legal MySQL framing (an empty OK-ish packet), and
// must produce a write rather than being swallowed as EOF.
func TestCopyBufferEmptyPayloadIsAPacket(t *testing.T) {
	var dst packetWriter
	if err := CopyBuffer(&dst, bytes.NewReader(encode(0, nil))); err != nil {
		t.Fatalf("CopyBuffer: %v", err)
	}
	if len(dst.writes) != 1 {
		t.Fatalf("want 1 write, got %d", len(dst.writes))
	}
}

// A clean end-of-stream on a packet boundary is not an error: the client
// simply closed its socket.
func TestCopyBufferCleanEOF(t *testing.T) {
	var dst packetWriter
	if err := CopyBuffer(&dst, bytes.NewReader(nil)); err != nil {
		t.Fatalf("clean EOF should not error, got %v", err)
	}
	if len(dst.writes) != 0 {
		t.Fatalf("want no writes, got %d", len(dst.writes))
	}
}

// A stream cut mid-packet is a real error: forwarding the partial bytes would
// desync the upstream decoder.
func TestCopyBufferTruncatedPacketErrors(t *testing.T) {
	full := encode(0, []byte("abcdefgh"))
	var dst packetWriter
	err := CopyBuffer(&dst, bytes.NewReader(full[:len(full)-3]))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("want io.ErrUnexpectedEOF, got %v", err)
	}
	if len(dst.writes) != 0 {
		t.Fatalf("truncated packet must not be forwarded, got %d writes", len(dst.writes))
	}
}

// A header cut mid-way is equally fatal.
func TestCopyBufferTruncatedHeaderErrors(t *testing.T) {
	var dst packetWriter
	err := CopyBuffer(&dst, bytes.NewReader([]byte{0x05, 0x00}))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("want io.ErrUnexpectedEOF, got %v", err)
	}
}

// A write failure must abort the copy rather than silently dropping the rest
// of the client's stream.
func TestCopyBufferPropagatesWriteError(t *testing.T) {
	stream := append(encode(0, []byte("one")), encode(1, []byte("two"))...)
	dst := packetWriter{failAt: 1}
	err := CopyBuffer(&dst, bytes.NewReader(stream))
	if !errors.Is(err, errWriteRejected) {
		t.Fatalf("want the writer's error, got %v", err)
	}
	if len(dst.writes) != 1 {
		t.Fatalf("copy continued past a failed write: %d writes", len(dst.writes))
	}
}

// The 3-byte length field caps a payload at 0xFFFFFF, so no encodable packet
// can exceed MaxPacketSize — the bound exists to reject a desynced stream, not
// a legitimately large one.
func TestMaxPacketSizeMatchesWireLimit(t *testing.T) {
	if MaxPacketSize != 1<<24-1 {
		t.Fatalf("MaxPacketSize = %d, want the 3-byte length maximum %d", MaxPacketSize, 1<<24-1)
	}
}

// oneByteReader yields a single byte per Read, exercising the reassembly path.
type oneByteReader struct {
	data []byte
	pos  int
}

func iotestOneByteReader(b []byte) io.Reader { return &oneByteReader{data: b} }

func (r *oneByteReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = r.data[r.pos]
	r.pos++
	return 1, nil
}

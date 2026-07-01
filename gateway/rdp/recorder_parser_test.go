package rdp

import (
	"os"
	"sync"
	"testing"
	"time"
)

// TestRecorderDegradedNilParserPaths covers the recorder's degraded mode: when
// per-session parser creation fails, the recorder must keep working with a nil
// parser — RecordOutput and Close must be safe no-ops for bitmap parsing, never
// a panic (DEP-47 hardening).
func TestRecorderDegradedNilParserPaths(t *testing.T) {
	r := &RDPSessionRecorder{
		sessionID: "test-nil-parser",
		labels:    map[string]string{},
		startTime: time.Now().UTC(),
		// parser and tmpFile intentionally nil: parser creation failure +
		// temp-file creation failure is the worst-case degraded recorder.
	}

	pdu := fastPathBitmapPDU(t, testRect{x: 0, y: 0, w: 8, h: 8, bgr: white})

	// Must not panic and must not accumulate an unbounded buffer.
	for i := 0; i < 3; i++ {
		r.RecordOutput(pdu)
	}
	if len(r.outputBuffer) != 0 {
		t.Fatalf("nil-parser recorder must not buffer output, got %d bytes", len(r.outputBuffer))
	}

	// parseAndStorePDU with nil parser/tmpFile must be a no-op.
	r.parseAndStorePDU(pdu)
	if r.bitmapCount != 0 {
		t.Fatalf("nil-parser recorder must not record bitmaps, got %d", r.bitmapCount)
	}

	// Close must be safe and idempotent in degraded mode.
	r.Close(nil)
	r.Close(nil)
}

// TestRecorderPipelineExtractsBitmapsFromWire is the recorder happy-path test:
// raw server->client wire bytes go through framing (GetPduSize), WASM parsing,
// bitmap extraction and land as JSONL events in the temp file — with the new
// per-session parser instance. Covers chunked delivery (a PDU split across
// RecordOutput calls, exercising the tail buffer) and Fast-Path fragment
// reassembly (state that is per-instance in the WASM module).
func TestRecorderPipelineExtractsBitmapsFromWire(t *testing.T) {
	r := NewRDPSessionRecorder("sid-pipeline", "org", "uid", "user", "mail", "conn", "")
	if r.parser == nil || r.tmpFile == nil {
		t.Fatal("recorder must have its own parser and temp file")
	}
	tmpName := r.tmpFile.Name()
	t.Cleanup(func() {
		_ = r.parser.Close()
		_ = r.tmpFile.Close()
		_ = os.Remove(tmpName)
	})

	// 1. One complete PDU delivered whole.
	r.RecordOutput(fastPathBitmapPDU(t, testRect{x: 16, y: 8, w: 10, h: 6, bgr: white}))
	if r.bitmapCount != 1 {
		t.Fatalf("after complete PDU: bitmapCount=%d, want 1", r.bitmapCount)
	}

	// 2. The same kind of PDU split byte-wise across two RecordOutput calls:
	// the first chunk must sit in the tail buffer, the second completes it.
	pdu := fastPathBitmapPDU(t, testRect{x: 32, y: 16, w: 12, h: 4, bgr: magenta})
	r.RecordOutput(pdu[:len(pdu)/2])
	if r.bitmapCount != 1 {
		t.Fatalf("half a PDU must not yield a bitmap, bitmapCount=%d", r.bitmapCount)
	}
	r.RecordOutput(pdu[len(pdu)/2:])
	if r.bitmapCount != 2 {
		t.Fatalf("after completing split PDU: bitmapCount=%d, want 2", r.bitmapCount)
	}

	// 3. A bitmap update fragmented across three Fast-Path PDUs: only the
	// Last fragment yields the bitmap (reassembly state inside this
	// recorder's own WASM instance).
	for _, frag := range fragmentedFastPathBitmapPDU(t, 3, testRect{x: 48, y: 24, w: 14, h: 6, bgr: white}) {
		r.RecordOutput(frag)
	}
	if r.bitmapCount != 3 {
		t.Fatalf("after fragmented PDU: bitmapCount=%d, want 3", r.bitmapCount)
	}

	// Canvas dimension tracking follows the furthest extents seen.
	if int(r.maxWidth) != 48+14 || int(r.maxHeight) != 24+6 {
		t.Fatalf("canvas extents = %dx%d, want %dx%d", r.maxWidth, r.maxHeight, 48+14, 24+6)
	}

	// The temp file must contain one JSONL event per bitmap.
	raw, err := os.ReadFile(tmpName)
	if err != nil {
		t.Fatalf("reading temp file: %v", err)
	}
	lines := 0
	for _, b := range raw {
		if b == '\n' {
			lines++
		}
	}
	if lines != 3 {
		t.Fatalf("temp file has %d events, want 3", lines)
	}
}

// TestRecorderOwnsIsolatedParserInstances ensures every recorder gets its own
// parser instance (never a shared one) and that concurrent recorders parse
// their own streams correctly under -race — the DEP-47 crash was recorders
// sharing one WASM instance across sessions.
func TestRecorderOwnsIsolatedParserInstances(t *testing.T) {
	const recorders = 4
	made := make([]*RDPSessionRecorder, recorders)
	for i := range made {
		made[i] = NewRDPSessionRecorder("sid", "org", "uid", "user", "mail", "conn", "")
		if made[i].parser == nil {
			t.Fatal("recorder must own a parser instance")
		}
		if made[i].tmpFile != nil {
			// Do not let Close reach the DB persistence path in this unit test.
			name := made[i].tmpFile.Name()
			_ = made[i].tmpFile.Close()
			made[i].tmpFile = nil
			t.Cleanup(func() { _ = os.Remove(name) })
		}
	}
	for i := 0; i < recorders; i++ {
		for j := i + 1; j < recorders; j++ {
			if made[i].parser == made[j].parser {
				t.Fatal("recorders must not share a parser instance")
			}
		}
	}

	var wg sync.WaitGroup
	for i, r := range made {
		wg.Add(1)
		go func(i int, r *RDPSessionRecorder) {
			defer wg.Done()
			pdu := fastPathBitmapPDU(t, testRect{x: 8 * i, y: 4 * i, w: 8, h: 8, bgr: white})
			for round := 0; round < 20; round++ {
				r.RecordOutput(pdu)
			}
			r.Close(nil)
		}(i, r)
	}
	wg.Wait()
}

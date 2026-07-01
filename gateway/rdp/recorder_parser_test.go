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

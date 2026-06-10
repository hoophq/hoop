package rdp

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/rdp/analyzer"
	"github.com/hoophq/hoop/gateway/rdp/parser"
)

// tpktPDU builds a minimal TPKT-framed PDU (0x03 0x00 len_hi len_lo payload).
// The WASM framer sizes TPKT PDUs but Parse extracts no bitmaps from them, so
// these exercise the hold/flush path without OCR.
func tpktPDU(payload []byte) []byte {
	total := 4 + len(payload)
	out := []byte{0x03, 0x00, byte(total >> 8), byte(total & 0xff)}
	return append(out, payload...)
}

// gateHarness collects forwarded bytes and detections from a gate under test.
type gateHarness struct {
	mu         sync.Mutex
	forwarded  []byte
	detected   []*analyzer.SnapshotResult
	overloaded []int
	fwdErr     error
}

func (h *gateHarness) onOverload(dropped int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.overloaded = append(h.overloaded, dropped)
}

func (h *gateHarness) overloads() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.overloaded)
}

func (h *gateHarness) setForwardErr(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fwdErr = err
}

func (h *gateHarness) forward(data []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.fwdErr != nil {
		return h.fwdErr
	}
	h.forwarded = append(h.forwarded, data...)
	return nil
}

func (h *gateHarness) onDetection(res *analyzer.SnapshotResult) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.detected = append(h.detected, res)
}

func (h *gateHarness) forwardedBytes() []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]byte(nil), h.forwarded...)
}

func (h *gateHarness) detections() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.detected)
}

func newTestGate(t *testing.T, h *gateHarness, analyze analyzeFunc) *PIIGate {
	t.Helper()
	gate, err := NewPIIGate(context.Background(), PIIGateConfig{
		SessionID:       "sid-test",
		Presidio:        analyzer.NewPresidioClient("http://unused.invalid"),
		Params:          analyzer.AnalysisParams{ScoreThreshold: 0.9},
		Forward:         h.forward,
		OnDetection:     h.onDetection,
		OnOverload:      h.onOverload,
		analyzeOverride: analyze,
	})
	if err != nil {
		t.Fatalf("NewPIIGate: %v", err)
	}
	t.Cleanup(gate.Close)
	return gate
}

// waitFor polls until cond returns true or the deadline expires.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if cond() {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for %s", what)
		case <-time.After(2 * time.Millisecond):
		}
	}
}

func TestPIIGate_ForwardsNonBitmapPDUs(t *testing.T) {
	h := &gateHarness{}
	gate := newTestGate(t, h, func(context.Context, []byte, int, int, []analyzer.YBand, string, int, float64, *analyzer.PresidioClient, analyzer.AnalysisParams) (*analyzer.SnapshotResult, error) {
		t.Error("analyze must not be called for PDUs without bitmaps")
		return &analyzer.SnapshotResult{}, nil
	})

	pdu := tpktPDU([]byte{0xde, 0xad, 0xbe, 0xef})
	gate.Ingest(pdu)

	waitFor(t, "pdu forwarded", func() bool { return bytes.Equal(h.forwardedBytes(), pdu) })
}

func TestPIIGate_HoldsPartialPDUUntilComplete(t *testing.T) {
	h := &gateHarness{}
	gate := newTestGate(t, h, nil)

	pdu := tpktPDU([]byte{1, 2, 3, 4, 5, 6})
	gate.Ingest(pdu[:3]) // incomplete header+payload
	time.Sleep(50 * time.Millisecond)
	if got := h.forwardedBytes(); len(got) != 0 {
		t.Fatalf("partial PDU must be held, got %d forwarded bytes", len(got))
	}

	gate.Ingest(pdu[3:])
	waitFor(t, "completed pdu forwarded", func() bool { return bytes.Equal(h.forwardedBytes(), pdu) })
}

func TestPIIGate_PreservesOrderAcrossBatches(t *testing.T) {
	h := &gateHarness{}
	gate := newTestGate(t, h, nil)

	var want []byte
	for i := 0; i < 50; i++ {
		pdu := tpktPDU([]byte{byte(i), byte(i + 1), byte(i + 2)})
		want = append(want, pdu...)
		gate.Ingest(pdu)
	}

	waitFor(t, "all pdus forwarded in order", func() bool { return bytes.Equal(h.forwardedBytes(), want) })
}

// ingestSyntheticBitmap simulates a composited bitmap region so the analysis
// path runs without needing a real Fast-Path bitmap PDU. It manipulates the
// gate under its own lock exactly like ingestPDULocked does.
func ingestSyntheticBitmap(gate *PIIGate, y, height int) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	// 24bpp raw white patch, 64px wide.
	const w = 64
	data := bytes.Repeat([]byte{0xff}, w*height*3)
	gate.compositeLocked(parser.BitmapRect{
		X: 0, Y: uint16(y), Width: w, Height: uint16(height), BitsPerPixel: 24, Compressed: false,
	}, data)
}

func TestPIIGate_KillsOnDetection(t *testing.T) {
	h := &gateHarness{}
	gate := newTestGate(t, h, func(_ context.Context, _ []byte, _, _ int, bands []analyzer.YBand, sid string, _ int, _ float64, _ *analyzer.PresidioClient, _ analyzer.AnalysisParams) (*analyzer.SnapshotResult, error) {
		if len(bands) == 0 {
			return &analyzer.SnapshotResult{}, nil
		}
		return &analyzer.SnapshotResult{
			Counts:     map[string]int64{"EMAIL_ADDRESS": 1},
			Detections: []models.RDPEntityDetection{{SessionID: sid, EntityType: "EMAIL_ADDRESS", Score: 1}},
		}, nil
	})

	// A PDU plus a dirty region: the analysis must detect and kill.
	ingestSyntheticBitmap(gate, 10, 20)
	gate.Ingest(tpktPDU([]byte{0xaa, 0xbb}))

	waitFor(t, "detection callback", func() bool { return h.detections() == 1 })
	if got := h.forwardedBytes(); len(got) != 0 {
		t.Errorf("held bytes must be dropped on detection, got %d forwarded", len(got))
	}
	if !gate.Killed() {
		t.Errorf("gate must report killed")
	}

	// After the kill, further ingests are discarded.
	gate.Ingest(tpktPDU([]byte{0xcc}))
	time.Sleep(50 * time.Millisecond)
	if got := h.forwardedBytes(); len(got) != 0 {
		t.Errorf("post-kill ingest must not forward, got %d bytes", len(got))
	}
	if h.detections() != 1 {
		t.Errorf("OnDetection must fire exactly once, got %d", h.detections())
	}
}

func TestPIIGate_CleanAnalysisForwards(t *testing.T) {
	h := &gateHarness{}
	var analyzed int
	var analyzedMu sync.Mutex
	gate := newTestGate(t, h, func(_ context.Context, fb []byte, fbW, fbH int, bands []analyzer.YBand, _ string, _ int, _ float64, _ *analyzer.PresidioClient, _ analyzer.AnalysisParams) (*analyzer.SnapshotResult, error) {
		analyzedMu.Lock()
		analyzed++
		analyzedMu.Unlock()
		if len(fb) < fbW*fbH*4 {
			return nil, fmt.Errorf("short framebuffer in analysis: %d for %dx%d", len(fb), fbW, fbH)
		}
		return &analyzer.SnapshotResult{Bands: len(bands)}, nil
	})

	ingestSyntheticBitmap(gate, 0, 32)
	pdu := tpktPDU([]byte{0x01, 0x02})
	gate.Ingest(pdu)

	waitFor(t, "clean batch forwarded", func() bool { return bytes.Equal(h.forwardedBytes(), pdu) })
	analyzedMu.Lock()
	defer analyzedMu.Unlock()
	if analyzed == 0 {
		t.Errorf("analysis must run when bands are dirty")
	}
}

func TestPIIGate_AnalysisErrorFailsOpen(t *testing.T) {
	h := &gateHarness{}
	gate := newTestGate(t, h, func(context.Context, []byte, int, int, []analyzer.YBand, string, int, float64, *analyzer.PresidioClient, analyzer.AnalysisParams) (*analyzer.SnapshotResult, error) {
		return nil, fmt.Errorf("presidio is down")
	})

	ingestSyntheticBitmap(gate, 0, 16)
	pdu := tpktPDU([]byte{0x42})
	gate.Ingest(pdu)

	waitFor(t, "fail-open forward", func() bool { return bytes.Equal(h.forwardedBytes(), pdu) })
	if h.detections() != 0 {
		t.Errorf("no detection expected on analyzer error")
	}
}

func TestPIIGate_UnknownBytesAreNeverDropped(t *testing.T) {
	h := &gateHarness{}
	gate := newTestGate(t, h, nil)

	// 0xFE has action bits 0x02: neither Fast-Path (0x00) nor TPKT (0x03).
	// The framer skips such bytes one at a time; they must still be forwarded
	// to the client in order — the gate never drops data. The final byte
	// stays buffered (a single byte cannot be framed) until more data or
	// session end.
	garbage := bytes.Repeat([]byte{0xfe}, 100)
	gate.Ingest(garbage)

	waitFor(t, "unknown bytes forwarded", func() bool { return bytes.Equal(h.forwardedBytes(), garbage[:99]) })
}

func TestPIIGate_PseudoTPKTGarbageIsForwardedFramed(t *testing.T) {
	h := &gateHarness{}
	gate := newTestGate(t, h, nil)

	// 0xFF has action bits 0x03 and is framed as a TPKT PDU with the length
	// taken from bytes 2-3 (0xFFFF). A complete pseudo-PDU must be forwarded;
	// the remainder stays buffered as a partial PDU awaiting more data.
	garbage := bytes.Repeat([]byte{0xff}, 0xFFFF+10)
	gate.Ingest(garbage)

	waitFor(t, "framed garbage forwarded", func() bool { return bytes.Equal(h.forwardedBytes(), garbage[:0xFFFF]) })
}

func TestPIIGate_BacklogOverflowFailsClosed(t *testing.T) {
	h := &gateHarness{}
	// A stuck analyzer: blocks until the test ends, so held bytes accumulate.
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	gate := newTestGate(t, h, func(ctx context.Context, _ []byte, _, _ int, _ []analyzer.YBand, _ string, _ int, _ float64, _ *analyzer.PresidioClient, _ analyzer.AnalysisParams) (*analyzer.SnapshotResult, error) {
		select {
		case <-block:
		case <-ctx.Done():
		}
		return &analyzer.SnapshotResult{}, nil
	})

	// Get the analyzer stuck on a first dirty batch.
	ingestSyntheticBitmap(gate, 0, 16)
	gate.Ingest(tpktPDU([]byte{0x01}))

	// Flood past the cap while analysis is stuck. Each pseudo-TPKT PDU is
	// 64KiB; push > maxHeldBytes worth.
	bigPDU := tpktPDU(bytes.Repeat([]byte{0xab}, 0xFFF0))
	for i := 0; i <= maxHeldBytes/len(bigPDU)+1; i++ {
		gate.Ingest(bigPDU)
	}

	waitFor(t, "overload callback", func() bool { return h.overloads() == 1 })
	if !gate.Killed() {
		t.Errorf("gate must report killed after overflow")
	}

	// Further ingests are discarded and never re-trigger the callback.
	gate.Ingest(bigPDU)
	time.Sleep(20 * time.Millisecond)
	if h.overloads() != 1 {
		t.Errorf("OnOverload must fire exactly once, got %d", h.overloads())
	}
}

func TestPIIGate_ForwardErrorStopsGate(t *testing.T) {
	h := &gateHarness{}
	h.setForwardErr(fmt.Errorf("client gone"))
	gate := newTestGate(t, h, nil)

	gate.Ingest(tpktPDU([]byte{0x01, 0x02}))

	// The forward failure must close the gate; later ingests are dropped and
	// Close does not deadlock.
	waitFor(t, "gate closed after forward error", func() bool {
		gate.mu.Lock()
		defer gate.mu.Unlock()
		return gate.closed
	})
	gate.Ingest(tpktPDU([]byte{0x03}))
	gate.Close()
	if h.detections() != 0 || h.overloads() != 0 {
		t.Errorf("no detection/overload expected on forward error")
	}
}

func TestPIIGate_CloseCancelsInFlightAnalysis(t *testing.T) {
	h := &gateHarness{}
	analyzerEntered := make(chan struct{})
	analyzerCancelled := make(chan struct{})
	gate := newTestGate(t, h, func(ctx context.Context, _ []byte, _, _ int, _ []analyzer.YBand, _ string, _ int, _ float64, _ *analyzer.PresidioClient, _ analyzer.AnalysisParams) (*analyzer.SnapshotResult, error) {
		close(analyzerEntered)
		<-ctx.Done() // simulate a hung tesseract: only cancellation frees it
		close(analyzerCancelled)
		return nil, ctx.Err()
	})

	// Get the analyzer stuck on a dirty batch.
	ingestSyntheticBitmap(gate, 0, 16)
	gate.Ingest(tpktPDU([]byte{0x01}))

	// Wait until the loop is provably inside the stuck analysis, then Close
	// must cancel it and return promptly.
	select {
	case <-analyzerEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("analysis never started")
	}
	closed := make(chan struct{})
	go func() {
		gate.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not cancel the in-flight analysis")
	}
	select {
	case <-analyzerCancelled:
	case <-time.After(time.Second):
		t.Fatal("analyzer did not observe cancellation")
	}
	if got := h.forwardedBytes(); len(got) != 0 {
		t.Errorf("cancelled batch must be dropped, got %d forwarded bytes", len(got))
	}
}

func TestPIIGate_CloseIsIdempotentAndUnblocks(t *testing.T) {
	h := &gateHarness{}
	gate := newTestGate(t, h, nil)
	gate.Ingest(tpktPDU([]byte{0x01}))

	done := make(chan struct{})
	go func() {
		gate.Close()
		gate.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close deadlocked")
	}
}

func TestPIIGate_GrowCanvasPreservesContent(t *testing.T) {
	h := &gateHarness{}
	gate := newTestGate(t, h, nil)

	// Paint a red 2x2 patch at origin on a small canvas.
	red := []byte{0x00, 0x00, 0xff, 0x00, 0x00, 0xff, 0x00, 0x00, 0xff, 0x00, 0x00, 0xff} // BGR x4
	gate.mu.Lock()
	gate.compositeLocked(parser.BitmapRect{X: 0, Y: 0, Width: 2, Height: 2, BitsPerPixel: 24}, red)
	if gate.fbW != 2 || gate.fbH != 2 {
		gate.mu.Unlock()
		t.Fatalf("canvas: got %dx%d, want 2x2", gate.fbW, gate.fbH)
	}
	// Extend the canvas with a patch further out.
	gate.compositeLocked(parser.BitmapRect{X: 30, Y: 30, Width: 2, Height: 2, BitsPerPixel: 24}, red)
	fbW, fbH := gate.fbW, gate.fbH
	// Original pixel (0,0) must still be red (RGBA: R=255).
	r := gate.fb[0]
	gate.mu.Unlock()

	if fbW != 32 || fbH != 32 {
		t.Errorf("grown canvas: got %dx%d, want 32x32", fbW, fbH)
	}
	if r != 0xff {
		t.Errorf("pixel (0,0) red channel after grow: got %d, want 255", r)
	}
}

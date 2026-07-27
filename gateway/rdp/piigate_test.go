package rdp

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
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

// testRect describes one solid-color bitmap rectangle for synthesized PDUs.
type testRect struct {
	x, y, w, h int
	bgr        [3]byte
}

var (
	white   = [3]byte{0xff, 0xff, 0xff}
	magenta = [3]byte{0xff, 0x00, 0xff} // BGR: composites to RGBA (255, 0, 255)
)

// bitmapUpdatePayload builds a TS_UPDATE_BITMAP payload (updateType, nrect,
// TS_BITMAP_DATA...) with one uncompressed 24bpp rectangle per testRect.
func bitmapUpdatePayload(t *testing.T, rects ...testRect) []byte {
	t.Helper()
	upd := new(bytes.Buffer)
	_ = binary.Write(upd, binary.LittleEndian, uint16(0x0001))     // updateType = BITMAP
	_ = binary.Write(upd, binary.LittleEndian, uint16(len(rects))) // numberRectangles
	for _, r := range rects {
		if r.w <= 0 || r.h <= 0 || r.w*r.h*3 > 0xffff {
			t.Fatalf("invalid test rect %+v", r)
		}
		_ = binary.Write(upd, binary.LittleEndian, uint16(r.x))       // destLeft
		_ = binary.Write(upd, binary.LittleEndian, uint16(r.y))       // destTop
		_ = binary.Write(upd, binary.LittleEndian, uint16(r.x+r.w-1)) // destRight (inclusive)
		_ = binary.Write(upd, binary.LittleEndian, uint16(r.y+r.h-1)) // destBottom (inclusive)
		_ = binary.Write(upd, binary.LittleEndian, uint16(r.w))       // width
		_ = binary.Write(upd, binary.LittleEndian, uint16(r.h))       // height
		_ = binary.Write(upd, binary.LittleEndian, uint16(24))        // bitsPerPixel
		_ = binary.Write(upd, binary.LittleEndian, uint16(0))         // compressionFlags: uncompressed
		_ = binary.Write(upd, binary.LittleEndian, uint16(r.w*r.h*3)) // bitmapLength
		upd.Write(bytes.Repeat(r.bgr[:], r.w*r.h))                    // raw BGR rows
	}
	return upd.Bytes()
}

// fastPathPDU wraps an update payload chunk into one Fast-Path PDU with the
// given fragmentation bits (0x0 Single, 0x1 Last, 0x2 First, 0x3 Next).
func fastPathPDU(t *testing.T, frag byte, payload []byte) []byte {
	t.Helper()
	pdu := new(bytes.Buffer)
	// updateHeader: updateCode=1 (bitmap) in bits[0..4], fragmentation in
	// bits[4..6], no compression in bits[6..8].
	pdu.WriteByte(0x01 | frag<<4)
	_ = binary.Write(pdu, binary.LittleEndian, uint16(len(payload)))
	pdu.Write(payload)

	total := 1 + 2 + pdu.Len() // fp header byte + 2-byte PER length + update PDU
	if total > 0x3fff {
		t.Fatalf("test PDU too large for PER length: %d", total)
	}
	out := []byte{0x00, 0x80 | byte(total>>8), byte(total & 0xff)}
	return append(out, pdu.Bytes()...)
}

// fastPathBitmapPDU synthesizes a complete (unfragmented) Fast-Path update
// PDU — the exact wire format the WASM parser frames and decodes. Multiple
// rects in one call form a single (atomic) PDU.
func fastPathBitmapPDU(t *testing.T, rects ...testRect) []byte {
	t.Helper()
	return fastPathPDU(t, 0x0, bitmapUpdatePayload(t, rects...))
}

// fragmentedFastPathBitmapPDU splits one bitmap update across n Fast-Path
// fragments (First, Next..., Last). The parser — like a real client — only
// reassembles and yields bitmaps when the Last fragment arrives.
func fragmentedFastPathBitmapPDU(t *testing.T, n int, rects ...testRect) [][]byte {
	t.Helper()
	payload := bitmapUpdatePayload(t, rects...)
	if n < 2 || len(payload) < n {
		t.Fatalf("cannot split %d payload bytes into %d fragments", len(payload), n)
	}
	chunk := (len(payload) + n - 1) / n
	var out [][]byte
	for i := 0; i < n; i++ {
		lo, hi := i*chunk, (i+1)*chunk
		if hi > len(payload) {
			hi = len(payload)
		}
		frag := byte(0x3) // Next
		switch {
		case i == 0:
			frag = 0x2 // First
		case i == n-1:
			frag = 0x1 // Last
		}
		out = append(out, fastPathPDU(t, frag, payload[lo:hi]))
	}
	return out
}

// clientOracle reconstructs what an RDP client would render from the bytes
// the gate forwarded: it frames PDUs exactly like the gate, composites each
// PDU atomically onto its own shadow canvas, and invokes onState after every
// PDU — i.e. for every intermediate screen state a client could display.
type clientOracle struct {
	t       *testing.T
	parser  *parser.Parser
	canvas  shadowCanvas
	tail    []byte
	onState func(fb []byte, w, h int)
}

func newClientOracle(t *testing.T, onState func(fb []byte, w, h int)) *clientOracle {
	t.Helper()
	p, err := parser.NewParser(context.Background())
	if err != nil {
		t.Fatalf("oracle parser: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return &clientOracle{t: t, parser: p, canvas: shadowCanvas{sessionID: "oracle"}, onState: onState}
}

// consume replays forwarded bytes, checking every per-PDU client state.
func (o *clientOracle) consume(data []byte) {
	o.t.Helper()
	o.tail = append(o.tail, data...)
	for len(o.tail) > 0 {
		size, err := o.parser.GetPduSize(o.tail)
		if err != nil {
			o.t.Fatalf("oracle framing: %v", err)
		}
		if size == 0 || int(size) > len(o.tail) {
			break
		}
		res, err := o.parser.Parse(o.tail[:size])
		if err == nil {
			for _, bmp := range res.Bitmaps {
				if d := o.parser.GetBitmapData(bmp); len(d) > 0 && bmp.Width > 0 && bmp.Height > 0 {
					o.canvas.composite(bmp, d)
				}
			}
		}
		o.tail = o.tail[size:]
		o.onState(o.canvas.fb, o.canvas.w, o.canvas.h)
	}
}

// rectIsColor reports whether every pixel of the rect matches the BGR color.
func rectIsColor(fb []byte, fbW, fbH, x, y, w, h int, bgr [3]byte) bool {
	if x+w > fbW || y+h > fbH {
		return false
	}
	for row := y; row < y+h; row++ {
		for col := x; col < x+w; col++ {
			p := (row*fbW + col) * 4
			if fb[p] != bgr[2] || fb[p+1] != bgr[1] || fb[p+2] != bgr[0] {
				return false
			}
		}
	}
	return true
}

// anyMagentaPixel reports whether any rendered pixel is the signature color.
func anyMagentaPixel(fb []byte) bool {
	for p := 0; p+3 < len(fb); p += 4 {
		if fb[p] == 0xff && fb[p+1] == 0x00 && fb[p+2] == 0xff {
			return true
		}
	}
	return false
}

// signatureDetector is a perfect detector for the planted magenta signature
// rect: it fires iff the FULL rect is visible in the analyzed framebuffer.
// It stands in for OCR+Presidio so tests prove pipeline properties (what is
// analyzed and what escapes) independently of detection accuracy.
func signatureDetector(calls *atomic.Int32, x, y, w, h int) analyzeFunc {
	return func(_ context.Context, fb []byte, fbW, fbH int, _ []analyzer.YBand, sid string, _ int, _ float64, _ *analyzer.PresidioClient, _ analyzer.AnalysisParams) (*analyzer.SnapshotResult, error) {
		if calls != nil {
			calls.Add(1)
		}
		if rectIsColor(fb, fbW, fbH, x, y, w, h, magenta) {
			return &analyzer.SnapshotResult{
				Counts:     map[string]int64{"TEST_SIGNATURE": 1},
				Detections: []models.RDPEntityDetection{{SessionID: sid, EntityType: "TEST_SIGNATURE", Score: 1}},
			}, nil
		}
		return &analyzer.SnapshotResult{}, nil
	}
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

func TestPIIGate_KillsOnDetection(t *testing.T) {
	h := &gateHarness{}
	gate := newTestGate(t, h, signatureDetector(nil, 40, 40, 8, 8))

	// A signature bitmap plus a trailing PDU: analysis must detect and kill.
	gate.Ingest(fastPathBitmapPDU(t, testRect{40, 40, 8, 8, magenta}))
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
	var analyzed atomic.Int32
	gate := newTestGate(t, h, func(_ context.Context, fb []byte, fbW, fbH int, bands []analyzer.YBand, _ string, _ int, _ float64, _ *analyzer.PresidioClient, _ analyzer.AnalysisParams) (*analyzer.SnapshotResult, error) {
		analyzed.Add(1)
		if len(fb) < fbW*fbH*4 {
			return nil, fmt.Errorf("short framebuffer in analysis: %d for %dx%d", len(fb), fbW, fbH)
		}
		return &analyzer.SnapshotResult{Bands: len(bands)}, nil
	})

	bmpPDU := fastPathBitmapPDU(t, testRect{0, 0, 64, 32, white})
	tailPDU := tpktPDU([]byte{0x01, 0x02})
	gate.Ingest(bmpPDU)
	gate.Ingest(tailPDU)

	want := append(append([]byte(nil), bmpPDU...), tailPDU...)
	waitFor(t, "clean batch forwarded", func() bool { return bytes.Equal(h.forwardedBytes(), want) })
	if analyzed.Load() == 0 {
		t.Errorf("analysis must run when bands are dirty")
	}
}

func TestPIIGate_AnalysisErrorFailsOpen(t *testing.T) {
	h := &gateHarness{}
	gate := newTestGate(t, h, func(context.Context, []byte, int, int, []analyzer.YBand, string, int, float64, *analyzer.PresidioClient, analyzer.AnalysisParams) (*analyzer.SnapshotResult, error) {
		return nil, fmt.Errorf("presidio is down")
	})

	bmpPDU := fastPathBitmapPDU(t, testRect{0, 0, 32, 16, white})
	tailPDU := tpktPDU([]byte{0x42})
	gate.Ingest(bmpPDU)
	gate.Ingest(tailPDU)

	want := append(append([]byte(nil), bmpPDU...), tailPDU...)
	waitFor(t, "fail-open forward", func() bool { return bytes.Equal(h.forwardedBytes(), want) })
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
	// A stuck analyzer: blocks until the test ends, so queued bytes accumulate.
	// `entered` signals (once) when the analysis loop has actually reached the
	// analyzer and is about to block — i.e. the first batch has been drained
	// off the queue. We MUST flood only after this, otherwise the run loop's
	// takePending() can drain the flood into the same stuck batch (resetting
	// queuedBytes to 0) and the overflow never triggers. That race made this
	// test flaky; gating the flood on `entered` makes it deterministic.
	block := make(chan struct{})
	entered := make(chan struct{})
	var enteredOnce sync.Once
	t.Cleanup(func() { close(block) })
	gate := newTestGate(t, h, func(ctx context.Context, _ []byte, _, _ int, _ []analyzer.YBand, _ string, _ int, _ float64, _ *analyzer.PresidioClient, _ analyzer.AnalysisParams) (*analyzer.SnapshotResult, error) {
		// Edge-triggered close (via Once) is robust even if a future edit
		// causes more than one analysis before the wait below.
		enteredOnce.Do(func() { close(entered) })
		select {
		case <-block:
		case <-ctx.Done():
		}
		return &analyzer.SnapshotResult{}, nil
	})

	// Get the analyzer stuck on a first dirty batch, and WAIT until it is
	// confirmed blocking before flooding. The analyzer is only reached after
	// takePending() has drained the first batch off the queue, so once we
	// observe `entered` the flood can no longer be merged into that batch.
	gate.Ingest(fastPathBitmapPDU(t, testRect{0, 0, 32, 16, white}))
	gate.Ingest(tpktPDU([]byte{0x01}))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for analyzer to start (first batch not drained)")
	}

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
	gate.Ingest(fastPathBitmapPDU(t, testRect{0, 0, 32, 16, white}))
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

func TestShadowCanvas_GrowPreservesContent(t *testing.T) {
	c := shadowCanvas{sessionID: "t"}
	red := bytes.Repeat([]byte{0x00, 0x00, 0xff}, 4) // BGR 2x2

	if !c.composite(parser.BitmapRect{X: 0, Y: 0, Width: 2, Height: 2, BitsPerPixel: 24}, red) {
		t.Fatal("first composite failed")
	}
	if c.w != 2 || c.h != 2 {
		t.Fatalf("canvas: got %dx%d, want 2x2", c.w, c.h)
	}
	if !c.composite(parser.BitmapRect{X: 30, Y: 30, Width: 2, Height: 2, BitsPerPixel: 24}, red) {
		t.Fatal("second composite failed")
	}
	if c.w != 32 || c.h != 32 {
		t.Errorf("grown canvas: got %dx%d, want 32x32", c.w, c.h)
	}
	if c.fb[0] != 0xff {
		t.Errorf("pixel (0,0) red channel after grow: got %d, want 255", c.fb[0])
	}
	if c.composite(parser.BitmapRect{X: maxCanvasDim - 1, Y: 0, Width: 2, Height: 2, BitsPerPixel: 24}, red) {
		t.Errorf("oversized extent must be rejected")
	}
}

// TestShadowCanvas_CompositeReportsChangedOnlyOnPixelChange: composite reports
// true only when pixels actually change (or the canvas grows), so the gate
// skips re-OCRing byte-identical RDP repaints.
func TestShadowCanvas_CompositeReportsChangedOnlyOnPixelChange(t *testing.T) {
	c := shadowCanvas{sessionID: "t"}
	red := bytes.Repeat([]byte{0x00, 0x00, 0xff}, 16)  // BGR 4x4
	blue := bytes.Repeat([]byte{0xff, 0x00, 0x00}, 16) // BGR 4x4

	if !c.composite(parser.BitmapRect{X: 0, Y: 0, Width: 4, Height: 4, BitsPerPixel: 24}, red) {
		t.Error("first paint must report changed")
	}
	if c.composite(parser.BitmapRect{X: 0, Y: 0, Width: 4, Height: 4, BitsPerPixel: 24}, red) {
		t.Error("byte-identical repaint must report unchanged")
	}
	if !c.composite(parser.BitmapRect{X: 0, Y: 0, Width: 4, Height: 4, BitsPerPixel: 24}, blue) {
		t.Error("different color must report changed")
	}
	if c.composite(parser.BitmapRect{X: 0, Y: 0, Width: 4, Height: 4, BitsPerPixel: 24}, blue) {
		t.Error("identical blue repaint must report unchanged")
	}
}

// TestShadowCanvas_FirstPaintOfZeroPixelsCountsAsChanged: the canvas starts
// zeroed; a first paint whose decoded RGBA is black (0,0,0) is byte-identical
// to the zeroed framebuffer but is NEW to the client and MUST be treated as
// changed (so it is analyzed). The grow guard guarantees this — without it,
// black-on-black content could reach the client unanalyzed.
func TestShadowCanvas_FirstPaintOfZeroPixelsCountsAsChanged(t *testing.T) {
	c := shadowCanvas{sessionID: "t"}
	black := bytes.Repeat([]byte{0x00, 0x00, 0x00}, 16) // BGR 4x4 black

	if !c.composite(parser.BitmapRect{X: 0, Y: 0, Width: 4, Height: 4, BitsPerPixel: 24}, black) {
		t.Error("first paint into new canvas area must be changed even if pixels are black")
	}
	if c.composite(parser.BitmapRect{X: 0, Y: 0, Width: 4, Height: 4, BitsPerPixel: 24}, black) {
		t.Error("identical black repaint over existing black must be unchanged")
	}
}

// --- Adversarial leak tests -------------------------------------------------
//
// These prove the zero-leak pipeline property with a perfect detector
// (signatureDetector) and the client oracle: no per-PDU client-renderable
// state may ever show content the detector would have flagged.

// TestPIIGate_FlashAttackNeverLeaks: PII painted and overwritten within the
// SAME ingest burst (faster than one analysis window). The overwrite must
// seal the batch, forcing the PII state to be analyzed — and killed — before
// either PDU is forwarded.
func TestPIIGate_FlashAttackNeverLeaks(t *testing.T) {
	h := &gateHarness{}
	gate := newTestGate(t, h, signatureDetector(nil, 40, 40, 8, 8))

	flash := fastPathBitmapPDU(t, testRect{40, 40, 8, 8, magenta})
	cover := fastPathBitmapPDU(t, testRect{40, 40, 8, 8, white})
	gate.Ingest(append(append([]byte(nil), flash...), cover...))

	waitFor(t, "flash detection", func() bool { return h.detections() == 1 })
	if !gate.Killed() {
		t.Error("gate must be killed by the flashed signature")
	}

	leaked := false
	oracle := newClientOracle(t, func(fb []byte, _, _ int) {
		if anyMagentaPixel(fb) {
			leaked = true
		}
	})
	oracle.consume(h.forwardedBytes())
	if leaked {
		t.Fatal("LEAK: signature pixels reached a client-renderable state")
	}
	if got := h.forwardedBytes(); len(got) != 0 {
		t.Errorf("flash batch must be dropped entirely, got %d forwarded bytes", len(got))
	}
}

// TestPIIGate_IdenticalRepaintOfPIIIsStillCaught: the dirty-skip optimization
// must not let an identical repaint refresh PII past the guard. The signature
// is painted (changed -> analyzed -> killed) so the identical repaint never even
// runs; the first changed paint is always the one analyzed, and nothing leaks.
func TestPIIGate_IdenticalRepaintOfPIIIsStillCaught(t *testing.T) {
	h := &gateHarness{}
	var calls atomic.Int32
	gate := newTestGate(t, h, signatureDetector(&calls, 40, 40, 8, 8))

	sig := fastPathBitmapPDU(t, testRect{40, 40, 8, 8, magenta})
	gate.Ingest(append(append([]byte(nil), sig...), sig...))

	waitFor(t, "signature detected on first changed paint", func() bool { return h.detections() == 1 })
	if !gate.Killed() {
		t.Error("gate must kill on the PII paint")
	}
	if got := calls.Load(); got < 1 {
		t.Errorf("the changed PII paint must be analyzed: got %d analyses", got)
	}

	leaked := false
	oracle := newClientOracle(t, func(fb []byte, _, _ int) {
		if anyMagentaPixel(fb) {
			leaked = true
		}
	})
	oracle.consume(h.forwardedBytes())
	if leaked {
		t.Fatal("LEAK: signature reached a client-renderable state")
	}
}

// TestPIIGate_IdenticalRepaintsForwardButAnalyzeOnce: byte-identical repaints
// of the same region carry no new content — they must still be FORWARDED (the
// client expects every PDU), but the gate skips re-analyzing pixels it already
// analyzed. RDP resends identical tiles constantly and re-OCRing them is waste.
func TestPIIGate_IdenticalRepaintsForwardButAnalyzeOnce(t *testing.T) {
	h := &gateHarness{}
	var calls atomic.Int32
	gate := newTestGate(t, h, signatureDetector(&calls, 40, 40, 8, 8))

	var burst, want []byte
	for i := 0; i < 5; i++ {
		pdu := fastPathBitmapPDU(t, testRect{40, 40, 8, 8, white})
		burst = append(burst, pdu...)
		want = append(want, pdu...)
	}
	gate.Ingest(burst)

	// Correctness preserved: every PDU is still delivered, in order.
	waitFor(t, "all repaints forwarded in order", func() bool { return bytes.Equal(h.forwardedBytes(), want) })
	// Optimization: identical pixels are analyzed once, not five times.
	if got := calls.Load(); got != 1 {
		t.Errorf("identical repaints must collapse to a single analysis: got %d, want 1", got)
	}
	if h.detections() != 0 {
		t.Errorf("clean repaints must not detect")
	}
}

// TestPIIGate_ChangedRepaintsAnalyzeEachState: every repaint that actually
// CHANGES pixels still forces its own batch and is analyzed — the dirty-skip
// optimization must never collapse genuinely distinct screen states (that would
// be a leak). Alternating colors at a non-signature region change pixels every
// generation; each must be analyzed.
func TestPIIGate_ChangedRepaintsAnalyzeEachState(t *testing.T) {
	h := &gateHarness{}
	var calls atomic.Int32
	gate := newTestGate(t, h, signatureDetector(&calls, 40, 40, 8, 8))

	// Paint the same region alternating white/magenta away from the signature
	// rect (signature at 40,40; paint at 0,0) so each generation differs in
	// pixels but never trips the detector.
	colors := [][3]byte{white, magenta, white, magenta, white}
	var burst, want []byte
	for _, c := range colors {
		pdu := fastPathBitmapPDU(t, testRect{0, 0, 8, 8, c})
		burst = append(burst, pdu...)
		want = append(want, pdu...)
	}
	gate.Ingest(burst)

	waitFor(t, "all changed repaints forwarded in order", func() bool { return bytes.Equal(h.forwardedBytes(), want) })
	if got := calls.Load(); got != int32(len(colors)) {
		t.Errorf("each pixel-changing generation must be analyzed: got %d, want %d", got, len(colors))
	}
	if h.detections() != 0 {
		t.Errorf("magenta away from the signature region must not detect")
	}
}

// TestPIIGate_CrossBatchAssemblyKilledOnCompletion: PII assembled from two
// halves delivered in separate batches. Each forwarded state is analyzed;
// the detector fires on the batch that completes the signature, which is
// dropped — the client never sees the assembled PII.
func TestPIIGate_CrossBatchAssemblyKilledOnCompletion(t *testing.T) {
	h := &gateHarness{}
	gate := newTestGate(t, h, signatureDetector(nil, 40, 40, 8, 8))

	left := fastPathBitmapPDU(t, testRect{40, 40, 4, 8, magenta})
	gate.Ingest(left)
	waitFor(t, "clean left half forwarded", func() bool { return bytes.Equal(h.forwardedBytes(), left) })

	right := fastPathBitmapPDU(t, testRect{44, 40, 4, 8, magenta})
	gate.Ingest(right)
	waitFor(t, "detection on completion", func() bool { return h.detections() == 1 })

	if !bytes.Equal(h.forwardedBytes(), left) {
		t.Errorf("completing batch must be dropped: forwarded %d bytes, want only the left half (%d)", len(h.forwardedBytes()), len(left))
	}

	assembled := false
	oracle := newClientOracle(t, func(fb []byte, w, hgt int) {
		if rectIsColor(fb, w, hgt, 40, 40, 8, 8, magenta) {
			assembled = true
		}
	})
	oracle.consume(h.forwardedBytes())
	if assembled {
		t.Fatal("LEAK: full signature visible in a client-renderable state")
	}
}

// TestPIIGate_IntraPDUOverwriteIsAtomic documents the granularity floor: a
// PDU is the smallest forwardable unit, so patches overwriting each other
// WITHIN one PDU are analyzed at the PDU-final state only. The client
// composites the PDU atomically, so the oracle never renders the overwritten
// intermediate either.
func TestPIIGate_IntraPDUOverwriteIsAtomic(t *testing.T) {
	h := &gateHarness{}
	gate := newTestGate(t, h, signatureDetector(nil, 40, 40, 8, 8))

	pdu := fastPathBitmapPDU(t,
		testRect{40, 40, 8, 8, magenta},
		testRect{40, 40, 8, 8, white},
	)
	gate.Ingest(pdu)

	waitFor(t, "atomic pdu forwarded", func() bool { return bytes.Equal(h.forwardedBytes(), pdu) })
	if h.detections() != 0 {
		t.Errorf("PDU-final state is clean: no detection expected")
	}

	leaked := false
	oracle := newClientOracle(t, func(fb []byte, _, _ int) {
		if anyMagentaPixel(fb) {
			leaked = true
		}
	})
	oracle.consume(h.forwardedBytes())
	if leaked {
		t.Fatal("oracle rendered the intra-PDU intermediate state: PDU atomicity broken")
	}
}

// TestPIIGate_NonConflictingPDUsShareOneBatch: updates to disjoint padded
// bands within one ingest burst coalesce into a single analyzed batch — the
// splitting rule must not degrade into per-PDU analysis for normal traffic.
func TestPIIGate_NonConflictingPDUsShareOneBatch(t *testing.T) {
	h := &gateHarness{}
	var calls atomic.Int32
	gate := newTestGate(t, h, signatureDetector(&calls, 40, 40, 8, 8))

	// Default band padding is 24: y=0,h=8 -> [0,32); y=300,h=8 -> [276,332).
	top := fastPathBitmapPDU(t, testRect{0, 0, 32, 8, white})
	bottom := fastPathBitmapPDU(t, testRect{0, 300, 32, 8, white})
	want := append(append([]byte(nil), top...), bottom...)
	gate.Ingest(want)

	waitFor(t, "batch forwarded", func() bool { return bytes.Equal(h.forwardedBytes(), want) })
	if got := calls.Load(); got != 1 {
		t.Errorf("disjoint bands must share one analysis: got %d, want 1", got)
	}
}

// TestPIIGate_FragmentedUpdateHeldUntilReassembly: a bitmap update split
// across Fast-Path fragments only becomes renderable when the Last fragment
// arrives — which is exactly when the parser yields its bitmaps and the gate
// analyzes it. First/Next fragments may flow through earlier (they carry no
// renderable pixels on their own); the Last fragment of a PII-bearing update
// must never be forwarded.
func TestPIIGate_FragmentedUpdateHeldUntilReassembly(t *testing.T) {
	h := &gateHarness{}
	gate := newTestGate(t, h, signatureDetector(nil, 40, 40, 8, 8))

	frags := fragmentedFastPathBitmapPDU(t, 3, testRect{40, 40, 8, 8, magenta})

	// First and Next fragments: no bitmaps yet, forwarded as plain bytes.
	gate.Ingest(frags[0])
	gate.Ingest(frags[1])
	clean := append(append([]byte(nil), frags[0]...), frags[1]...)
	waitFor(t, "non-final fragments forwarded", func() bool { return bytes.Equal(h.forwardedBytes(), clean) })
	if h.detections() != 0 {
		t.Fatalf("no detection expected before reassembly")
	}

	// Last fragment: the parser reassembles, the gate composites + analyzes
	// the full update, detects the signature and kills.
	gate.Ingest(frags[2])
	waitFor(t, "detection on reassembled update", func() bool { return h.detections() == 1 })
	if !bytes.Equal(h.forwardedBytes(), clean) {
		t.Errorf("Last fragment must be dropped: %d forwarded bytes, want %d", len(h.forwardedBytes()), len(clean))
	}

	// Leak oracle: the forwarded fragments alone must not be renderable —
	// without the Last fragment a client cannot composite the signature.
	leaked := false
	oracle := newClientOracle(t, func(fb []byte, _, _ int) {
		if anyMagentaPixel(fb) {
			leaked = true
		}
	})
	oracle.consume(h.forwardedBytes())
	if leaked {
		t.Fatal("LEAK: fragments without Last rendered the signature")
	}
}

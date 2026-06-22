package rdp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/broker"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/rdp/analyzer"
	"github.com/hoophq/hoop/gateway/rdp/ocr"
	"github.com/hoophq/hoop/gateway/rdp/parser"
	"github.com/hoophq/hoop/gateway/rdp/rle"
)

// PIIGateFlagName gates the realtime hold-and-release PII analysis on the
// RDP server->client stream.
const PIIGateFlagName = "experimental.rdp_pii_guard"

// maxCanvasDim bounds the shadow framebuffer the gate is willing to
// composite. RDP allows up to 8192x8192, but a full-size framebuffer would
// cost ~268MB per session; 4096x4096 (64MB) covers 4K desktops. Bitmaps
// beyond the cap are not composited (logged, fail-open for that region).
const maxCanvasDim = 4096

// maxHeldBytes caps the per-session backlog awaiting analysis (PDU bytes
// plus their extracted bitmap payloads). If the analyzer cannot keep up (or
// is being flooded to force it to lag), the gate fails CLOSED: letting the
// backlog through unanalyzed would be the obvious bypass, and letting it
// grow is an OOM vector.
const maxHeldBytes = 32 << 20

// PIIGateConfig configures a PIIGate.
type PIIGateConfig struct {
	SessionID string
	Presidio  *analyzer.PresidioClient
	Params    analyzer.AnalysisParams
	// Forward delivers cleared bytes downstream (to the RDP client). It is
	// only ever called from the gate's analysis goroutine, so a
	// single-writer transport (e.g. a gorilla websocket) is safe.
	//
	// Ownership follows io.Writer semantics: Forward must not retain or
	// mutate data after it returns — the gate reuses the buffer for the
	// next chunk. Synchronous writers (gorilla's WriteMessage, net.Conn)
	// satisfy this; an implementation that queues the slice for async
	// delivery must copy it first.
	Forward func(data []byte) error
	// OnDetection is invoked exactly once, when PII is detected. The held
	// frames that contained the PII are never forwarded. The callback must
	// terminate the session (the gate stops forwarding permanently).
	OnDetection func(res *analyzer.SnapshotResult)
	// OnOverload is invoked exactly once if the held backlog exceeds
	// maxHeldBytes (analysis cannot keep up). The backlog is dropped, the
	// gate stops permanently (fail-closed), and the callback must terminate
	// the session.
	OnOverload func(droppedBytes int)

	// analyzeOverride replaces analyzer.AnalyzeFramebufferBands in tests
	// (which cannot depend on tesseract/presidio being installed).
	analyzeOverride analyzeFunc
}

// PIIGate is a hold-and-release valve on the RDP server->client byte stream.
//
// Bytes ingested from the server are framed into PDUs; bitmap payloads are
// extracted and queued alongside the wire bytes. The analysis goroutine
// composites queued PDUs into a shadow framebuffer and releases them in
// batches, where a batch never repaints rows it has already dirtied: a PDU
// that would overwrite (or touch the padded text lines of) rows dirtied by
// the current batch seals the batch, forcing the intermediate screen state
// to be analyzed BEFORE the overwrite is released. On detection the held
// bytes are dropped and OnDetection fires.
//
// Enforcement semantics — the precise guarantee is:
//
//	Every forwarded pixel was analyzed in its final on-screen position for
//	the batch that delivered it, and content that is painted and then
//	overwritten is analyzed in its intermediate state before the overwrite
//	is forwarded — regardless of how briefly it would have been visible.
//
// The remaining, deliberate exceptions:
//   - PDU atomicity floor: a PDU is the smallest forwardable unit. Patches
//     that overwrite each other WITHIN one PDU are analyzed at the PDU-final
//     state only.
//   - Progressive rendering: the client renders a batch PDU by PDU. Within
//     a batch, partially-applied states mix already-analyzed old content
//     with new content — but only across non-intersecting padded bands
//     (same-band mixing seals the batch), so no unanalyzed text line is
//     ever composed.
//   - Analysis errors and unframeable data fail OPEN (forwarded, loudly
//     logged): for the PoC, availability wins over enforcement there. A
//     fail-closed policy knob and fail-open metrics are productionization
//     work.
//   - Backlog overflow (analyzer slower than the stream) fails CLOSED: the
//     session is killed rather than letting unanalyzed frames through (see
//     maxHeldBytes).
//   - Detection accuracy is bounded by OCR + Presidio: the pipeline
//     guarantees every state is INSPECTED, not that the detector is
//     infallible.
//
// analyzeFunc matches analyzer.AnalyzeFramebufferBands; injectable for
// tests. Implementations must not retain the framebuffer slice after
// returning: it is owned by the analysis loop and reused.
type analyzeFunc func(
	ctx context.Context,
	framebuffer []byte,
	fbWidth, fbHeight int,
	bands []analyzer.YBand,
	sessionID string,
	frameIndex int,
	timestamp float64,
	presidio *analyzer.PresidioClient,
	params analyzer.AnalysisParams,
) (*analyzer.SnapshotResult, error)

// gatePatch is one bitmap rectangle extracted from a PDU at ingest time
// (the WASM parser's scratch data does not survive the next Parse call, so
// payloads are copied out immediately). Decoding to RGBA is deferred to the
// analysis loop.
type gatePatch struct {
	bmp  parser.BitmapRect
	data []byte
}

// gatePDU is one framed PDU awaiting analysis clearance: the exact wire
// bytes to forward plus the bitmap payloads it carried.
type gatePDU struct {
	data    []byte
	patches []gatePatch
}

func (p gatePDU) size() int {
	n := len(p.data)
	for _, patch := range p.patches {
		n += len(patch.data)
	}
	return n
}

type PIIGate struct {
	cfg     PIIGateConfig
	parser  *parser.Parser
	analyze analyzeFunc

	mu          sync.Mutex
	queue       []gatePDU // framed PDUs awaiting analysis clearance
	queuedBytes int       // backlog accounting for queue entries
	tail        []byte    // partial-PDU bytes, not yet framed
	killed      bool
	closed      bool

	notify chan struct{}      // signals the analysis loop that work is pending
	done   chan struct{}      // closed when the analysis loop exits
	cancel context.CancelFunc // cancels the loop and any in-flight analysis

	// Owned exclusively by the analysis goroutine after start (no locking):
	// the shadow framebuffer always reflects exactly the states being
	// released, never states still queued behind a batch seal.
	canvas shadowCanvas
	dirty  *analyzer.DirtyBands
}

// NewPIIGate creates a gate and starts its analysis goroutine. Callers must
// Close() it when the session ends. The gate owns a private RDP parser
// instance: the parser keeps per-stream Fast-Path fragment state and must not
// be shared with the recorder or other sessions.
func NewPIIGate(ctx context.Context, cfg PIIGateConfig) (*PIIGate, error) {
	if cfg.Forward == nil || cfg.OnDetection == nil || cfg.OnOverload == nil {
		return nil, fmt.Errorf("piigate: Forward, OnDetection and OnOverload callbacks are required")
	}
	if cfg.Presidio == nil {
		return nil, fmt.Errorf("piigate: presidio client is required")
	}
	// WithoutCancel is deliberate: the parser stores this context and uses it
	// for every WASM call INCLUDING runtime teardown — binding it to a
	// cancellable session context would make Close() itself fail after the
	// session context is cancelled. The parser's lifetime is managed
	// explicitly and deterministically by Close() (deferred by the session
	// handler), which is the only correct release path.
	p, err := parser.NewParser(context.WithoutCancel(ctx))
	if err != nil {
		return nil, fmt.Errorf("piigate: failed to instantiate RDP parser: %w", err)
	}
	loopCtx, cancel := context.WithCancel(ctx)
	g := &PIIGate{
		cfg:     cfg,
		parser:  p,
		analyze: analyzer.AnalyzeFramebufferBands,
		canvas:  shadowCanvas{sessionID: cfg.SessionID},
		dirty:   analyzer.NewDirtyBands(maxCanvasDim, cfg.Params.BandPadding),
		notify:  make(chan struct{}, 1),
		done:    make(chan struct{}),
		cancel:  cancel,
	}
	if cfg.analyzeOverride != nil {
		g.analyze = cfg.analyzeOverride
	}
	go g.analysisLoop(loopCtx)
	return g, nil
}

// Ingest consumes server->client bytes. It frames complete PDUs, extracts
// their bitmap payloads, and queues everything for the analysis loop. Ingest
// never blocks on analysis.
func (g *PIIGate) Ingest(data []byte) {
	g.mu.Lock()
	if g.closed || g.killed {
		g.mu.Unlock()
		return
	}

	// Fail closed on backlog overflow: analysis cannot keep up and letting
	// the backlog through unanalyzed would be the trivial bypass.
	if g.queuedBytes+len(g.tail)+len(data) > maxHeldBytes {
		dropped := g.queuedBytes + len(g.tail) + len(data)
		g.killed = true
		g.queue = nil
		g.queuedBytes = 0
		g.tail = nil
		g.mu.Unlock()
		log.With("sid", g.cfg.SessionID).Warnf("piigate: analysis backlog exceeded %d bytes, failing closed and terminating session", maxHeldBytes)
		// Synchronous and exactly-once: killed=true (set under the lock)
		// makes every subsequent Ingest return before reaching this point.
		// Ingest runs on the TLS read path, which is exactly the stream the
		// callback is about to tear down — blocking it is harmless.
		g.cfg.OnOverload(dropped)
		return
	}
	defer g.mu.Unlock()

	g.tail = append(g.tail, data...)

	for len(g.tail) > 0 {
		pduSize, err := g.parser.GetPduSize(g.tail)
		if err != nil {
			log.With("sid", g.cfg.SessionID).Warnf("piigate: framing error, failing open for %d buffered bytes: %v", len(g.tail), err)
			g.enqueueLocked(gatePDU{data: append([]byte(nil), g.tail...)})
			g.tail = g.tail[:0]
			break
		}
		if pduSize == 0 {
			// Unframeable: either an incomplete PDU header or non-FastPath/
			// non-TPKT data. Keep buffering; if it grows past any sane PDU
			// size the stream is not something we can frame — fail open so
			// the session keeps working (bitmaps always arrive as FastPath,
			// which we CAN frame; this path carries no decodable pixels).
			if len(g.tail) > 128*1024 {
				log.With("sid", g.cfg.SessionID).Warnf("piigate: %d unframeable bytes, failing open", len(g.tail))
				g.enqueueLocked(gatePDU{data: append([]byte(nil), g.tail...)})
				g.tail = g.tail[:0]
			}
			break
		}
		if int(pduSize) > len(g.tail) {
			break // incomplete PDU, wait for more bytes
		}

		g.enqueueLocked(g.framePDULocked(g.tail[:pduSize]))
		g.tail = g.tail[pduSize:]
	}

	if len(g.queue) > 0 {
		g.signal()
	}
}

func (g *PIIGate) enqueueLocked(pdu gatePDU) {
	g.queue = append(g.queue, pdu)
	g.queuedBytes += pdu.size()
}

// framePDULocked copies one complete PDU off the tail buffer (queue entries
// outlive the tail's backing array) and extracts its bitmap payloads. Parse
// failures fail open: the PDU is still queued and forwarded, just with no
// pixels to analyze.
func (g *PIIGate) framePDULocked(pdu []byte) gatePDU {
	entry := gatePDU{data: append([]byte(nil), pdu...)}
	result, err := g.parser.Parse(pdu)
	if err != nil {
		log.With("sid", g.cfg.SessionID).Debugf("piigate: parse error (len=%d): %v", len(pdu), err)
		return entry
	}
	for _, bmp := range result.Bitmaps {
		data := g.parser.GetBitmapData(bmp)
		if len(data) == 0 || bmp.Width == 0 || bmp.Height == 0 {
			continue
		}
		entry.patches = append(entry.patches, gatePatch{bmp: bmp, data: data})
	}
	return entry
}

func (g *PIIGate) signal() {
	select {
	case g.notify <- struct{}{}:
	default:
	}
}

// analysisLoop is the single consumer of queued PDUs: it composites them
// into batches, analyzes each batch's dirty bands, and either forwards or
// kills. Running continuously (no ticker) means each batch is analyzed as
// soon as the previous one finishes — analysis duration is the natural rate
// limit.
func (g *PIIGate) analysisLoop(ctx context.Context) {
	defer close(g.done)
	for {
		select {
		case <-ctx.Done():
			return
		case <-g.notify:
		}

		for {
			pdus, ok := g.takePending()
			if !ok {
				break
			}
			if !g.processPDUs(ctx, pdus) {
				return
			}
		}

		g.mu.Lock()
		stop := g.closed
		g.mu.Unlock()
		if stop {
			return
		}
	}
}

// takePending atomically drains the queue. Returns ok=false when there is
// nothing to do.
func (g *PIIGate) takePending() ([]gatePDU, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.killed || len(g.queue) == 0 {
		return nil, false
	}
	pdus := g.queue
	g.queue = nil
	g.queuedBytes = 0
	return pdus, true
}

// processPDUs composites and releases the taken PDUs as one or more analyzed
// batches. A batch is sealed before any PDU that would repaint (or touch the
// padded text lines of) rows the current batch already dirtied — that PDU
// waits until the intermediate state has been analyzed. Returns false when
// the loop must exit (kill, forward failure, or cancellation).
func (g *PIIGate) processPDUs(ctx context.Context, pdus []gatePDU) bool {
	i := 0
	for i < len(pdus) {
		j := i
		for j < len(pdus) {
			if j > i && g.pduConflicts(pdus[j]) {
				break
			}
			g.compositePDU(pdus[j])
			j++
		}
		if !g.analyzeAndForward(ctx, pdus[i:j]) {
			return false
		}
		i = j
	}
	return true
}

// pduConflicts reports whether any of the PDU's patches would touch rows
// already dirtied by the current (unanalyzed) batch.
func (g *PIIGate) pduConflicts(pdu gatePDU) bool {
	for _, p := range pdu.patches {
		if g.dirty.Intersects(int(p.bmp.Y), int(p.bmp.Height)) {
			return true
		}
	}
	return false
}

// compositePDU draws all of a PDU's patches onto the shadow framebuffer and
// accumulates their dirty extents. Decode failures skip the patch (fail open
// for that region, logged by the canvas).
func (g *PIIGate) compositePDU(pdu gatePDU) {
	for _, p := range pdu.patches {
		if g.canvas.composite(p.bmp, p.data) {
			g.dirty.AddRect(int(p.bmp.Y), int(p.bmp.Height))
		}
	}
}

// analyzeAndForward analyzes the current shadow framebuffer state (if the
// batch dirtied anything) and forwards the batch on clearance. Returns false
// when the loop must exit.
func (g *PIIGate) analyzeAndForward(ctx context.Context, batch []gatePDU) bool {
	bands := g.takeBands()
	if len(bands) > 0 {
		res, err := g.analyze(
			ctx, g.canvas.fb, g.canvas.w, g.canvas.h, bands,
			g.cfg.SessionID, 0, 0, g.cfg.Presidio, g.cfg.Params,
		)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return false
			}
			// Fail open: forwarding beats freezing the session on an
			// analyzer hiccup (PoC policy; see type doc).
			log.With("sid", g.cfg.SessionID).Warnf("piigate: analysis failed, failing open: %v", err)
		case len(res.Counts) > 0:
			// Terminal-state transition under the lock: if an overload (or
			// Close) already terminated the gate while this analysis was in
			// flight, the session is going down anyway — do not fire a
			// second terminal callback.
			g.mu.Lock()
			alreadyDown := g.killed || g.closed
			g.killed = true
			g.queue = nil
			g.queuedBytes = 0
			g.tail = nil
			g.mu.Unlock()
			if alreadyDown {
				return false
			}
			log.With("sid", g.cfg.SessionID).Warnf("piigate: PII detected (%v), dropping held batch and terminating session", res.Counts)
			g.cfg.OnDetection(res)
			return false
		}
	}
	return g.forwardBatch(batch)
}

// takeBands drains the dirty-band accumulator, clamped to the framebuffer.
func (g *PIIGate) takeBands() []analyzer.YBand {
	var bands []analyzer.YBand
	for _, b := range g.dirty.TakeAndReset() {
		if b.Y0 >= g.canvas.h {
			continue
		}
		if b.Y1 > g.canvas.h {
			b.Y1 = g.canvas.h
		}
		bands = append(bands, b)
	}
	return bands
}

// forwardChunkBytes caps how many PDU bytes are coalesced into one Forward
// call: enough to amortize per-message overhead, small enough that a
// maxHeldBytes-sized batch never doubles peak memory with a giant copy.
const forwardChunkBytes = 256 << 10

// forwardBatch delivers cleared PDUs downstream, coalescing them into
// bounded chunks (PDU boundaries are preserved within the byte stream; the
// client reassembles from arbitrary message framing). The chunk buffer is
// reused across flushes — sound because Forward's contract forbids
// retaining the slice (see PIIGateConfig.Forward). A transport failure
// means the client is gone; mark the gate closed so the loop exits.
func (g *PIIGate) forwardBatch(batch []gatePDU) bool {
	chunk := make([]byte, 0, forwardChunkBytes)
	flush := func() bool {
		if len(chunk) == 0 {
			return true
		}
		if err := g.cfg.Forward(chunk); err != nil {
			log.With("sid", g.cfg.SessionID).Infof("piigate: forward failed, closing gate: %v", err)
			g.mu.Lock()
			g.closed = true
			g.mu.Unlock()
			return false
		}
		chunk = chunk[:0]
		return true
	}
	for _, p := range batch {
		if len(chunk) > 0 && len(chunk)+len(p.data) > forwardChunkBytes {
			if !flush() {
				return false
			}
		}
		chunk = append(chunk, p.data...)
	}
	return flush()
}

// shadowCanvas is a growable RGBA framebuffer that mirrors the client's
// screen. It is shared between the gate's analysis loop and the test-side
// leak oracle so both composite pixels identically.
type shadowCanvas struct {
	sessionID string
	fb        []byte
	w, h      int
}

// composite decodes one bitmap patch and draws it, growing the canvas if the
// patch extends beyond it. Returns false when the patch cannot be composited
// (oversized extent or decode failure) — fail-open for that region.
func (c *shadowCanvas) composite(bmp parser.BitmapRect, data []byte) bool {
	right, bottom := int(bmp.X)+int(bmp.Width), int(bmp.Y)+int(bmp.Height)
	if right > maxCanvasDim || bottom > maxCanvasDim {
		log.With("sid", c.sessionID).Warnf("piigate: bitmap extent %dx%d exceeds max canvas, skipping", right, bottom)
		return false
	}
	var rgba []byte
	var err error
	if bmp.Compressed {
		rgba, err = rle.DecompressToRGBA(data, int(bmp.Width), int(bmp.Height), int(bmp.BitsPerPixel))
	} else {
		rgba, err = rle.ToRGBA(data, int(bmp.Width), int(bmp.Height), int(bmp.BitsPerPixel))
	}
	if err != nil {
		log.With("sid", c.sessionID).Debugf("piigate: bitmap decode error: %v", err)
		return false
	}
	if right > c.w || bottom > c.h {
		c.grow(right, bottom)
	}
	analyzer.CompositeBitmap(c.fb, c.w, c.h, rgba, int(bmp.Width), int(bmp.Height), int(bmp.X), int(bmp.Y))
	return true
}

// grow reallocates the framebuffer to cover at least (width x height),
// preserving existing content.
func (c *shadowCanvas) grow(width, height int) {
	if width < c.w {
		width = c.w
	}
	if height < c.h {
		height = c.h
	}
	newFB := make([]byte, width*height*4)
	for y := 0; y < c.h; y++ {
		copy(newFB[y*width*4:y*width*4+c.w*4], c.fb[y*c.w*4:(y+1)*c.w*4])
	}
	c.fb = newFB
	c.w, c.h = width, height
}

// newSessionPIIGate builds a PIIGate wired to a live IronRDP session: it
// forwards cleared bytes to the websocket and, on detection, persists the
// violation and terminates the broker session. Returns nil when the guard is
// not enabled for the org or its prerequisites (Presidio, an OCR engine) are
// missing — callers fall back to direct forwarding.
func newSessionPIIGate(
	orgID, sessionID string,
	ws *websocket.Conn,
	session *broker.Session,
	setSessionErr func(error),
) *PIIGate {
	if !featureflag.IsEnabled(orgID, PIIGateFlagName) {
		return nil
	}
	// Unlike the async job pipeline (analyzer.IsEnabled), the gate does not
	// depend on the worker pool: it only needs Presidio and an OCR engine.
	analyzerURL := appconfig.Get().MSPresidioAnalyzerURL()
	if analyzerURL == "" || !ocr.IsAvailable() {
		log.With("sid", sessionID).Warnf("piigate: %s is on but presidio/OCR engine are unavailable, session runs UNGUARDED", PIIGateFlagName)
		return nil
	}

	gate, err := NewPIIGate(context.Background(), PIIGateConfig{
		SessionID: sessionID,
		Presidio:  analyzer.NewPresidioClient(analyzerURL),
		Params:    analyzer.DefaultAnalysisParams(),
		Forward: func(data []byte) error {
			return ws.WriteMessage(websocket.BinaryMessage, data)
		},
		OnDetection: func(res *analyzer.SnapshotResult) {
			setSessionErr(fmt.Errorf("session terminated: PII detected on screen (%s)", formatEntityCounts(res.Counts)))
			// Tear the session down first; persistence is best-effort and
			// must not delay enforcement on a slow database. Closing the
			// websocket too is required: session.Close() only unblocks the
			// TLS side, and the handler would otherwise stay parked on the
			// websocket reader until the (idle) browser acts.
			session.Close()
			_ = ws.Close()
			go persistPIIViolation(orgID, sessionID, res)
		},
		OnOverload: func(droppedBytes int) {
			setSessionErr(fmt.Errorf("session terminated: PII analysis backlog exceeded (%d bytes dropped)", droppedBytes))
			session.Close()
			_ = ws.Close()
		},
	})
	if err != nil {
		log.With("sid", sessionID).Warnf("piigate: failed to start, session runs UNGUARDED: %v", err)
		return nil
	}
	log.With("sid", sessionID).Infof("piigate: realtime PII guard active (hold-and-release)")
	return gate
}

// persistPIIViolation records the detection as guardrails info on the session
// and stores the per-entity bounding boxes for the UI. The two writes are
// deliberately best-effort and non-transactional: enforcement (session kill)
// has already happened, and partial evidence is better than none.
func persistPIIViolation(orgID, sessionID string, res *analyzer.SnapshotResult) {
	info := []models.SessionGuardRailsInfo{{
		RuleName:     "rdp_pii_guard",
		Rule:         models.SessionGuardRailMatchedRule{Type: "pii_detection"},
		Direction:    "server_to_client",
		MatchedWords: entityTypes(res.Counts),
	}}
	if data, err := json.Marshal(info); err == nil {
		if err := models.UpdateSessionGuardRailsInfo(orgID, sessionID, data); err != nil {
			log.With("sid", sessionID).Warnf("piigate: failed to persist guardrails info: %v", err)
		}
	}
	if len(res.Detections) > 0 {
		if err := models.BulkInsertRDPEntityDetections(res.Detections); err != nil {
			log.With("sid", sessionID).Warnf("piigate: failed to persist entity detections: %v", err)
		}
	}
}

func entityTypes(counts map[string]int64) []string {
	types := make([]string, 0, len(counts))
	for entity := range counts {
		types = append(types, entity)
	}
	sort.Strings(types)
	return types
}

func formatEntityCounts(counts map[string]int64) string {
	parts := entityTypes(counts)
	for i, entity := range parts {
		parts[i] = fmt.Sprintf("%s x%d", entity, counts[entity])
	}
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}

// Killed reports whether the gate terminated the session on a detection.
func (g *PIIGate) Killed() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.killed
}

// Close shuts the gate down and releases the parser. Held bytes that were
// never analyzed are dropped — the session is ending anyway, and forwarding
// unanalyzed frames on shutdown would bypass the guarantee. Any in-flight
// analysis is cancelled immediately (its batch is dropped, its detections —
// if any — are lost): shutdown liveness wins over best-effort final
// evidence, since a hung OCR subprocess must never wedge session teardown.
func (g *PIIGate) Close() {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return
	}
	g.closed = true
	g.queue = nil
	g.queuedBytes = 0
	g.tail = nil
	g.mu.Unlock()

	// Cancel any in-flight analysis (kills a hung OCR subprocess/request via
	// CommandContext) and unblock the loop if it is waiting, then wait for
	// it to exit before releasing the parser.
	g.cancel()
	g.signal()
	<-g.done
	_ = g.parser.Close()
}

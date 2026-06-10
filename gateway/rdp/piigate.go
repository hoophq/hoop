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
// composite. RDP allows up to 8192x8192, but a full-size framebuffer plus its
// analysis snapshot would cost ~536MB per session; 4096x4096 (64MB + 64MB)
// covers 4K desktops. Bitmaps beyond the cap are not composited (logged,
// fail-open for that region).
const maxCanvasDim = 4096

// maxHeldBytes caps the per-session backlog of bytes awaiting analysis. If
// the analyzer cannot keep up (or is being flooded to force it to lag), the
// gate fails CLOSED: letting the backlog through unanalyzed would be the
// obvious bypass, and letting it grow is an OOM vector.
const maxHeldBytes = 32 << 20

// PIIGateConfig configures a PIIGate.
type PIIGateConfig struct {
	SessionID string
	Presidio  *analyzer.PresidioClient
	Params    analyzer.AnalysisParams
	// Forward delivers cleared bytes downstream (to the RDP client). It is
	// only ever called from the gate's analysis goroutine, so a
	// single-writer transport (e.g. a gorilla websocket) is safe.
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
// Bytes ingested from the server are framed into PDUs and held. Bitmap
// updates are composited into a shadow framebuffer and their extents
// accumulated as dirty bands. An analysis goroutine repeatedly snapshots the
// pending state, runs OCR + Presidio on the dirty bands, and only then
// releases the held bytes to the client — so PII never reaches the client's
// screen. On detection the held bytes are dropped and OnDetection fires.
//
// Enforcement semantics — the precise guarantee is:
//
//	PII that PERSISTS on screen for at least one batch window (~100ms,
//	the analysis duration) is never forwarded to the client.
//
// It is explicitly NOT "PII never reaches the client", because:
//   - Analysis inspects the final composited state of each held batch. A
//     transient state that is painted and fully overwritten WITHIN one batch
//     window is flushed without its intermediate state being analyzed; the
//     client may render it for a sub-frame interval. Detecting those would
//     require per-PDU analysis, far beyond the latency budget.
//   - Analysis errors and unframeable data fail OPEN (forwarded, loudly
//     logged): for the PoC, availability wins over enforcement there. A
//     fail-closed policy knob and fail-open metrics are productionization
//     work.
//   - Backlog overflow (analyzer slower than the stream) fails CLOSED: the
//     session is killed rather than letting unanalyzed frames through (see
//     maxHeldBytes).
//
// analyzeFunc matches analyzer.AnalyzeFramebufferBands; injectable for tests.
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

type PIIGate struct {
	cfg     PIIGateConfig
	parser  *parser.Parser
	analyze analyzeFunc

	mu     sync.Mutex
	held   []byte // complete-PDU bytes awaiting analysis clearance
	tail   []byte // partial-PDU bytes, not yet framed
	fb     []byte // shadow RGBA framebuffer (grows with observed extents)
	fbW    int
	fbH    int
	dirty  *analyzer.DirtyBands
	killed bool
	closed bool

	notify chan struct{}      // signals the analysis loop that work is pending
	done   chan struct{}      // closed when the analysis loop exits
	cancel context.CancelFunc // cancels the loop and any in-flight analysis

	fbScratch []byte // reused snapshot copy buffer
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
	p, err := parser.NewParser(context.WithoutCancel(ctx))
	if err != nil {
		return nil, fmt.Errorf("piigate: failed to instantiate RDP parser: %w", err)
	}
	loopCtx, cancel := context.WithCancel(ctx)
	g := &PIIGate{
		cfg:     cfg,
		parser:  p,
		analyze: analyzer.AnalyzeFramebufferBands,
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

// Ingest consumes server->client bytes. It frames complete PDUs, composites
// bitmap updates into the shadow framebuffer, and holds everything until the
// analysis loop clears it. Ingest never blocks on analysis.
func (g *PIIGate) Ingest(data []byte) {
	g.mu.Lock()
	if g.closed || g.killed {
		g.mu.Unlock()
		return
	}

	// Fail closed on backlog overflow: analysis cannot keep up and letting
	// the backlog through unanalyzed would be the trivial bypass.
	if len(g.held)+len(g.tail)+len(data) > maxHeldBytes {
		dropped := len(g.held) + len(g.tail) + len(data)
		g.killed = true
		g.held = nil
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
			g.held = append(g.held, g.tail...)
			g.tail = nil
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
				g.held = append(g.held, g.tail...)
				g.tail = nil
			}
			break
		}
		if int(pduSize) > len(g.tail) {
			break // incomplete PDU, wait for more bytes
		}

		pdu := g.tail[:pduSize]
		g.ingestPDULocked(pdu)
		g.held = append(g.held, pdu...)
		g.tail = g.tail[pduSize:]
	}

	if len(g.held) > 0 {
		g.signal()
	}
}

// ingestPDULocked parses one complete PDU and composites any bitmap updates.
func (g *PIIGate) ingestPDULocked(pdu []byte) {
	result, err := g.parser.Parse(pdu)
	if err != nil {
		log.With("sid", g.cfg.SessionID).Debugf("piigate: parse error (len=%d): %v", len(pdu), err)
		return
	}
	for _, bmp := range result.Bitmaps {
		data := g.parser.GetBitmapData(bmp)
		if len(data) == 0 || bmp.Width == 0 || bmp.Height == 0 {
			continue
		}
		g.compositeLocked(bmp, data)
	}
}

// compositeLocked decodes one bitmap patch and draws it onto the shadow
// framebuffer, growing the framebuffer if the patch extends beyond it.
func (g *PIIGate) compositeLocked(bmp parser.BitmapRect, data []byte) {
	right, bottom := int(bmp.X)+int(bmp.Width), int(bmp.Y)+int(bmp.Height)
	if right > maxCanvasDim || bottom > maxCanvasDim {
		log.With("sid", g.cfg.SessionID).Warnf("piigate: bitmap extent %dx%d exceeds max canvas, skipping", right, bottom)
		return
	}
	if right > g.fbW || bottom > g.fbH {
		g.growCanvasLocked(right, bottom)
	}

	var rgba []byte
	var err error
	if bmp.Compressed {
		rgba, err = rle.DecompressToRGBA(data, int(bmp.Width), int(bmp.Height), int(bmp.BitsPerPixel))
	} else {
		rgba, err = rle.ToRGBA(data, int(bmp.Width), int(bmp.Height), int(bmp.BitsPerPixel))
	}
	if err != nil {
		log.With("sid", g.cfg.SessionID).Debugf("piigate: bitmap decode error: %v", err)
		return
	}

	analyzer.CompositeBitmap(g.fb, g.fbW, g.fbH, rgba, int(bmp.Width), int(bmp.Height), int(bmp.X), int(bmp.Y))
	g.dirty.AddRect(int(bmp.Y), int(bmp.Height))
}

// growCanvasLocked reallocates the shadow framebuffer to cover at least
// (width x height), preserving existing content.
func (g *PIIGate) growCanvasLocked(width, height int) {
	if width < g.fbW {
		width = g.fbW
	}
	if height < g.fbH {
		height = g.fbH
	}
	newFB := make([]byte, width*height*4)
	for y := 0; y < g.fbH; y++ {
		copy(newFB[y*width*4:y*width*4+g.fbW*4], g.fb[y*g.fbW*4:(y+1)*g.fbW*4])
	}
	g.fb = newFB
	g.fbW, g.fbH = width, height
}

func (g *PIIGate) signal() {
	select {
	case g.notify <- struct{}{}:
	default:
	}
}

// analysisLoop is the single consumer of held bytes: it snapshots pending
// state, analyzes the dirty bands, and either forwards or kills. Running
// continuously (no ticker) means each batch is analyzed as soon as the
// previous one finishes — analysis duration is the natural rate limit.
func (g *PIIGate) analysisLoop(ctx context.Context) {
	defer close(g.done)
	for {
		select {
		case <-ctx.Done():
			return
		case <-g.notify:
		}

		g.mu.Lock()
		stop := g.closed
		g.mu.Unlock()
		if stop {
			return
		}

		for {
			batch, fbSnapshot, bands, fbW, fbH, ok := g.takeBatch()
			if !ok {
				break
			}

			if len(bands) == 0 {
				// No pixels changed: nothing to analyze, clear immediately.
				g.forwardBatch(batch)
				continue
			}

			res, err := g.analyze(
				ctx, fbSnapshot, fbW, fbH, bands,
				g.cfg.SessionID, 0, 0, g.cfg.Presidio, g.cfg.Params,
			)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				// Fail open: forwarding beats freezing the session on an
				// analyzer hiccup (PoC policy; see type doc).
				log.With("sid", g.cfg.SessionID).Warnf("piigate: analysis failed, failing open: %v", err)
				g.forwardBatch(batch)
				continue
			}

			if len(res.Counts) > 0 {
				g.mu.Lock()
				g.killed = true
				g.held = nil
				g.tail = nil
				g.mu.Unlock()
				log.With("sid", g.cfg.SessionID).Warnf("piigate: PII detected (%v), dropping %d held bytes and terminating session", res.Counts, len(batch))
				g.cfg.OnDetection(res)
				return
			}

			g.forwardBatch(batch)
		}
	}
}

// takeBatch atomically captures the pending state: held bytes, a snapshot
// copy of the framebuffer, and the dirty bands clamped to the current
// framebuffer. Returns ok=false when there is nothing to do.
//
// The returned fbSnapshot aliases g.fbScratch, which is reused across calls:
// it is only valid until the next takeBatch call. That is safe because the
// analysis loop is the single caller and fully consumes the snapshot before
// iterating.
func (g *PIIGate) takeBatch() (batch, fbSnapshot []byte, bands []analyzer.YBand, fbW, fbH int, ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.killed || len(g.held) == 0 {
		return nil, nil, nil, 0, 0, false
	}

	batch = g.held
	g.held = nil

	for _, b := range g.dirty.TakeAndReset() {
		if b.Y0 >= g.fbH {
			continue
		}
		if b.Y1 > g.fbH {
			b.Y1 = g.fbH
		}
		bands = append(bands, b)
	}

	if len(bands) > 0 {
		if cap(g.fbScratch) < len(g.fb) {
			g.fbScratch = make([]byte, len(g.fb))
		}
		g.fbScratch = g.fbScratch[:len(g.fb)]
		copy(g.fbScratch, g.fb)
		fbSnapshot = g.fbScratch
	}

	return batch, fbSnapshot, bands, g.fbW, g.fbH, true
}

// forwardBatch delivers cleared bytes downstream. A transport failure means
// the client is gone; mark the gate closed so the loop drains and exits.
func (g *PIIGate) forwardBatch(batch []byte) {
	if len(batch) == 0 {
		return
	}
	if err := g.cfg.Forward(batch); err != nil {
		log.With("sid", g.cfg.SessionID).Infof("piigate: forward failed, closing gate: %v", err)
		g.mu.Lock()
		g.closed = true
		g.mu.Unlock()
	}
}

// newSessionPIIGate builds a PIIGate wired to a live IronRDP session: it
// forwards cleared bytes to the websocket and, on detection, persists the
// violation and terminates the broker session. Returns nil when the guard is
// not enabled for the org or its prerequisites (Presidio, tesseract) are
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
	// depend on the worker pool: it only needs Presidio and tesseract.
	analyzerURL := appconfig.Get().MSPresidioAnalyzerURL()
	if analyzerURL == "" || !ocr.IsAvailable() {
		log.With("sid", sessionID).Warnf("piigate: %s is on but presidio/tesseract are unavailable, session runs UNGUARDED", PIIGateFlagName)
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
			// must not delay enforcement on a slow database.
			session.Close()
			go persistPIIViolation(orgID, sessionID, res)
		},
		OnOverload: func(droppedBytes int) {
			setSessionErr(fmt.Errorf("session terminated: PII analysis backlog exceeded (%d bytes dropped)", droppedBytes))
			session.Close()
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
	g.held = nil
	g.tail = nil
	g.mu.Unlock()

	// Cancel any in-flight analysis (kills a hung tesseract subprocess via
	// CommandContext) and unblock the loop if it is waiting, then wait for
	// it to exit before releasing the parser.
	g.cancel()
	g.signal()
	<-g.done
	_ = g.parser.Close()
}

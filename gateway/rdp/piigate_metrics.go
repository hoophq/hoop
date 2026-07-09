package rdp

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/log"
)

// piiLatencyWindow is how long the gateway-side PII gate accumulates per-stage
// timings before emitting one summary log line and resetting. The gate runs
// OCR + Presidio per held batch (many batches per second on a busy session),
// so per-batch logging would flood the gateway log; a windowed summary is
// enough to find the dominant offender (OCR vs Presidio vs compositing)
// without the spam.
//
// Note: the gateway-side gate is KILL-ONLY (it has no redaction path —
// redaction is an agent-side policy, see agentrs/src/piigate). There is
// therefore deliberately no redact stage here; redact latency is attributed
// only in the agentrs aggregator. The agentrs summary line carries an extra
// redact[...] field for that reason.
const piiLatencyWindow = 5 * time.Second

// piiMaxSamplesPerStage hard-caps samples retained per stage within one
// window. A busy session can analyze many batches per second; without a cap a
// 5s window on a pathological stream could retain a huge sample vector (and a
// costlier percentile sort). Once the cap is hit, further samples for that
// stage are dropped — the percentiles become an estimate over the first
// piiMaxSamplesPerStage samples of the window, which is plenty to spot the
// dominant stage. This is diagnostic, not billing.
const piiMaxSamplesPerStage = 4096

// piiStageSamples collects one stage's durations (in microseconds) within the
// current window.
type piiStageSamples struct {
	micros []int64
}

func (s *piiStageSamples) push(d time.Duration) {
	// Non-positive samples are excluded (a stage that did not run, or one that
	// rounded below 1µs and carries no signal). Bounded by the per-stage cap.
	if d <= 0 || len(s.micros) >= piiMaxSamplesPerStage {
		return
	}
	s.micros = append(s.micros, d.Microseconds())
}

// summarize returns "n=… avg=…ms p50=…ms p95=…ms max=…ms" for the stage, or
// "n=0" when it never ran this window. It sorts the samples in place.
func (s *piiStageSamples) summarize() string {
	if len(s.micros) == 0 {
		return "n=0"
	}
	sort.Slice(s.micros, func(i, j int) bool { return s.micros[i] < s.micros[j] })
	n := len(s.micros)
	var sum int64
	for _, v := range s.micros {
		sum += v
	}
	avgMs := float64(sum) / float64(n) / 1000.0
	maxMs := float64(s.micros[n-1]) / 1000.0
	return fmt.Sprintf("n=%d avg=%.1fms p50=%.1fms p95=%.1fms max=%.1fms",
		n, avgMs, percentileMs(s.micros, 0.50), percentileMs(s.micros, 0.95), maxMs)
}

// percentileMs is the nearest-rank percentile (q in [0,1]) over an
// already-sorted ascending micros slice, returned as milliseconds.
func percentileMs(sorted []int64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(q*float64(len(sorted)-1) + 0.5)
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return float64(sorted[rank]) / 1000.0
}

// piiLatencyAggregator accumulates per-stage timings for one gateway-side PII
// gate session and logs a windowed summary. Safe for concurrent use; in
// practice every record call comes from the single analysis goroutine, but the
// mutex keeps the final flush race-free.
type piiLatencyAggregator struct {
	sessionID string

	mu             sync.Mutex
	startedAt      time.Time // zero until the first record after a flush
	batches        int
	detections     int
	forwardedBytes int64
	composite      piiStageSamples
	ocr            piiStageSamples
	presidio       piiStageSamples
	total          piiStageSamples
}

func newPIILatencyAggregator(sessionID string) *piiLatencyAggregator {
	return &piiLatencyAggregator{sessionID: sessionID}
}

// recordBatch records one analyzed batch: its per-stage timings, the bytes
// forwarded (0 when dropped/killed), and whether it was a detection. This is
// the per-batch anchor — call it exactly once per analyzed batch. It flushes
// the window when [piiLatencyWindow] has elapsed.
func (a *piiLatencyAggregator) recordBatch(
	composite, ocr, presidio, total time.Duration,
	forwardedBytes int64,
	detection bool,
) {
	a.mu.Lock()
	elapsed := false
	if a.startedAt.IsZero() {
		a.startedAt = time.Now()
	} else if time.Since(a.startedAt) >= piiLatencyWindow {
		elapsed = true
	}

	a.batches++
	if detection {
		a.detections++
	}
	a.forwardedBytes += forwardedBytes
	a.composite.push(composite)
	a.ocr.push(ocr)
	a.presidio.push(presidio)
	a.total.push(total)

	// Detach the window under the lock and log it outside, so the (sort +
	// format + log) work never blocks concurrent recorders.
	var detached *piiLatencyAggregator
	if elapsed {
		detached = a.detachLocked()
	}
	a.mu.Unlock()
	if detached != nil {
		detached.logWindow()
	}
}

// flushFinal emits the last (partial) window at gate close so the final few
// seconds of measurements are not lost. A no-op when nothing was recorded
// since the last flush.
func (a *piiLatencyAggregator) flushFinal() {
	a.mu.Lock()
	var detached *piiLatencyAggregator
	if a.batches > 0 {
		detached = a.detachLocked()
	}
	a.mu.Unlock()
	if detached != nil {
		detached.logWindow()
	}
}

// detachLocked snapshots the current window into a standalone aggregator and
// resets the live one. The caller must hold a.mu.
func (a *piiLatencyAggregator) detachLocked() *piiLatencyAggregator {
	d := &piiLatencyAggregator{
		sessionID:      a.sessionID,
		startedAt:      a.startedAt,
		batches:        a.batches,
		detections:     a.detections,
		forwardedBytes: a.forwardedBytes,
		composite:      a.composite,
		ocr:            a.ocr,
		presidio:       a.presidio,
		total:          a.total,
	}
	a.reset()
	return d
}

// logWindow summarizes and logs a detached window. Must be called WITHOUT the
// lock held (a detached aggregator is not shared, so it needs no locking).
func (a *piiLatencyAggregator) logWindow() {
	var windowS float64
	if !a.startedAt.IsZero() {
		windowS = time.Since(a.startedAt).Seconds()
	}
	log.With("sid", a.sessionID).Infof(
		"piigate latency (gateway): window_s=%.1f batches=%d detections=%d forwarded_kib=%d "+
			"composite[%s] ocr[%s] presidio[%s] total[%s]",
		windowS, a.batches, a.detections, a.forwardedBytes/1024,
		a.composite.summarize(), a.ocr.summarize(),
		a.presidio.summarize(), a.total.summarize(),
	)
}

func (a *piiLatencyAggregator) reset() {
	a.startedAt = time.Time{}
	a.batches = 0
	a.detections = 0
	a.forwardedBytes = 0
	a.composite = piiStageSamples{}
	a.ocr = piiStageSamples{}
	a.presidio = piiStageSamples{}
	a.total = piiStageSamples{}
}

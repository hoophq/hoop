package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/hoophq/hoop/gateway/rdp/analyzer"
)

// runGatePace simulates the PIIGate hold-and-release semantics on a real
// recording: bitmap events are paced into a queue by their original
// timestamps; a serial analysis loop drains the queue and splits it into
// batches at dirty-band conflicts (the same rule as gateway/rdp.PIIGate —
// a batch never repaints rows it has already dirtied), running the real
// OCR+Presidio analysis on every batch state. It measures what the guard
// costs interactively: how long each frame is HELD before release, and how
// many extra analyses the conflict rule forces.
//
// One fixture event approximates one PDU carrying a single bitmap patch.
// That is the worst case for splitting — live PDUs often group several
// patches, which can only reduce the number of seals.
func runGatePace(cfg *benchConfig) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w, h := cfg.fixture.CanvasWidth, cfg.fixture.CanvasHeight
	fb := make([]byte, w*h*4)
	dirty := analyzer.NewDirtyBands(h, cfg.bandPad)
	report := &benchReport{}

	type queuedEvent struct {
		ev      frameEvent
		arrived time.Time
	}
	var mu sync.Mutex
	var queue []queuedEvent
	notify := make(chan struct{}, 1)

	baseTS := cfg.frames[0].Timestamp
	wallStart := time.Now()
	replayDone := make(chan struct{})

	// Replayer: paces bitmap events by their original timestamps, exactly
	// like the TLS read path delivers PDUs to PIIGate.Ingest.
	go func() {
		defer close(replayDone)
		for _, ev := range cfg.frames {
			target := wallStart.Add(time.Duration((ev.Timestamp - baseTS) / cfg.speed * float64(time.Second)))
			if d := time.Until(target); d > 0 {
				select {
				case <-time.After(d):
				case <-ctx.Done():
					return
				}
			}
			mu.Lock()
			queue = append(queue, queuedEvent{ev: ev, arrived: time.Now()})
			mu.Unlock()
			select {
			case notify <- struct{}{}:
			default:
			}
		}
	}()

	takePending := func() []queuedEvent {
		mu.Lock()
		defer mu.Unlock()
		out := queue
		queue = nil
		return out
	}

	var holdLag durationStats
	var batchRects []rect
	batches, conflictSeals, released := 0, 0, 0

	// Content-addressed verdict cache: hash of a padded band's pixel rows ->
	// PII verdict from a previous analysis. A conflict whose intermediate
	// state hashes to a known CLEAN verdict needs no seal: the exact screen
	// content was already analyzed. Content-addressing keeps the zero-leak
	// property: only states that were actually analyzed are ever skipped —
	// unlike a time-based assumption, new pixels always miss.
	//
	// Known-PII hits are counted separately as "masked skips": releasing
	// them WOULD require pixel masking, which neither this simulator nor
	// the production gate implements yet — the counter only sizes how much
	// a future redaction mode would benefit. In kill mode a known-PII hit
	// is moot anyway (the first detection already ended the session).
	verdictCache := make(map[[32]byte]bool)
	cacheCleanSkips, cacheMaskedSkips := 0, 0

	paddedBand := func(y, height int) (int, int) {
		y0, y1 := y-cfg.bandPad, y+height+cfg.bandPad
		if y0 < 0 {
			y0 = 0
		}
		if y1 > h {
			y1 = h
		}
		return y0, y1
	}
	bandHash := func(y0, y1 int) [32]byte {
		return sha256.Sum256(fb[y0*w*4 : y1*w*4])
	}

	// Capture mode: instead of sealing on a conflict, memcpy the padded band
	// rows that are about to be hidden. At batch end the final state's dirty
	// bands AND every captured intermediate state are stacked into one tall
	// virtual framebuffer and analyzed in a single parallel OCR pass — one
	// release decision per batch, no serialization.
	type capturedBand struct {
		y0, y1 int
		rows   []byte
		hash   [32]byte
	}
	var captures []capturedBand
	captured, captureBytes := 0, 0

	conflicts := func(bmp analyzer.BitmapEvent) bool {
		switch cfg.conflictMode {
		case "rect":
			q := rect{x: int(bmp.X), y: int(bmp.Y), w: int(bmp.Width), h: int(bmp.Height)}
			for _, r := range batchRects {
				if q.x < r.x+r.w && r.x < q.x+q.w && q.y < r.y+r.h && r.y < q.y+q.h {
					return true
				}
			}
			return false
		default: // "band"
			return dirty.Intersects(int(bmp.Y), int(bmp.Height))
		}
	}
	killed := false
	var killTime float64

	// processRun mirrors PIIGate.processPDUs: maximal non-conflicting batch,
	// analyze, release, repeat. Returns true when the kill switch fired.
	processRun := func(pending []queuedEvent) (bool, error) {
		i := 0
		for i < len(pending) {
			j := i
			for j < len(pending) {
				bmp := pending[j].ev.Bitmap
				if j > i && conflicts(bmp) {
					// The state at risk is the band as it is RIGHT NOW
					// (about to be hidden by this patch).
					y0, y1 := paddedBand(int(bmp.Y), int(bmp.Height))
					hash := bandHash(y0, y1)
					pii, known := verdictCache[hash]
					switch {
					case cfg.verdictCache && known:
						if pii {
							cacheMaskedSkips++
						} else {
							cacheCleanSkips++
						}
					case cfg.captureStates:
						rows := make([]byte, (y1-y0)*w*4)
						copy(rows, fb[y0*w*4:y1*w*4])
						captures = append(captures, capturedBand{y0: y0, y1: y1, rows: rows, hash: hash})
						captured++
						captureBytes += len(rows)
					default:
						conflictSeals++
					}
					if !cfg.captureStates && !(cfg.verdictCache && known) {
						break
					}
				}
				if err := decodeAndComposite(fb, w, h, pending[j].ev, report); err != nil {
					fmt.Fprintf(os.Stderr, "warning: %v\n", err)
				} else {
					dirty.AddRect(int(bmp.Y), int(bmp.Height))
					batchRects = append(batchRects, rect{x: int(bmp.X), y: int(bmp.Y), w: int(bmp.Width), h: int(bmp.Height)})
				}
				j++
			}
			batches++
			sessionTime := pending[j-1].ev.Timestamp - baseTS
			if bands := dirty.TakeAndReset(); bands != nil || len(captures) > 0 {
				// Assemble the analysis sheet: the final state's dirty bands
				// plus every captured intermediate state, stacked into one
				// tall virtual framebuffer so the band-chunking OCR analyzes
				// all states in a single parallel pass.
				stackRows := 0
				for _, b := range bands {
					stackRows += b.Y1 - b.Y0
				}
				for _, c := range captures {
					stackRows += c.y1 - c.y0
				}
				sheet := make([]byte, 0, stackRows*w*4)
				var sheetBands []analyzer.YBand
				row := 0
				appendSegment := func(rows []byte) {
					n := len(rows) / (w * 4)
					sheet = append(sheet, rows...)
					sheetBands = append(sheetBands, analyzer.YBand{Y0: row, Y1: row + n})
					row += n
				}
				for _, b := range bands {
					appendSegment(fb[b.Y0*w*4 : b.Y1*w*4])
				}
				for _, c := range captures {
					appendSegment(c.rows)
				}

				detected, err := analyzeAndRecord(ctx, cfg, report, sheet, w, row, sheetBands,
					pending[j-1].ev.Index, sessionTime, time.Now(), time.Time{})
				if err != nil {
					return false, err
				}
				if detected && cfg.killOnDetected {
					killTime = sessionTime
					fmt.Printf("\n*** kill switch fired at session t=%.2fs: %d held frame(s) dropped, never forwarded ***\n",
						sessionTime, len(pending)-i)
					return true, nil
				}
				if cfg.verdictCache {
					// Record the analyzed states' verdicts, both at merged-band
					// granularity (what was analyzed) and per-patch padded
					// bands (what future conflicts will query). A detection
					// anywhere in the batch coarsely marks all its bands as
					// PII — conservative for the masked-release accounting.
					for _, b := range bands {
						verdictCache[bandHash(b.Y0, b.Y1)] = detected
					}
					for _, r := range batchRects {
						y0, y1 := paddedBand(r.y, r.h)
						verdictCache[bandHash(y0, y1)] = detected
					}
					for _, c := range captures {
						verdictCache[c.hash] = detected
					}
				}
				captures = captures[:0]
			}
			now := time.Now()
			for _, q := range pending[i:j] {
				holdLag.add(now.Sub(q.arrived))
			}
			released += j - i
			batchRects = batchRects[:0]
			i = j
		}
		return false, nil
	}

	running := true
	for running {
		select {
		case <-notify:
		case <-replayDone:
			running = false // final drain below, then exit
		}
		for {
			pending := takePending()
			if len(pending) == 0 {
				break
			}
			k, err := processRun(pending)
			if err != nil {
				return err
			}
			if k {
				killed = true
				running = false
				cancel()
				break
			}
		}
	}

	sessionDuration := cfg.frames[len(cfg.frames)-1].Timestamp - baseTS
	if killed {
		sessionDuration = killTime
	}
	report.print(time.Since(wallStart), sessionDuration)

	fmt.Printf("\n=== gate simulation (hold-and-release) ===\n")
	fmt.Printf("frames released:      %d of %d (held-and-dropped on kill: %d)\n",
		released, len(cfg.frames), len(cfg.frames)-released)
	fmt.Printf("batches analyzed:     %d (conflict seals: %d, %.1f frames/batch)\n",
		batches, conflictSeals, float64(released)/float64(max(batches, 1)))
	if cfg.verdictCache {
		fmt.Printf("verdict cache:        %d entries, %d clean skips, %d known-PII skips (seals avoided: %d; known-PII skips would need a masking mode, not implemented)\n",
			len(verdictCache), cacheCleanSkips, cacheMaskedSkips, cacheCleanSkips+cacheMaskedSkips)
	}
	if cfg.captureStates {
		fmt.Printf("captured states:      %d intermediate band states (%.1f MB copied) analyzed in-batch\n",
			captured, float64(captureBytes)/(1<<20))
	}
	if holdLag.count() > 0 {
		fmt.Printf("hold latency:         %s\n", holdLag.summary())
		fmt.Printf("p95 hold latency:     %s — added screen-update delay the user experiences under the guard\n",
			holdLag.percentile(0.95).Round(time.Millisecond))
	}
	return nil
}

// rect is a screen-space rectangle used for rect-level conflict detection.
type rect struct{ x, y, w, h int }

package analyzer

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/hoophq/hoop/gateway/rdp/ocr"
	"golang.org/x/sync/errgroup"
)

// YBand is a horizontal band of framebuffer rows [Y0, Y1).
// Bands always span the full canvas width: on-screen text is horizontal, so a
// full-width band contains complete text lines and is a contiguous (zero-copy)
// slice of an RGBA framebuffer.
type YBand struct {
	Y0, Y1 int
}

// Height returns the number of rows covered by the band.
func (b YBand) Height() int { return b.Y1 - b.Y0 }

// DirtyBands accumulates the vertical extents of dirty rectangles between two
// analysis snapshots. Each rect is expanded by a vertical padding (so text
// lines touching the dirty area are fully covered) and merged with adjacent
// or overlapping bands.
//
// The zero value is not usable; construct with NewDirtyBands. The type does
// no locking: callers sharing it across goroutines must synchronize
// externally (the same discipline as the framebuffer it describes).
type DirtyBands struct {
	canvasHeight int
	pad          int
	bands        []YBand // sorted by Y0, non-overlapping, non-adjacent
}

// DefaultBandPadding is the default vertical padding (in pixels) applied
// around dirty rectangles. It must cover at least one text line height so a
// partially repainted line is always OCR'd in full.
const DefaultBandPadding = 24

// NewDirtyBands creates an accumulator for a canvas of the given height.
// pad is the vertical padding applied around each dirty rect; values <= 0
// fall back to DefaultBandPadding. Use the same value as
// AnalysisParams.BandPadding so band accumulation and chunk overlap follow
// one padding policy.
func NewDirtyBands(canvasHeight, pad int) *DirtyBands {
	if pad <= 0 {
		pad = DefaultBandPadding
	}
	return &DirtyBands{canvasHeight: canvasHeight, pad: pad}
}

// AddRect records a dirty rectangle's vertical extent (x is ignored: bands
// span the full width). The extent is padded and merged into the band list.
func (d *DirtyBands) AddRect(y, height int) {
	if height <= 0 {
		return
	}
	y0 := y - d.pad
	y1 := y + height + d.pad
	if y0 < 0 {
		y0 = 0
	}
	if y1 > d.canvasHeight {
		y1 = d.canvasHeight
	}
	if y0 >= y1 {
		return
	}

	// Insert keeping the list sorted, then merge overlapping/adjacent bands.
	idx := len(d.bands)
	for i, b := range d.bands {
		if y0 < b.Y0 {
			idx = i
			break
		}
	}
	d.bands = append(d.bands, YBand{})
	copy(d.bands[idx+1:], d.bands[idx:])
	d.bands[idx] = YBand{Y0: y0, Y1: y1}

	merged := d.bands[:1]
	for _, b := range d.bands[1:] {
		last := &merged[len(merged)-1]
		if b.Y0 <= last.Y1 { // overlapping or adjacent
			if b.Y1 > last.Y1 {
				last.Y1 = b.Y1
			}
			continue
		}
		merged = append(merged, b)
	}
	d.bands = merged
}

// Bands returns the current merged band list. The returned slice is owned by
// the accumulator and is invalidated by AddRect/Reset; callers that retain it
// across mutations must copy it.
func (d *DirtyBands) Bands() []YBand { return d.bands }

// Empty reports whether no dirty area has been recorded since the last Reset.
func (d *DirtyBands) Empty() bool { return len(d.bands) == 0 }

// Intersects reports whether a rect's vertical extent, padded with the same
// policy as AddRect, overlaps any accumulated band. It answers "would this
// rect repaint (or touch the text lines of) rows already dirtied since the
// last Reset" — the conflict test used to split hold-and-release batches so
// no intermediate screen state escapes analysis.
func (d *DirtyBands) Intersects(y, height int) bool {
	if height <= 0 {
		return false
	}
	y0 := y - d.pad
	y1 := y + height + d.pad
	if y0 < 0 {
		y0 = 0
	}
	if y1 > d.canvasHeight {
		y1 = d.canvasHeight
	}
	for _, b := range d.bands {
		if y0 < b.Y1 && b.Y0 < y1 {
			return true
		}
	}
	return false
}

// CoveredRows returns the total number of framebuffer rows covered by the
// current bands — useful to decide between band analysis and a full-frame
// pass when most of the screen is dirty anyway.
func (d *DirtyBands) CoveredRows() int {
	total := 0
	for _, b := range d.bands {
		total += b.Height()
	}
	return total
}

// Reset clears the accumulated bands (call after each analyzed snapshot).
func (d *DirtyBands) Reset() { d.bands = d.bands[:0] }

// TakeAndReset atomically (with respect to the caller's lock) returns an
// owned copy of the current bands and clears the accumulator. Callers must
// hold the same lock that guards AddRect — this is the snapshot-time
// counterpart to the framebuffer copy.
func (d *DirtyBands) TakeAndReset() []YBand {
	if len(d.bands) == 0 {
		return nil
	}
	out := append([]YBand(nil), d.bands...)
	d.bands = d.bands[:0]
	return out
}

// offsetWords shifts word bounding boxes down by dy framebuffer rows,
// converting band-local OCR coordinates into full-screen coordinates.
func offsetWords(words []ocr.Word, dy int) {
	for i := range words {
		words[i].Top += dy
	}
}

const (
	// maxChunkRows is the maximum height of a single OCR invocation. Bands
	// taller than this (e.g. a full-screen repaint) are split into chunks
	// that are OCR'd in parallel, bounding the worst-case OCR latency by
	// the chunk cost instead of the full canvas cost.
	maxChunkRows = 256
	// DefaultMaxOCRConcurrency is the default cap on concurrent tesseract
	// processes. Each tesseract run is effectively single-threaded, so one
	// process per core is the sweet spot; the cap bounds memory and process
	// churn on large machines. Override per-deployment via
	// AnalysisParams.MaxOCRConcurrency.
	DefaultMaxOCRConcurrency = 8
)

// ocrChunk is a band slice prepared for one OCR invocation. The window is
// what gets OCR'd; ownership decides which detected words are kept, so words
// in the overlap between adjacent chunks are attributed to exactly one chunk.
type ocrChunk struct {
	win YBand // rows passed to OCR (ownership expanded by pad, clamped to the band)
	own YBand // rows this chunk owns: keep words whose vertical center falls here
}

// splitBands slices bands taller than maxRows into chunks with pad rows of
// overlap on each side, so a text line straddling a chunk boundary is fully
// visible to the chunk that owns its center.
func splitBands(bands []YBand, maxRows, pad int) []ocrChunk {
	var chunks []ocrChunk
	for _, band := range bands {
		if band.Height() <= maxRows+pad {
			chunks = append(chunks, ocrChunk{win: band, own: band})
			continue
		}
		for y := band.Y0; y < band.Y1; y += maxRows {
			own := YBand{Y0: y, Y1: y + maxRows}
			if own.Y1 > band.Y1 {
				own.Y1 = band.Y1
			}
			win := YBand{Y0: own.Y0 - pad, Y1: own.Y1 + pad}
			if win.Y0 < band.Y0 {
				win.Y0 = band.Y0
			}
			if win.Y1 > band.Y1 {
				win.Y1 = band.Y1
			}
			chunks = append(chunks, ocrChunk{win: win, own: own})
		}
	}
	return chunks
}

// ownsWord reports whether the chunk owns a word whose coordinates are
// already in full-screen space.
func (c ocrChunk) ownsWord(w ocr.Word) bool {
	center := w.Top + w.Height/2
	return center >= c.own.Y0 && center < c.own.Y1
}

// AnalyzeFramebufferBands runs OCR over the given horizontal bands of the
// framebuffer (instead of the full frame) and then the same Presidio analysis
// as AnalyzeFramebuffer. Word coordinates are translated back to full-screen
// space, so detections are indistinguishable from full-frame analysis.
//
// This is the incremental path for realtime analysis: OCR cost scales with
// the screen area that actually changed since the previous snapshot rather
// than the full canvas. PII that was already on screen before the bands were
// dirtied is not re-detected — callers own that semantic (for a kill switch,
// PII is detected when it is painted, which is exactly when it appears).
//
// Contract: framebuffer must be RGBA (4 bytes/pixel, top-down) with
// len >= fbWidth*fbHeight*4; bands must be sorted, non-overlapping and within
// [0, fbHeight) — as produced by DirtyBands. The function does no locking.
func AnalyzeFramebufferBands(
	ctx context.Context,
	framebuffer []byte,
	fbWidth, fbHeight int,
	bands []YBand,
	sessionID string,
	frameIndex int,
	timestamp float64,
	presidio *PresidioClient,
	params AnalysisParams,
) (*SnapshotResult, error) {
	res := &SnapshotResult{Bands: len(bands)}

	if fbWidth <= 0 || fbHeight <= 0 {
		return res, fmt.Errorf("invalid framebuffer dimensions %dx%d", fbWidth, fbHeight)
	}
	stride := fbWidth * 4
	if need := stride * fbHeight; len(framebuffer) < need {
		return res, fmt.Errorf("framebuffer too short: got %d bytes, need %d for %dx%d RGBA",
			len(framebuffer), need, fbWidth, fbHeight)
	}

	for _, band := range bands {
		if band.Y0 < 0 || band.Y1 > fbHeight || band.Y0 >= band.Y1 {
			return res, fmt.Errorf("invalid band [%d,%d) for framebuffer height %d", band.Y0, band.Y1, fbHeight)
		}
	}

	// Tall bands (e.g. a full-screen repaint) are split into chunks OCR'd in
	// parallel: tesseract runs one process per chunk, so the worst-case OCR
	// latency is bounded by the chunk cost, not the full canvas cost. The
	// chunk overlap follows the same padding policy as the dirty-band
	// accumulator (see AnalysisParams.BandPadding).
	chunkPad := params.BandPadding
	if chunkPad <= 0 {
		chunkPad = DefaultBandPadding
	}
	chunks := splitBands(bands, maxChunkRows, chunkPad)
	chunkWords := make([][]ocr.Word, len(chunks))

	concurrency := params.MaxOCRConcurrency
	if concurrency <= 0 {
		concurrency = DefaultMaxOCRConcurrency
		if n := runtime.NumCPU(); n < concurrency {
			concurrency = n
		}
	}

	ocrStart := time.Now()
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)
	for i, chunk := range chunks {
		g.Go(func() error {
			crop := framebuffer[chunk.win.Y0*stride : chunk.win.Y1*stride]
			chunkResult, err := ocr.ExtractWords(gctx, crop, fbWidth, chunk.win.Height())
			if err != nil {
				return fmt.Errorf("OCR failed on chunk [%d,%d): %w", chunk.win.Y0, chunk.win.Y1, err)
			}
			offsetWords(chunkResult.Words, chunk.win.Y0)
			// Keep only owned words so overlap regions are not duplicated.
			owned := chunkResult.Words[:0]
			for _, w := range chunkResult.Words {
				if chunk.ownsWord(w) {
					owned = append(owned, w)
				}
			}
			chunkWords[i] = owned
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		res.OCRDuration = time.Since(ocrStart)
		return res, err
	}
	res.OCRDuration = time.Since(ocrStart)

	// Concatenate in chunk order. Within a chunk tesseract emits reading
	// order; ownership-by-center assigns whole lines to one chunk (the pad
	// guarantees the owning chunk sees the full line), so concatenation
	// preserves top-to-bottom reading order across seams. The only known
	// edge is a single line whose words have sufficiently different heights
	// that their centers straddle a seam — its words stay adjacent in the
	// text but may locally reorder, potentially missing one entity match
	// until the next repaint.
	var allWords []ocr.Word
	for _, words := range chunkWords {
		allWords = append(allWords, words...)
	}

	res.OCRWords = len(allWords)
	if len(allWords) == 0 {
		return res, nil
	}

	// Rebuild the text exactly like ocr.ExtractWords does (words joined by
	// single spaces) so Presidio character offsets line up with word ranges.
	textParts := make([]string, len(allWords))
	for i, w := range allWords {
		textParts[i] = w.Text
	}
	text := strings.Join(textParts, " ")
	res.OCRTextLen = len(text)

	return analyzeText(ctx, res, text, allWords, sessionID, frameIndex, timestamp, presidio, params)
}

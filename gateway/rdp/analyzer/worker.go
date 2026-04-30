package analyzer

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/rdp/ocr"
	"github.com/hoophq/hoop/gateway/rdp/rle"
)

const (
	// defaultWorkerCount is the number of worker goroutines if RDP_ANALYSIS_WORKERS is not set.
	defaultWorkerCount = 2
	// pollInterval is how long workers sleep when the queue is empty.
	pollInterval = 5 * time.Second
)

// resolveWorkerCount reads RDP_ANALYSIS_WORKERS and returns the configured count.
// An unset env var returns defaultWorkerCount. Any value <= 0 or an unparseable value
// returns 0 (feature explicitly disabled).
func resolveWorkerCount() int {
	envVal := os.Getenv("RDP_ANALYSIS_WORKERS")
	if envVal == "" {
		return defaultWorkerCount
	}
	n, err := strconv.Atoi(envVal)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// IsEnabled reports whether the RDP PII analysis pipeline is currently active.
// It is enabled only when Presidio is configured and the worker pool is running
// (analyzerURL is set, RDP_ANALYSIS_WORKERS resolves to > 0, and tesseract is in PATH).
//
// Callers should gate any work that depends on the pipeline (e.g. enqueueing
// analysis jobs on session close) to avoid leaving sessions stuck in 'pending'.
func IsEnabled(analyzerURL string) bool {
	return analyzerURL != "" && resolveWorkerCount() > 0 && ocr.IsAvailable()
}

// BitmapEvent mirrors the struct in gateway/rdp/recorder.go.
// Duplicated here to avoid circular imports.
type BitmapEvent struct {
	X            uint16 `json:"x"`
	Y            uint16 `json:"y"`
	Width        uint16 `json:"width"`
	Height       uint16 `json:"height"`
	BitsPerPixel uint16 `json:"bits_per_pixel"`
	Compressed   bool   `json:"compressed"`
	Data         []byte `json:"data"`
}

// wordRange tracks a word's character range in the reconstructed text string.
type wordRange struct {
	start int // inclusive byte offset in full text
	end   int // exclusive byte offset in full text
	word  ocr.Word
}

// StartWorkerPool launches the RDP analysis worker pool.
// It reads the worker count from RDP_ANALYSIS_WORKERS env var.
// Workers are started only if analyzerURL is non-empty (Presidio is configured),
// RDP_ANALYSIS_WORKERS resolves to a positive count, and tesseract is in PATH.
//
// Call this once at gateway boot. The pool runs until ctx is cancelled.
func StartWorkerPool(ctx context.Context, analyzerURL string) {
	if analyzerURL == "" {
		log.Infof("rdp-analyzer: Presidio not configured, skipping worker pool startup")
		return
	}

	workerCount := resolveWorkerCount()
	if workerCount == 0 {
		log.Infof("rdp-analyzer: RDP_ANALYSIS_WORKERS=0, skipping worker pool startup")
		return
	}

	if !ocr.IsAvailable() {
		log.Warnf("rdp-analyzer: tesseract not found in PATH, skipping worker pool startup")
		return
	}

	// Rescue jobs left in 'running' state from a previous crash/restart.
	// Without this, those jobs would be orphaned forever since ClaimRDPAnalysisJob
	// only picks up 'pending' and 'failed' jobs.
	if rescued, err := models.ResetOrphanedRDPAnalysisJobs(models.DB); err != nil {
		log.Warnf("rdp-analyzer: failed to reset orphaned jobs: %v", err)
	} else if rescued > 0 {
		log.Infof("rdp-analyzer: reset %d orphaned running job(s) to pending", rescued)
	}

	presidioClient := NewPresidioClient(analyzerURL)

	log.Infof("rdp-analyzer: starting %d workers (presidio=%s)", workerCount, analyzerURL)

	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			runWorker(ctx, workerID, presidioClient)
		}(i)
	}

	go func() {
		wg.Wait()
		log.Infof("rdp-analyzer: all workers stopped")
	}()
}

// runWorker is the main loop for a single worker goroutine.
func runWorker(ctx context.Context, workerID int, presidio *PresidioClient) {
	logger := log.With("worker", fmt.Sprintf("rdp-analyzer-%d", workerID))
	logger.Infof("worker started")

	for {
		select {
		case <-ctx.Done():
			logger.Infof("worker stopping (context cancelled)")
			return
		default:
		}

		job, err := models.ClaimRDPAnalysisJob(models.DB)
		if err != nil {
			logger.Errorf("failed to claim job: %v", err)
			sleep(ctx, pollInterval)
			continue
		}

		if job == nil {
			// No jobs available, wait before polling again
			sleep(ctx, pollInterval)
			continue
		}

		logger.With("sid", job.SessionID, "job_id", job.ID, "attempt", job.Attempt).
			Infof("processing job")

		// Mark the session as "analyzing"
		_ = models.UpdateSessionRDPAnalysisStatus(job.OrgID, job.SessionID, "analyzing")

		if err := processJob(ctx, job, presidio); err != nil {
			logger.With("sid", job.SessionID, "job_id", job.ID).
				Errorf("job failed: %v", err)
			errMsg := err.Error()
			if len(errMsg) > 1000 {
				errMsg = errMsg[:1000]
			}
			_ = models.FailRDPAnalysisJob(models.DB, job.ID, errMsg)
			_ = models.UpdateSessionRDPAnalysisStatus(job.OrgID, job.SessionID, "failed")
			continue
		}

		_ = models.CompleteRDPAnalysisJob(models.DB, job.ID)
		_ = models.UpdateSessionRDPAnalysisStatus(job.OrgID, job.SessionID, "done")

		logger.With("sid", job.SessionID, "job_id", job.ID).
			Infof("job completed successfully")

		// Immediately re-poll for next job (no sleep)
	}
}

const (
	// defaultCanvasWidth / Height are fallbacks if the session metrics are missing.
	defaultCanvasWidth  = 1280
	defaultCanvasHeight = 720
)

// AnalysisParams holds tunable parameters for the RDP PII analysis pipeline.
// Values are read from env vars (via appconfig) at worker startup.
type AnalysisParams struct {
	ScoreThreshold   float64  // Minimum Presidio score (env: RDP_PII_SCORE_THRESHOLD, default 0.9)
	EntityDenylist   []string // Entity types to exclude (env: RDP_PII_ENTITY_DENYLIST, default "DATE_TIME,NRP")
	SnapshotInterval float64  // Seconds between snapshots (env: RDP_PII_SNAPSHOT_INTERVAL, default 0.25)
}

// DefaultAnalysisParams returns the analysis parameters from appconfig (env vars).
func DefaultAnalysisParams() AnalysisParams {
	cfg := appconfig.Get()
	return AnalysisParams{
		ScoreThreshold:   cfg.RDPPIIScoreThreshold(),
		EntityDenylist:   cfg.RDPPIIEntityDenylist(),
		SnapshotInterval: cfg.RDPPIISnapshotInterval(),
	}
}

// isEntityDenied checks if an entity type is in the denylist.
func isEntityDenied(entityType string, denylist []string) bool {
	for _, denied := range denylist {
		if denied == entityType {
			return true
		}
	}
	return false
}

// getCanvasDimensions reads the canvas size from session metrics, falling back to defaults.
func getCanvasDimensions(session *models.Session) (int, int) {
	w, h := defaultCanvasWidth, defaultCanvasHeight
	if session.Metrics != nil {
		if cw, ok := session.Metrics["canvas_width"]; ok {
			switch v := cw.(type) {
			case float64:
				w = int(v)
			case json.Number:
				if n, err := v.Int64(); err == nil {
					w = int(n)
				}
			}
		}
		if ch, ok := session.Metrics["canvas_height"]; ok {
			switch v := ch.(type) {
			case float64:
				h = int(v)
			case json.Number:
				if n, err := v.Int64(); err == nil {
					h = int(n)
				}
			}
		}
	}
	if w <= 0 {
		w = defaultCanvasWidth
	}
	if h <= 0 {
		h = defaultCanvasHeight
	}
	return w, h
}

// compositeBitmap draws a decoded RGBA bitmap patch onto the full framebuffer at (dstX, dstY).
func compositeBitmap(framebuffer []byte, fbWidth, fbHeight int, patch []byte, patchW, patchH, dstX, dstY int) {
	for row := 0; row < patchH; row++ {
		fbY := dstY + row
		if fbY < 0 || fbY >= fbHeight {
			continue
		}
		srcOff := row * patchW * 4
		dstOff := (fbY*fbWidth + dstX) * 4
		for col := 0; col < patchW; col++ {
			fbX := dstX + col
			if fbX < 0 || fbX >= fbWidth {
				continue
			}
			si := srcOff + col*4
			di := dstOff + col*4
			if si+3 < len(patch) && di+3 < len(framebuffer) {
				framebuffer[di] = patch[si]
				framebuffer[di+1] = patch[si+1]
				framebuffer[di+2] = patch[si+2]
				framebuffer[di+3] = patch[si+3]
			}
		}
	}
}

// sampleFramebuffer extracts every 64th scanline from the framebuffer for fast hashing.
// This captures enough visual information to detect meaningful screen changes while
// avoiding the cost of hashing the entire framebuffer (which can be 8MB+ for 2048x1083).
func sampleFramebuffer(fb []byte, width, height int) []byte {
	stride := width * 4
	var sample []byte
	for y := 0; y < height; y += 64 {
		offset := y * stride
		end := offset + stride
		if end > len(fb) {
			end = len(fb)
		}
		sample = append(sample, fb[offset:end]...)
	}
	return sample
}

// analyzeSnapshot runs OCR + Presidio on the full framebuffer and returns detections.
// The params control score threshold and entity denylist filtering.
func analyzeSnapshot(
	ctx context.Context,
	framebuffer []byte,
	fbWidth, fbHeight int,
	sessionID string,
	frameIndex int,
	timestamp float64,
	presidio *PresidioClient,
	params AnalysisParams,
) ([]models.RDPEntityDetection, map[string]int64, error) {
	ocrResult, err := ocr.ExtractWords(ctx, framebuffer, fbWidth, fbHeight)
	if err != nil {
		return nil, nil, fmt.Errorf("OCR failed: %w", err)
	}
	if ocrResult.Text == "" || len(ocrResult.Words) == 0 {
		return nil, nil, nil
	}

	results, err := presidio.Analyze(ctx, ocrResult.Text, params.ScoreThreshold)
	if err != nil {
		return nil, nil, fmt.Errorf("Presidio failed: %w", err)
	}

	// Filter out denied entity types
	if len(params.EntityDenylist) > 0 {
		filtered := results[:0]
		for _, r := range results {
			if !isEntityDenied(r.EntityType, params.EntityDenylist) {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}

	counts := AggregateResults(results)

	var detections []models.RDPEntityDetection
	if len(results) > 0 {
		wordRanges := buildWordRanges(ocrResult.Words)
		for _, result := range results {
			bbox := mapEntityToPixelBBox(result, wordRanges)
			if bbox == nil {
				continue
			}
			// Coordinates are already in screen-space (full framebuffer)
			detections = append(detections, models.RDPEntityDetection{
				SessionID:  sessionID,
				FrameIndex: frameIndex,
				Timestamp:  timestamp,
				EntityType: result.EntityType,
				Score:      result.Score,
				X:          bbox.x,
				Y:          bbox.y,
				Width:      bbox.w,
				Height:     bbox.h,
			})
		}
	}

	return detections, counts, nil
}

// processJob does the actual analysis work for a single job.
//
// It reconstructs a full RGBA framebuffer from the differential bitmap patches,
// then periodically snapshots the full frame for OCR + Presidio analysis.
// This ensures PII that spans multiple bitmap updates (e.g. a long email address)
// is detected correctly, since OCR sees the complete screen content.
func processJob(ctx context.Context, job *models.RDPAnalysisJob, presidio *PresidioClient) error {
	// 1. Fetch the session and its blob stream
	session, err := models.GetSessionByID(job.OrgID, job.SessionID)
	if err != nil {
		return fmt.Errorf("failed to fetch session: %w", err)
	}

	blob, err := session.GetBlobStream()
	if err != nil {
		return fmt.Errorf("failed to fetch blob stream: %w", err)
	}
	if blob == nil || len(blob.BlobStream) == 0 {
		return nil // No recording data, nothing to analyze
	}

	// 2. Parse the blob stream (JSON array of events)
	var events []json.RawMessage
	if err := json.Unmarshal(blob.BlobStream, &events); err != nil {
		return fmt.Errorf("failed to parse blob stream: %w", err)
	}

	// 3. Allocate the full-screen framebuffer and read analysis params
	fbWidth, fbHeight := getCanvasDimensions(session)
	framebuffer := make([]byte, fbWidth*fbHeight*4) // RGBA, initialized to black (zeroes)
	params := DefaultAnalysisParams()

	log.With("sid", job.SessionID).
		Infof("rdp-analyzer: params score_threshold=%.2f, snapshot_interval=%.1fs, entity_denylist=%v",
			params.ScoreThreshold, params.SnapshotInterval, params.EntityDenylist)

	// 4. Iterate events, composite patches, and snapshot periodically
	aggregatedCounts := make(map[string]int64)
	var allDetections []models.RDPEntityDetection
	var prevTextHash [32]byte
	var prevFBHash [32]byte
	snapshotsRun := 0
	bitmapsComposited := 0
	lastSnapshotTime := -params.SnapshotInterval // ensure first eligible frame triggers a snapshot
	fbDirty := false                             // has the framebuffer changed since last snapshot?
	var lastEventTimestamp float64               // timestamp of the most recent bitmap event
	frameIndex := 0

	for _, rawEvent := range events {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Parse the event: [timestamp, type, base64data]
		var event [3]json.RawMessage
		if err := json.Unmarshal(rawEvent, &event); err != nil {
			continue
		}

		var eventType string
		if err := json.Unmarshal(event[1], &eventType); err != nil || eventType != "b" {
			continue
		}

		var timestamp float64
		if err := json.Unmarshal(event[0], &timestamp); err != nil {
			continue
		}

		var b64Str string
		if err := json.Unmarshal(event[2], &b64Str); err != nil {
			frameIndex++
			continue
		}
		bitmapJSON, err := base64.StdEncoding.DecodeString(b64Str)
		if err != nil {
			frameIndex++
			continue
		}

		var bmpEvent BitmapEvent
		if err := json.Unmarshal(bitmapJSON, &bmpEvent); err != nil {
			frameIndex++
			continue
		}

		currentFrameIndex := frameIndex
		frameIndex++

		if len(bmpEvent.Data) == 0 || bmpEvent.Width == 0 || bmpEvent.Height == 0 {
			continue
		}

		// Decompress the patch to RGBA
		patchW := int(bmpEvent.Width)
		patchH := int(bmpEvent.Height)
		bpp := int(bmpEvent.BitsPerPixel)

		var rgba []byte
		if bmpEvent.Compressed {
			rgba, err = rle.DecompressToRGBA(bmpEvent.Data, patchW, patchH, bpp)
		} else {
			rgba, err = rle.ToRGBA(bmpEvent.Data, patchW, patchH, bpp)
		}
		if err != nil {
			log.With("sid", job.SessionID).Debugf("failed to decode bitmap frame %d: %v", currentFrameIndex, err)
			continue
		}

		// Composite the patch onto the full framebuffer
		compositeBitmap(framebuffer, fbWidth, fbHeight, rgba, patchW, patchH, int(bmpEvent.X), int(bmpEvent.Y))
		fbDirty = true
		lastEventTimestamp = timestamp
		bitmapsComposited++

		// Check if it's time for a snapshot (every params.SnapshotInterval of session time)
		if (timestamp - lastSnapshotTime) < params.SnapshotInterval {
			continue
		}

		// Take a snapshot of the full framebuffer
		lastSnapshotTime = timestamp
		fbDirty = false

		// Quick framebuffer dedup: sample a few scanlines and hash them.
		// This avoids expensive OCR+Presidio calls when only minor changes
		// happened (cursor blink, clock tick, etc.)
		fbSample := sampleFramebuffer(framebuffer, fbWidth, fbHeight)
		fbHash := sha256.Sum256(fbSample)
		if fbHash == prevFBHash {
			continue
		}
		prevFBHash = fbHash

		// Text dedup: hash the full framebuffer is too expensive, so we still
		// dedup based on OCR text output. We run OCR and if the text is identical
		// to the previous snapshot, skip the Presidio call.
		detections, counts, analyzeErr := analyzeSnapshot(
			ctx, framebuffer, fbWidth, fbHeight,
			job.SessionID, currentFrameIndex, timestamp, presidio, params,
		)
		if analyzeErr != nil {
			log.With("sid", job.SessionID).Debugf("snapshot analysis failed at t=%.1fs: %v", timestamp, analyzeErr)
			continue
		}

		snapshotsRun++

		if len(detections) == 0 && len(counts) == 0 {
			continue
		}

		// Detection dedup: skip writing duplicate rows when the same entities
		// are detected at the same bounding boxes as the previous snapshot.
		// This avoids flooding the detections table during long stretches of
		// identical screen content that still pass the framebuffer-sample hash
		// (e.g. anti-aliased text that pixel-samples differently but OCRs the same).
		var bboxFingerprint string
		for _, d := range detections {
			bboxFingerprint += fmt.Sprintf("%s:%d:%d:%d:%d,", d.EntityType, d.X, d.Y, d.Width, d.Height)
		}
		bboxHash := sha256.Sum256([]byte(bboxFingerprint))
		if bboxHash == prevTextHash && len(detections) > 0 {
			continue
		}
		prevTextHash = bboxHash

		for infoType, count := range counts {
			aggregatedCounts[infoType] += count
		}
		allDetections = append(allDetections, detections...)
	}

	// Final snapshot: if the framebuffer was modified after the last snapshot, analyze it
	if fbDirty {
		detections, counts, analyzeErr := analyzeSnapshot(
			ctx, framebuffer, fbWidth, fbHeight,
			job.SessionID, frameIndex-1, lastEventTimestamp, presidio, params,
		)
		if analyzeErr == nil && (len(detections) > 0 || len(counts) > 0) {
			snapshotsRun++
			for infoType, count := range counts {
				aggregatedCounts[infoType] += count
			}
			allDetections = append(allDetections, detections...)
		}
	}

	log.With("sid", job.SessionID).
		Infof("rdp-analyzer: bitmaps=%d composited, snapshots=%d analyzed, entities=%d types, detections=%d",
			bitmapsComposited, snapshotsRun, len(aggregatedCounts), len(allDetections))

	// 5. Write aggregate results to session_metrics and sessions.metrics
	if len(aggregatedCounts) > 0 {
		if err := models.IncrementSessionAnalyzedMetrics(models.DB, job.SessionID, aggregatedCounts); err != nil {
			log.With("sid", job.SessionID).Warnf("failed to write session_metrics: %v", err)
		}
		if err := models.UpdateSessionAnalyzerMetrics(job.OrgID, job.SessionID, aggregatedCounts); err != nil {
			log.With("sid", job.SessionID).Warnf("failed to update session analyzer metrics: %v", err)
		}
	}

	// 6. Bulk-insert per-frame entity detections
	if len(allDetections) > 0 {
		if err := models.BulkInsertRDPEntityDetections(allDetections); err != nil {
			log.With("sid", job.SessionID).Warnf("failed to write entity detections: %v", err)
		}
	}

	return nil
}

// buildWordRanges constructs a character-offset index from OCR words.
// The full text is reconstructed as "word1 word2 word3..." (space-separated),
// and each wordRange records the start/end byte offsets of each word in that string.
func buildWordRanges(words []ocr.Word) []wordRange {
	ranges := make([]wordRange, 0, len(words))
	offset := 0
	for _, w := range words {
		end := offset + len(w.Text)
		ranges = append(ranges, wordRange{
			start: offset,
			end:   end,
			word:  w,
		})
		offset = end + 1 // +1 for the space separator
	}
	return ranges
}

// pixelBBox is a bounding box in bitmap-local pixel coordinates.
type pixelBBox struct {
	x, y, w, h int
}

// mapEntityToPixelBBox maps a Presidio AnalyzerResult (character offsets) to a merged
// bounding box from the OCR words that overlap the entity's character range.
// Returns nil if no overlapping words are found.
func mapEntityToPixelBBox(entity AnalyzerResult, ranges []wordRange) *pixelBBox {
	var minX, minY, maxX2, maxY2 int
	found := false

	for _, wr := range ranges {
		// Check if this word overlaps the entity's character range [Start, End)
		if wr.end <= entity.Start || wr.start >= entity.End {
			continue // No overlap
		}

		x2 := wr.word.Left + wr.word.Width
		y2 := wr.word.Top + wr.word.Height

		if !found {
			minX = wr.word.Left
			minY = wr.word.Top
			maxX2 = x2
			maxY2 = y2
			found = true
		} else {
			if wr.word.Left < minX {
				minX = wr.word.Left
			}
			if wr.word.Top < minY {
				minY = wr.word.Top
			}
			if x2 > maxX2 {
				maxX2 = x2
			}
			if y2 > maxY2 {
				maxY2 = y2
			}
		}
	}

	if !found {
		return nil
	}

	return &pixelBBox{
		x: minX,
		y: minY,
		w: maxX2 - minX,
		h: maxY2 - minY,
	}
}

// sleep waits for duration or until context is cancelled.
func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

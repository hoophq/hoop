package main

import (
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hoophq/hoop/gateway/rdp/analyzer"
	"github.com/hoophq/hoop/gateway/rdp/ocr"
	"github.com/hoophq/hoop/gateway/rdp/rle"
)

type benchConfig struct {
	fixture        *Fixture
	frames         []frameEvent
	presidio       *analyzer.PresidioClient
	params         analyzer.AnalysisParams
	speed          float64
	dumpDir        string
	verbose        bool
	killOnDetected bool
}

// runBench replays a fixture through the PII detection pipeline.
func runBench(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	input := fs.String("i", "recording.json", "input fixture file (from 'rdpbench fetch')")
	presidioURL := fs.String("presidio", envOr("MSPRESIDIO_ANALYZER_URL", "http://127.0.0.1:5002"), "presidio analyzer base URL")
	pace := fs.String("pace", "fast", "replay pacing: 'fast' (as fast as possible) or 'realtime' (original timing)")
	interval := fs.Float64("interval", 0.25, "snapshot interval in seconds of session time")
	score := fs.Float64("score", 0.9, "presidio minimum score threshold")
	denylist := fs.String("denylist", "DATE_TIME,NRP", "comma-separated entity types to ignore")
	speed := fs.Float64("speed", 1.0, "realtime pace speed multiplier (2.0 = replay twice as fast)")
	dumpDir := fs.String("dump", "", "optional directory to dump analyzed snapshots as PNG")
	kill := fs.Bool("kill", false, "stop the replay at the first detection (simulates the kill switch)")
	verbose := fs.Bool("v", false, "log every analyzed snapshot")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if !ocr.IsAvailable() {
		return fmt.Errorf("tesseract not found in PATH (install tesseract-ocr or run inside the gateway image)")
	}
	if *interval <= 0 {
		return fmt.Errorf("-interval must be > 0")
	}
	if *speed <= 0 {
		return fmt.Errorf("-speed must be > 0")
	}
	if *score < 0 || *score > 1 {
		return fmt.Errorf("-score must be between 0 and 1")
	}

	fixture, err := loadFixture(*input)
	if err != nil {
		return err
	}
	frames, err := parseEvents(fixture.Events)
	if err != nil {
		return err
	}
	if len(frames) == 0 {
		return fmt.Errorf("fixture contains no replayable bitmap frames")
	}
	// Timing fidelity is the whole point of this tool: refuse fixtures with
	// corrupt timestamps instead of silently producing misleading numbers.
	for i, f := range frames {
		if math.IsNaN(f.Timestamp) || math.IsInf(f.Timestamp, 0) {
			return fmt.Errorf("fixture frame %d has an invalid timestamp", i)
		}
		if i > 0 && f.Timestamp < frames[i-1].Timestamp {
			return fmt.Errorf("fixture timestamps are not monotonic at frame %d (%.3f < %.3f)",
				i, f.Timestamp, frames[i-1].Timestamp)
		}
	}

	var denied []string
	for _, e := range strings.Split(*denylist, ",") {
		if e = strings.TrimSpace(e); e != "" {
			denied = append(denied, e)
		}
	}

	cfg := &benchConfig{
		fixture:  fixture,
		frames:   frames,
		presidio: analyzer.NewPresidioClient(*presidioURL),
		params: analyzer.AnalysisParams{
			ScoreThreshold:   *score,
			EntityDenylist:   denied,
			SnapshotInterval: *interval,
		},
		speed:          *speed,
		dumpDir:        *dumpDir,
		verbose:        *verbose,
		killOnDetected: *kill,
	}

	if cfg.dumpDir != "" {
		if err := os.MkdirAll(cfg.dumpDir, 0o755); err != nil {
			return fmt.Errorf("failed to create dump dir: %w", err)
		}
	}

	// Fail fast if Presidio is unreachable: benchmark numbers without the
	// analyzer stage would be silently meaningless.
	if _, err := cfg.presidio.Analyze(context.Background(), "warmup probe john.smith@example.com", cfg.params.ScoreThreshold); err != nil {
		return fmt.Errorf("presidio analyzer not reachable at %s: %w", *presidioURL, err)
	}

	sessionDuration := frames[len(frames)-1].Timestamp - frames[0].Timestamp
	fmt.Printf("fixture: session=%s canvas=%dx%d bitmaps=%d session-duration=%.1fs\n",
		fixture.SessionID, fixture.CanvasWidth, fixture.CanvasHeight, len(frames), sessionDuration)
	fmt.Printf("params:  pace=%s interval=%.2fs score=%.2f denylist=%v speed=%.1fx\n\n",
		*pace, *interval, *score, denied, *speed)

	switch *pace {
	case "fast":
		return runFastPace(cfg)
	case "realtime":
		return runRealtimePace(cfg)
	default:
		return fmt.Errorf("invalid -pace %q: must be 'fast' or 'realtime'", *pace)
	}
}

// benchReport accumulates pipeline measurements for the final summary.
// All mutations and reads go through mu: in realtime mode the replayer
// goroutine records decode/composite stats concurrently with the analyzer
// loop recording snapshot stats.
type benchReport struct {
	mu sync.Mutex

	decode    durationStats
	composite durationStats
	ocrStage  durationStats
	presidio  durationStats
	analyze   durationStats // full AnalyzeFramebuffer (ocr + presidio + bbox mapping)

	bitmaps         int
	snapshots       int
	dedupSkips      int
	emptySnapshots  int
	detectionEvents []detectionEvent
}

func (r *benchReport) addDedupSkip() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dedupSkips++
}

type detectionEvent struct {
	sessionTime float64 // session-relative timestamp of the snapshot
	entities    map[string]int64
	analysisLag time.Duration // copy/snapshot start -> detection available
	worstLag    time.Duration // previous snapshot -> detection available (realtime only)
}

func (r *benchReport) print(wallDuration time.Duration, sessionDuration float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Printf("\n=== pipeline stage latencies ===\n")
	fmt.Printf("rle-decode:  %s\n", r.decode.summary())
	fmt.Printf("composite:   %s\n", r.composite.summary())
	fmt.Printf("ocr:         %s\n", r.ocrStage.summary())
	fmt.Printf("presidio:    %s\n", r.presidio.summary())
	fmt.Printf("snapshot:    %s\n", r.analyze.summary())

	fmt.Printf("\n=== pipeline summary ===\n")
	fmt.Printf("bitmaps composited:   %d\n", r.bitmaps)
	fmt.Printf("snapshots analyzed:   %d (dedup-skipped=%d, empty=%d)\n", r.snapshots, r.dedupSkips, r.emptySnapshots)
	fmt.Printf("wall time:            %s (session time %.1fs, %.1fx realtime)\n",
		wallDuration.Round(time.Millisecond), sessionDuration,
		sessionDuration/wallDuration.Seconds())

	if r.snapshots > 0 {
		budget := r.analyze.percentile(0.95)
		fmt.Printf("p95 snapshot latency: %s — this is the floor for time-to-kill once PII is on screen\n",
			budget.Round(time.Millisecond))
	}

	fmt.Printf("\n=== detections ===\n")
	if len(r.detectionEvents) == 0 {
		fmt.Printf("no PII detected\n")
		return
	}
	for _, ev := range r.detectionEvents {
		var parts []string
		for entity, count := range ev.entities {
			parts = append(parts, fmt.Sprintf("%s x%d", entity, count))
		}
		line := fmt.Sprintf("t=%6.2fs  %s  (analysis-lag=%s",
			ev.sessionTime, strings.Join(parts, ", "), ev.analysisLag.Round(time.Millisecond))
		if ev.worstLag > 0 {
			line += fmt.Sprintf(", worst-case-lag=%s", ev.worstLag.Round(time.Millisecond))
		}
		fmt.Println(line + ")")
	}
	first := r.detectionEvents[0]
	fmt.Printf("\nfirst detection at session t=%.2fs; a kill switch would have fired %s after the snapshot was taken\n",
		first.sessionTime, first.analysisLag.Round(time.Millisecond))
}

// decodeAndComposite decodes one bitmap event and composites it onto fb,
// recording stage timings into the report.
func decodeAndComposite(fb []byte, w, h int, ev frameEvent, report *benchReport) error {
	bmp := ev.Bitmap
	start := time.Now()
	var rgba []byte
	var err error
	if bmp.Compressed {
		rgba, err = rle.DecompressToRGBA(bmp.Data, int(bmp.Width), int(bmp.Height), int(bmp.BitsPerPixel))
	} else {
		rgba, err = rle.ToRGBA(bmp.Data, int(bmp.Width), int(bmp.Height), int(bmp.BitsPerPixel))
	}
	decodeDur := time.Since(start)
	if err != nil {
		report.mu.Lock()
		report.decode.add(decodeDur)
		report.mu.Unlock()
		return fmt.Errorf("frame %d: failed to decode bitmap: %w", ev.Index, err)
	}

	start = time.Now()
	analyzer.CompositeBitmap(fb, w, h, rgba, int(bmp.Width), int(bmp.Height), int(bmp.X), int(bmp.Y))
	compositeDur := time.Since(start)

	report.mu.Lock()
	report.decode.add(decodeDur)
	report.composite.add(compositeDur)
	report.bitmaps++
	report.mu.Unlock()
	return nil
}

// analyzeAndRecord runs AnalyzeFramebuffer on fb and records the outcome.
// Returns true when PII was detected.
func analyzeAndRecord(
	ctx context.Context,
	cfg *benchConfig,
	report *benchReport,
	fb []byte,
	frameIndex int,
	sessionTime float64,
	snapshotStart time.Time,
	worstLagBase time.Time,
) (bool, error) {
	res, err := analyzer.AnalyzeFramebuffer(
		ctx, fb, cfg.fixture.CanvasWidth, cfg.fixture.CanvasHeight,
		cfg.fixture.SessionID, frameIndex, sessionTime, cfg.presidio, cfg.params,
	)
	analyzeDur := time.Since(snapshotStart)
	if err != nil {
		report.mu.Lock()
		report.analyze.add(analyzeDur)
		report.mu.Unlock()
		return false, err
	}

	report.mu.Lock()
	report.analyze.add(analyzeDur)
	report.snapshots++
	snapshotN := report.snapshots
	report.ocrStage.add(res.OCRDuration)
	report.presidio.add(res.PresidioDuration)
	if res.OCRTextLen == 0 {
		report.emptySnapshots++
	}
	detected := len(res.Counts) > 0
	if detected {
		now := time.Now()
		ev := detectionEvent{
			sessionTime: sessionTime,
			entities:    res.Counts,
			analysisLag: now.Sub(snapshotStart),
		}
		if !worstLagBase.IsZero() {
			ev.worstLag = now.Sub(worstLagBase)
		}
		report.detectionEvents = append(report.detectionEvents, ev)
	}
	report.mu.Unlock()

	if cfg.dumpDir != "" {
		dumpSnapshot(cfg, fb, snapshotN)
	}
	if cfg.verbose {
		fmt.Printf("snapshot %3d t=%6.2fs ocr=%s presidio=%s words=%d entities=%d\n",
			snapshotN, sessionTime,
			res.OCRDuration.Round(time.Millisecond), res.PresidioDuration.Round(time.Millisecond),
			res.OCRWords, len(res.Detections))
	}

	return detected, nil
}

// runFastPace replays all events as fast as possible. This measures pure
// pipeline throughput and per-stage latencies — ideal for comparing OCR
// engines and analysis parameters.
func runFastPace(cfg *benchConfig) error {
	ctx := context.Background()
	w, h := cfg.fixture.CanvasWidth, cfg.fixture.CanvasHeight
	fb := make([]byte, w*h*4)
	report := &benchReport{}

	baseTS := cfg.frames[0].Timestamp
	lastSnapshotTime := -cfg.params.SnapshotInterval
	var prevFBHash [32]byte
	fbDirty := false
	wallStart := time.Now()

	for _, ev := range cfg.frames {
		if err := decodeAndComposite(fb, w, h, ev, report); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", err)
			continue
		}
		fbDirty = true

		sessionTime := ev.Timestamp - baseTS
		if (ev.Timestamp - lastSnapshotTime) < cfg.params.SnapshotInterval {
			continue
		}
		lastSnapshotTime = ev.Timestamp
		fbDirty = false

		fbHash := sha256.Sum256(analyzer.SampleFramebuffer(fb, w, h))
		if fbHash == prevFBHash {
			report.addDedupSkip()
			continue
		}
		prevFBHash = fbHash

		detected, err := analyzeAndRecord(ctx, cfg, report, fb, ev.Index, sessionTime, time.Now(), time.Time{})
		if err != nil {
			return err
		}
		if detected && cfg.killOnDetected {
			fmt.Printf("\n*** kill switch fired at session t=%.2fs ***\n", sessionTime)
			report.print(time.Since(wallStart), sessionTime)
			return nil
		}
	}

	if fbDirty {
		lastTS := cfg.frames[len(cfg.frames)-1].Timestamp - baseTS
		if _, err := analyzeAndRecord(ctx, cfg, report, fb,
			cfg.frames[len(cfg.frames)-1].Index, lastTS, time.Now(), time.Time{}); err != nil {
			return err
		}
	}

	sessionDuration := cfg.frames[len(cfg.frames)-1].Timestamp - baseTS
	report.print(time.Since(wallStart), sessionDuration)
	return nil
}

// runRealtimePace replays events paced by their original timestamps while an
// independent analyzer goroutine snapshots the shared framebuffer on a wall
// clock interval — the same architecture as the planned live analyzer. The
// reported lags answer: "how long after PII hits the screen would the session
// die?"
func runRealtimePace(cfg *benchConfig) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w, h := cfg.fixture.CanvasWidth, cfg.fixture.CanvasHeight
	fb := make([]byte, w*h*4)
	var fbMu sync.Mutex
	report := &benchReport{}

	baseTS := cfg.frames[0].Timestamp
	wallStart := time.Now()
	replayDone := make(chan struct{})

	// Replayer: paces bitmap events by their original timestamps.
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
			fbMu.Lock()
			err := decodeAndComposite(fb, w, h, ev, report)
			fbMu.Unlock()
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: %v\n", err)
			}
		}
	}()

	// Analyzer: snapshots the framebuffer on a wall-clock interval, exactly
	// like the future realtime analyzer attached to a live session.
	snapshotCopy := make([]byte, len(fb))
	var prevFBHash [32]byte
	prevSnapshotWall := wallStart
	behindIntervals := 0
	interval := time.Duration(cfg.params.SnapshotInterval / cfg.speed * float64(time.Second))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	running := true
	for running {
		select {
		case <-ticker.C:
		case <-replayDone:
			running = false // one final snapshot below
		}

		snapshotStart := time.Now()
		fbMu.Lock()
		fbHash := sha256.Sum256(analyzer.SampleFramebuffer(fb, w, h))
		if fbHash == prevFBHash {
			fbMu.Unlock()
			report.addDedupSkip()
			continue
		}
		prevFBHash = fbHash
		copy(snapshotCopy, fb)
		fbMu.Unlock()

		sessionTime := snapshotStart.Sub(wallStart).Seconds() * cfg.speed
		detected, err := analyzeAndRecord(ctx, cfg, report, snapshotCopy, 0, sessionTime, snapshotStart, prevSnapshotWall)
		if err != nil {
			return err
		}
		if time.Since(snapshotStart) > interval {
			behindIntervals++
		}
		prevSnapshotWall = snapshotStart

		if detected && cfg.killOnDetected {
			cancel()
			<-replayDone // wait for the replayer to stop before reading the report
			fmt.Printf("\n*** kill switch fired at session t=%.2fs, %s after snapshot ***\n",
				sessionTime, time.Since(snapshotStart).Round(time.Millisecond))
			break
		}
	}

	sessionDuration := cfg.frames[len(cfg.frames)-1].Timestamp - baseTS
	report.print(time.Since(wallStart), sessionDuration)
	if behindIntervals > 0 {
		fmt.Printf("\nwarning: analysis was slower than the %.2fs interval on %d snapshot(s) — the analyzer cannot keep up at this rate\n",
			cfg.params.SnapshotInterval, behindIntervals)
	}
	return nil
}

// dumpSnapshot writes the framebuffer as a PNG for visual debugging.
func dumpSnapshot(cfg *benchConfig, fb []byte, n int) {
	pix := make([]byte, len(fb))
	copy(pix, fb)
	img := &image.NRGBA{
		Pix:    pix,
		Stride: cfg.fixture.CanvasWidth * 4,
		Rect:   image.Rect(0, 0, cfg.fixture.CanvasWidth, cfg.fixture.CanvasHeight),
	}
	path := filepath.Join(cfg.dumpDir, fmt.Sprintf("snapshot-%04d.png", n))
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to create %s: %v\n", path, err)
		return
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to encode %s: %v\n", path, err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

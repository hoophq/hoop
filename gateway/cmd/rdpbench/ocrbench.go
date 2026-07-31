package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoophq/hoop/gateway/rdp/analyzer"
	"github.com/hoophq/hoop/gateway/rdp/ocr"
)

// normalizeToken lowercases and strips characters that OCR renders
// inconsistently (whitespace, punctuation used as separators) so token
// comparison focuses on the alphanumeric content a PII scan keys on.
func normalizeToken(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// droppedTokens returns the normalized tokens present in ref (e.g. fp32) but
// missing from got (e.g. fp16), as a multiset difference. Empty (non-empty)
// normalized tokens are ignored — pure punctuation/whitespace differences are
// not PII-relevant. This answers "did the candidate engine lose any content
// the reference caught?", the only correctness question that matters for a PII
// guard evaluating an engine swap.
func droppedTokens(ref, got []string) []string {
	counts := map[string]int{}
	for _, w := range got {
		if n := normalizeToken(w); n != "" {
			counts[n]++
		}
	}
	var dropped []string
	for _, w := range ref {
		n := normalizeToken(w)
		if n == "" {
			continue
		}
		if counts[n] > 0 {
			counts[n]--
		} else {
			dropped = append(dropped, w)
		}
	}
	return dropped
}

// runOCRBench isolates the OCR engine cost: it replays a fixture, carves out
// the padded band of every bitmap event (the exact per-state unit the PII
// gate's capture mode would have to analyze), and measures per-state OCR
// latency on the chosen engine. This answers "what does an engine swap buy"
// without rewiring the analysis pipeline — OCR is ~96% of it anyway.
//
//	rdpbench ocrbench -i recording.json -engine tesseract
//	rdpbench ocrbench -i recording.json -engine http -url http://cudabox:8868
func runOCRBench(args []string) error {
	fs := flag.NewFlagSet("ocrbench", flag.ExitOnError)
	input := fs.String("i", "recording.json", "input fixture file (from 'rdpbench fetch')")
	engine := fs.String("engine", "tesseract", "OCR engine: 'tesseract' (the gateway ocr package — honors RDP_OCR_SERVER_URL, falling back to the local tesseract subprocess) or 'http' (direct PoC server access, bypassing the gateway package)")
	url := fs.String("url", "http://127.0.0.1:8868", "http engine: OCR server base URL")
	compareURL := fs.String("compare-url", "", "http engine: optional second OCR server to cross-check text against -url (e.g. fp16 vs fp32). Reports per-state text mismatches — a PII guard must not lose characters to an engine swap.")
	bandPad := fs.Int("band-pad", analyzer.DefaultBandPadding, "vertical padding in pixels around dirty rects")
	samples := fs.Int("n", 300, "number of band states to sample (evenly spaced; 0 = all)")
	concurrency := fs.Int("concurrency", 1, "http engine: number of bands to OCR in parallel (the gateway analyzer issues up to 8 concurrent chunk requests). Reports aggregate throughput at this level; per-state latency then reflects queued/contended service time.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *concurrency < 1 {
		return fmt.Errorf("-concurrency must be >= 1")
	}
	if *concurrency > 1 && *engine != "http" {
		return fmt.Errorf("-concurrency is only supported with -engine http")
	}
	if *concurrency > 1 && *compareURL != "" {
		return fmt.Errorf("-concurrency cannot be combined with -compare-url (ordering of the cross-check would be nondeterministic)")
	}

	fixture, err := loadFixture(*input)
	if err != nil {
		return err
	}
	frames, err := parseEvents(fixture.Events)
	if err != nil {
		return err
	}

	var serverLat durationStats // http engine: server-reported compute time
	var latMu sync.Mutex        // guards lat + serverLat under -concurrency > 1
	var ocrState func(rgba []byte, w, h int) (int, error)
	switch *engine {
	case "tesseract":
		if !ocr.IsAvailable() {
			return fmt.Errorf("tesseract not found in PATH")
		}
		ocrState = func(rgba []byte, w, h int) (int, error) {
			res, err := ocr.ExtractWords(context.Background(), rgba, w, h)
			if err != nil {
				return 0, err
			}
			return len(res.Words), nil
		}
	case "http":
		client := &http.Client{Timeout: 30 * time.Second}
		probe, err := client.Get(*url + "/healthz")
		if err != nil {
			return fmt.Errorf("OCR server not reachable at %s: %w", *url, err)
		}
		health, _ := io.ReadAll(probe.Body)
		probe.Body.Close()
		fmt.Printf("ocr server: %s\n", bytes.TrimSpace(health))

		// ocrWords posts a band to a server and returns its recognized words
		// (in server order) plus the server-reported compute time.
		ocrWords := func(base string, rgba []byte, w, h int) ([]string, time.Duration, error) {
			img := &image.NRGBA{Pix: rgba, Stride: w * 4, Rect: image.Rect(0, 0, w, h)}
			var buf bytes.Buffer
			if err := png.Encode(&buf, img); err != nil {
				return nil, 0, err
			}
			resp, err := client.Post(base+"/ocr", "application/octet-stream", &buf)
			if err != nil {
				return nil, 0, err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
				return nil, 0, fmt.Errorf("ocr server status %d: %s", resp.StatusCode, body)
			}
			var out struct {
				DurationMS float64 `json:"duration_ms"`
				Words      []struct {
					Text string  `json:"text"`
					Conf float64 `json:"conf"`
				} `json:"words"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				return nil, 0, err
			}
			words := make([]string, len(out.Words))
			for i, wd := range out.Words {
				words[i] = wd.Text
			}
			return words, time.Duration(out.DurationMS * float64(time.Millisecond)), nil
		}

		var cmpMismatch, cmpChecked, cmpPIIDrop int
		if *compareURL != "" {
			cprobe, err := client.Get(*compareURL + "/healthz")
			if err != nil {
				return fmt.Errorf("compare OCR server not reachable at %s: %w", *compareURL, err)
			}
			chealth, _ := io.ReadAll(cprobe.Body)
			cprobe.Body.Close()
			fmt.Printf("compare server: %s\n", bytes.TrimSpace(chealth))
		}

		ocrState = func(rgba []byte, w, h int) (int, error) {
			words, dur, err := ocrWords(*url, rgba, w, h)
			if err != nil {
				return 0, err
			}
			latMu.Lock()
			serverLat.add(dur)
			latMu.Unlock()
			if *compareURL != "" {
				cwords, _, cerr := ocrWords(*compareURL, rgba, w, h)
				if cerr != nil {
					return 0, fmt.Errorf("compare server: %w", cerr)
				}
				cmpChecked++
				// The PII-relevant question is not "identical" but "does -url
				// (e.g. fp16) DROP a token that -compare-url (fp32) caught?".
				// Word order and whitespace are harmless to a token-based PII
				// scan; a missing token is a potential leak. Compare as
				// normalized token multisets and report only tokens present in
				// compare-url but absent from url.
				dropped := droppedTokens(cwords, words)
				// A token is PII-shaped if it carries >= 4 digits (phones,
				// long numbers) or an '@' (emails) — the content a leak would
				// expose. UI-chrome/segmentation noise is reported separately
				// so it does not drown out the signal that matters.
				var piiDropped []string
				for _, d := range dropped {
					digits := 0
					hasAt := strings.ContainsRune(d, '@')
					for _, r := range d {
						if r >= '0' && r <= '9' {
							digits++
						}
					}
					if digits >= 4 || hasAt {
						piiDropped = append(piiDropped, d)
					}
				}
				if len(dropped) > 0 {
					cmpMismatch++
				}
				if len(piiDropped) > 0 {
					cmpPIIDrop++
					fmt.Printf("  *** PII-SHAPED DROP by -url: %q\n", piiDropped)
				} else if len(dropped) > 0 && cmpMismatch <= 15 {
					fmt.Printf("  (chrome/segmentation drop, no PII): %q\n", dropped)
				}
			}
			return len(words), nil
		}
		defer func() {
			if *compareURL != "" {
				fmt.Printf("\ntext cross-check: %d/%d states dropped some token; %d states dropped a PII-shaped token (>=4 digits or '@')\n",
					cmpMismatch, cmpChecked, cmpPIIDrop)
			}
		}()
	default:
		return fmt.Errorf("invalid -engine %q: must be 'tesseract' or 'http'", *engine)
	}

	w, h := fixture.CanvasWidth, fixture.CanvasHeight
	fb := make([]byte, w*h*4)
	report := &benchReport{}

	stride := 1
	if *samples > 0 && len(frames) > *samples {
		stride = len(frames) / *samples
	}

	// Extract the sampled bands up front. Compositing mutates the shared
	// framebuffer in event order, so it must stay serial; each band is copied
	// out so the concurrent OCR phase reads immutable, independent buffers.
	type bandJob struct {
		rgba []byte
		h    int
	}
	var jobs []bandJob
	for idx, ev := range frames {
		if err := decodeAndComposite(fb, w, h, ev, report); err != nil {
			continue
		}
		if idx%stride != 0 {
			continue
		}
		y0 := int(ev.Bitmap.Y) - *bandPad
		y1 := int(ev.Bitmap.Y) + int(ev.Bitmap.Height) + *bandPad
		if y0 < 0 {
			y0 = 0
		}
		if y1 > h {
			y1 = h
		}
		if y1 <= y0 {
			continue
		}
		src := fb[y0*w*4 : y1*w*4]
		buf := make([]byte, len(src))
		copy(buf, src)
		jobs = append(jobs, bandJob{rgba: buf, h: y1 - y0})
	}

	var lat durationStats
	var states, words, errs int64
	wallStart := time.Now()

	if *concurrency == 1 {
		for _, j := range jobs {
			start := time.Now()
			n, err := ocrState(j.rgba, w, j.h)
			if err != nil {
				errs++
				if errs <= 3 {
					fmt.Printf("warning: ocr error: %v\n", err)
				}
				continue
			}
			lat.add(time.Since(start))
			states++
			words += int64(n)
		}
	} else {
		jobCh := make(chan bandJob, *concurrency)
		var wg sync.WaitGroup
		for i := 0; i < *concurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := range jobCh {
					start := time.Now()
					n, err := ocrState(j.rgba, w, j.h)
					if err != nil {
						if atomic.AddInt64(&errs, 1) <= 3 {
							fmt.Printf("warning: ocr error: %v\n", err)
						}
						continue
					}
					elapsed := time.Since(start)
					latMu.Lock()
					lat.add(elapsed)
					latMu.Unlock()
					atomic.AddInt64(&states, 1)
					atomic.AddInt64(&words, int64(n))
				}
			}()
		}
		for _, j := range jobs {
			jobCh <- j
		}
		close(jobCh)
		wg.Wait()
	}

	sessionDuration := frames[len(frames)-1].Timestamp - frames[0].Timestamp
	statesPerSecNeeded := float64(len(frames)) / sessionDuration

	fmt.Printf("\n=== ocr engine benchmark: %s (concurrency %d) ===\n", *engine, *concurrency)
	fmt.Printf("band states OCR'd:    %d of %d events (stride %d, errors %d)\n", states, len(frames), stride, errs)
	fmt.Printf("words recognized:     %d (%.1f/state)\n", words, float64(words)/float64(max(states, 1)))
	fmt.Printf("per-state latency:    %s\n", lat.summary())
	if serverLat.count() > 0 {
		fmt.Printf("server compute only:  %s (rest is PNG encode + network)\n", serverLat.summary())
	}
	if states > 0 {
		throughput := float64(states) / time.Since(wallStart).Seconds()
		label := "serial throughput"
		if *concurrency > 1 {
			label = fmt.Sprintf("throughput @ c=%d", *concurrency)
		}
		fmt.Printf("%s:  %.1f states/s (capture mode on this recording needs %.1f states/s sustained)\n",
			label, throughput, statesPerSecNeeded)
	}
	return nil
}

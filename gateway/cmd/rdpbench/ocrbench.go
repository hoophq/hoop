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
	"time"

	"github.com/hoophq/hoop/gateway/rdp/analyzer"
	"github.com/hoophq/hoop/gateway/rdp/ocr"
)

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
	bandPad := fs.Int("band-pad", analyzer.DefaultBandPadding, "vertical padding in pixels around dirty rects")
	samples := fs.Int("n", 300, "number of band states to sample (evenly spaced; 0 = all)")
	if err := fs.Parse(args); err != nil {
		return err
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
		ocrState = func(rgba []byte, w, h int) (int, error) {
			img := &image.NRGBA{Pix: rgba, Stride: w * 4, Rect: image.Rect(0, 0, w, h)}
			var buf bytes.Buffer
			if err := png.Encode(&buf, img); err != nil {
				return 0, err
			}
			resp, err := client.Post(*url+"/ocr", "application/octet-stream", &buf)
			if err != nil {
				return 0, err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
				return 0, fmt.Errorf("ocr server status %d: %s", resp.StatusCode, body)
			}
			var out struct {
				DurationMS float64 `json:"duration_ms"`
				Words      []struct {
					Text string  `json:"text"`
					Conf float64 `json:"conf"`
				} `json:"words"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				return 0, err
			}
			serverLat.add(time.Duration(out.DurationMS * float64(time.Millisecond)))
			return len(out.Words), nil
		}
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

	var lat durationStats
	states, words, errs := 0, 0, 0
	wallStart := time.Now()
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
		band := fb[y0*w*4 : y1*w*4]

		start := time.Now()
		n, err := ocrState(band, w, y1-y0)
		if err != nil {
			errs++
			if errs <= 3 {
				fmt.Printf("warning: ocr error on state %d: %v\n", idx, err)
			}
			continue
		}
		lat.add(time.Since(start))
		states++
		words += n
	}

	sessionDuration := frames[len(frames)-1].Timestamp - frames[0].Timestamp
	statesPerSecNeeded := float64(len(frames)) / sessionDuration

	fmt.Printf("\n=== ocr engine benchmark: %s ===\n", *engine)
	fmt.Printf("band states OCR'd:    %d of %d events (stride %d, errors %d)\n", states, len(frames), stride, errs)
	fmt.Printf("words recognized:     %d (%.1f/state)\n", words, float64(words)/float64(max(states, 1)))
	fmt.Printf("per-state latency:    %s\n", lat.summary())
	if serverLat.count() > 0 {
		fmt.Printf("server compute only:  %s (rest is PNG encode + network)\n", serverLat.summary())
	}
	if states > 0 {
		throughput := float64(states) / time.Since(wallStart).Seconds()
		fmt.Printf("serial throughput:    %.1f states/s (capture mode on this recording needs %.1f states/s sustained)\n",
			throughput, statesPerSecNeeded)
	}
	return nil
}

package ocr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/log"
)

// engine is one OCR backend. Implementations receive validated top-down RGBA
// pixels and return word/line tokens whose space-joined text equals
// ExtractResult.Text — the contract the analyzer's character-offset to
// bounding-box mapping depends on.
type engine interface {
	extract(ctx context.Context, rgba []byte, width, height int) (*ExtractResult, error)
	available() bool
	name() string
}

// ocrServerURLEnv selects the HTTP OCR engine (a RapidOCR/PaddleOCR sidecar,
// see scripts/dev/ocr-poc) instead of the local tesseract subprocess. The
// sidecar decides CPU vs GPU on its side (RAPIDOCR_USE_CUDA); the gateway
// contract is identical for both. Measured on real RDP band states:
// tesseract ~139ms p50/state, RapidOCR CPU ~10ms, RapidOCR/Paddle CUDA ~6ms.
const ocrServerURLEnv = "RDP_OCR_SERVER_URL"

var (
	engineOnce   sync.Once
	activeEngine engine
)

// getEngine resolves the OCR backend ONCE per process lifetime: like the
// rest of the gateway's env-based configuration, RDP_OCR_SERVER_URL is read
// at first use and latched — changing the environment afterwards has no
// effect until restart. Note that a configured HTTP engine is treated as
// available without a health probe; an unreachable sidecar surfaces as
// per-request errors (loudly logged, fail-open in the PII gate), not as
// unavailability at startup.
func getEngine() engine {
	engineOnce.Do(func() {
		if url := strings.TrimSpace(os.Getenv(ocrServerURLEnv)); url != "" {
			activeEngine = newHTTPEngine(url)
			log.Infof("rdp-ocr: using HTTP OCR server at %s", url)
			return
		}
		activeEngine = tesseractEngine{}
	})
	return activeEngine
}

// tesseractEngine runs the local tesseract subprocess (see ocr.go for the
// BMP/tempfile/upscale rationale).
type tesseractEngine struct{}

func (tesseractEngine) name() string { return "tesseract" }

func (tesseractEngine) available() bool {
	_, err := exec.LookPath("tesseract")
	return err == nil
}

func (tesseractEngine) extract(ctx context.Context, rgba []byte, width, height int) (*ExtractResult, error) {
	// Upscale 2x if image is small (improves accuracy on small fonts).
	upscaled := false
	if width < minWidthForUpscale {
		rgba = nearestNeighbor2xRGBA(rgba, width, height)
		width *= 2
		height *= 2
		upscaled = true
	}

	bmp := encodeBMP(rgba, width, height)
	tsvOutput, err := runTesseractTSV(ctx, bmp)
	if err != nil {
		return nil, err
	}
	words := parseTSV(tsvOutput, upscaled)
	return &ExtractResult{
		Text:     joinWords(words),
		Words:    words,
		Upscaled: upscaled,
	}, nil
}

// httpEngine posts frames to an OCR server (RapidOCR/PaddleOCR sidecar).
//
// Transport notes:
//   - Frames are sent as uncompressed 24bpp BMP: a plain pixel copy, no
//     deflate pass (PNG encode measured ~8-14ms per band, dwarfing the
//     ~6ms GPU inference it wraps).
//   - No 2x upscale: PP-OCR detection/recognition reads RDP screen text at
//     native resolution (measured equal PII recognition), halving payload
//     and skipping the upscale copy.
//   - The pooled transport sizes match the analyzer's parallel OCR chunking
//     so concurrent chunk requests reuse connections.
type httpEngine struct {
	baseURL string
	client  *http.Client
}

func newHTTPEngine(baseURL string) *httpEngine {
	return &httpEngine{
		baseURL: strings.TrimRight(baseURL, "/"),
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        32,
				MaxIdleConnsPerHost: 16,
				MaxConnsPerHost:     32,
				IdleConnTimeout:     90 * time.Second,
				// Bound server think time separately from the total client
				// timeout: a wedged sidecar should fail the request fast.
				ResponseHeaderTimeout: 20 * time.Second,
			},
		},
	}
}

func (e *httpEngine) name() string    { return "http:" + e.baseURL }
func (e *httpEngine) available() bool { return true }

// ocrServerWord is one token in the OCR server's response. Confidence is
// 0..1 (PP-OCR convention); coordinates are pixels in the sent image space.
type ocrServerWord struct {
	Text string  `json:"text"`
	Conf float64 `json:"conf"`
	X    int     `json:"x"`
	Y    int     `json:"y"`
	W    int     `json:"w"`
	H    int     `json:"h"`
}

type ocrServerResponse struct {
	DurationMS float64         `json:"duration_ms"`
	Words      []ocrServerWord `json:"words"`
}

// maxOCRResponseBytes bounds the response body read: even a pathological
// full-screen of dense text is far below this.
const maxOCRResponseBytes = 8 << 20

func (e *httpEngine) extract(ctx context.Context, rgba []byte, width, height int) (*ExtractResult, error) {
	bmp := encodeBMP(rgba, width, height)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/ocr", bytes.NewReader(bmp))
	if err != nil {
		return nil, fmt.Errorf("ocr: failed to build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ocr: server request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("ocr: server returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out ocrServerResponse
	dec := json.NewDecoder(io.LimitReader(resp.Body, maxOCRResponseBytes))
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("ocr: invalid server response: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("ocr: invalid server response: trailing data after JSON object")
	}

	// Validate every token before it enters the analyzer contract: the HTTP
	// path must not trust the sidecar more than we trust tesseract TSV.
	// Malformed entries are dropped (geometry) or clamped (confidence) —
	// never propagated.
	words := make([]Word, 0, len(out.Words))
	for _, w := range out.Words {
		text := strings.TrimSpace(w.Text)
		if text == "" {
			continue
		}
		if math.IsNaN(w.Conf) || math.IsInf(w.Conf, 0) || w.Conf < 0 {
			continue
		}
		if w.Conf > 1 {
			w.Conf = 1
		}
		if w.X < 0 || w.Y < 0 || w.W <= 0 || w.H <= 0 {
			continue
		}
		words = append(words, Word{
			Text:   text,
			Left:   w.X,
			Top:    w.Y,
			Width:  w.W,
			Height: w.H,
			// Server confidence is 0..1; Word.Conf is 0-100 (tesseract
			// convention, see the Word doc).
			Conf: w.Conf * 100,
		})
	}
	return &ExtractResult{
		Text:  joinWords(words),
		Words: words,
	}, nil
}

// joinWords reconstructs the full text exactly as the analyzer expects:
// tokens joined by single spaces, so Presidio character offsets line up
// with word ranges.
func joinWords(words []Word) string {
	if len(words) == 0 {
		return ""
	}
	parts := make([]string, len(words))
	for i, w := range words {
		parts[i] = w.Text
	}
	return strings.Join(parts, " ")
}

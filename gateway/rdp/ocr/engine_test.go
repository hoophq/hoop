package ocr

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// rgbaTestImage builds a tiny top-down RGBA image.
func rgbaTestImage(w, h int) []byte {
	img := make([]byte, w*h*4)
	for i := 0; i < len(img); i += 4 {
		img[i], img[i+1], img[i+2], img[i+3] = 0xaa, 0xbb, 0xcc, 0xff
	}
	return img
}

func TestHTTPEngine_MapsServerResponse(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ocr" {
			t.Errorf("path: got %s, want /ocr", r.URL.Path)
		}
		gotBody, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(rw).Encode(ocrServerResponse{
			DurationMS: 5.5,
			Words: []ocrServerWord{
				{Text: "lucas@teske.com.br", Conf: 0.93, X: 10, Y: 20, W: 200, H: 16},
				{Text: "  ", Conf: 0.5, X: 0, Y: 0, W: 1, H: 1}, // blank: dropped
				{Text: "+5511987876654", Conf: 0.82, X: 10, Y: 40, W: 150, H: 16},
			},
		})
	}))
	defer srv.Close()

	const w, h = 32, 8
	eng := newHTTPEngine(srv.URL + "/") // trailing slash must be tolerated
	res, err := eng.extract(context.Background(), rgbaTestImage(w, h), w, h)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	if want := "lucas@teske.com.br +5511987876654"; res.Text != want {
		t.Errorf("joined text: got %q, want %q", res.Text, want)
	}
	if len(res.Words) != 2 {
		t.Fatalf("words: got %d, want 2 (blank dropped)", len(res.Words))
	}
	first := res.Words[0]
	if first.Left != 10 || first.Top != 20 || first.Width != 200 || first.Height != 16 {
		t.Errorf("bbox: got %+v", first)
	}
	if first.Conf != 93 {
		t.Errorf("conf must be rescaled to 0-100: got %v", first.Conf)
	}
	if res.Upscaled {
		t.Errorf("http engine must not report upscaling")
	}

	// The request body must be a valid 24bpp BMP of the original dimensions
	// (no upscale on the HTTP path).
	if len(gotBody) < bmpHeaderSize || gotBody[0] != 'B' || gotBody[1] != 'M' {
		t.Fatalf("body is not a BMP (len=%d)", len(gotBody))
	}
	if bw := int(binary.LittleEndian.Uint32(gotBody[18:])); bw != w {
		t.Errorf("BMP width: got %d, want %d", bw, w)
	}
	if bh := int(binary.LittleEndian.Uint32(gotBody[22:])); bh != h {
		t.Errorf("BMP height: got %d, want %d", bh, h)
	}
}

func TestHTTPEngine_ServerErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		http.Error(rw, "model exploded", http.StatusInternalServerError)
	}))
	defer srv.Close()

	eng := newHTTPEngine(srv.URL)
	_, err := eng.extract(context.Background(), rgbaTestImage(4, 4), 4, 4)
	if err == nil {
		t.Fatal("expected error on 500")
	}
	if want := "status 500"; !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "model exploded") {
		t.Errorf("error must carry status and body snippet: %v", err)
	}
}

func TestHTTPEngine_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(rw, "not json")
	}))
	defer srv.Close()

	eng := newHTTPEngine(srv.URL)
	if _, err := eng.extract(context.Background(), rgbaTestImage(4, 4), 4, 4); err == nil {
		t.Fatal("expected error on invalid JSON")
	}
}

func TestHTTPEngine_ContextCancellation(t *testing.T) {
	entered := make(chan struct{})
	handlerDone := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		// Drain the body so the server's background connection read is
		// active — that is what propagates a client disconnect into the
		// request context.
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done() // hold until the client cancels
		close(handlerDone)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	eng := newHTTPEngine(srv.URL)
	go func() {
		_, err := eng.extract(ctx, rgbaTestImage(4, 4), 4, 4)
		errCh <- err
	}()

	// Cancel while the request is provably in flight; the client must
	// return promptly and the server must observe the cancellation.
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("request never reached the server")
	}
	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error on cancelled in-flight request")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("extract did not return after cancellation")
	}
	select {
	case <-handlerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("server never observed the cancellation")
	}
}

func TestHTTPEngine_RejectsMalformedServerTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		// Raw JSON so non-finite confidences can be expressed... they cannot
		// in valid JSON — which is itself part of the contract: a server
		// emitting NaN produces a decode error, tested separately. Here:
		// finite-but-invalid values.
		fmt.Fprint(rw, `{
			"duration_ms": 1,
			"words": [
				{"text": "ok-token",  "conf": 0.9,  "x": 1,  "y": 2, "w": 10, "h": 5},
				{"text": "neg-conf",  "conf": -0.1, "x": 1,  "y": 2, "w": 10, "h": 5},
				{"text": "over-conf", "conf": 1.7,  "x": 1,  "y": 2, "w": 10, "h": 5},
				{"text": "neg-x",     "conf": 0.5,  "x": -1, "y": 2, "w": 10, "h": 5},
				{"text": "zero-w",    "conf": 0.5,  "x": 1,  "y": 2, "w": 0,  "h": 5},
				{"text": "neg-h",     "conf": 0.5,  "x": 1,  "y": 2, "w": 10, "h": -5},
				{"text": "  pad  ",   "conf": 0.5,  "x": 1,  "y": 2, "w": 10, "h": 5}
			]
		}`)
	}))
	defer srv.Close()

	eng := newHTTPEngine(srv.URL)
	res, err := eng.extract(context.Background(), rgbaTestImage(4, 4), 4, 4)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if want := "ok-token over-conf pad"; res.Text != want {
		t.Errorf("text: got %q, want %q (invalid tokens dropped, conf clamped, padding trimmed)", res.Text, want)
	}
	if res.Words[1].Conf != 100 {
		t.Errorf("over-conf must clamp to 100, got %v", res.Words[1].Conf)
	}
}

func TestHTTPEngine_RejectsTrailingData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(rw, `{"duration_ms": 1, "words": []} {"sneaky": true}`)
	}))
	defer srv.Close()

	eng := newHTTPEngine(srv.URL)
	if _, err := eng.extract(context.Background(), rgbaTestImage(4, 4), 4, 4); err == nil {
		t.Fatal("expected error on trailing data after the JSON object")
	}
}

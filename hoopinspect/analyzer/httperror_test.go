package analyzer_test

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/hoophq/hoopinspect/analyzer"
)

// atLevel installs a default logger at lv for one test and restores the
// previous one. The gate reads slog.Default(), so this exercises the real
// mechanism rather than a test seam. Not parallel-safe, deliberately:
// slog.SetDefault is process-wide.
func atLevel(t *testing.T, lv slog.Level) {
	t.Helper()
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: lv})))
	t.Cleanup(func() { slog.SetDefault(prev) })
}

func failedResponse(status int, statusText, body string) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Status:        statusText,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

// The case this exists for. A bare "provider returned 404 Not Found" cannot
// tell a wrong model id from a model that is not enabled from a bad region,
// and the one sentence that can is in the body.
func TestProviderErrorQuotesTheBodyAtDebug(t *testing.T) {
	atLevel(t, slog.LevelDebug)
	body := `{"error":{"code":404,"message":"Publisher Model ` +
		`projects/x/locations/global/publishers/anthropic/models/claude-sonnet-4-5@20250929 was not found",` +
		`"status":"NOT_FOUND"}}`

	err := analyzer.NewProviderHTTPError("analyzer/vertex", failedResponse(404, "404 Not Found", body))

	if err.StatusCode != 404 {
		t.Errorf("StatusCode = %d, want 404", err.StatusCode)
	}
	if err.Truncated {
		t.Error("a short body was reported as truncated")
	}
	msg := err.Error()
	for _, want := range []string{"analyzer/vertex", "404 Not Found", "was not found", "NOT_FOUND"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message is missing %q:\n%s", want, msg)
		}
	}
}

// Truncation is stated, never left to be inferred: a reader who cannot tell a
// complete short body from a cut one chases the wrong half of a JSON
// document.
func TestProviderErrorMarksTruncation(t *testing.T) {
	atLevel(t, slog.LevelDebug)
	body := `{"error":"` + strings.Repeat("x", analyzer.MaxErrorBodyBytes*2) + `"}`
	err := analyzer.NewProviderHTTPError("analyzer/openai", failedResponse(400, "400 Bad Request", body))

	if !err.Truncated {
		t.Fatal("an oversized body was not reported as truncated")
	}
	if len(err.Body) > analyzer.MaxErrorBodyBytes {
		t.Errorf("body is %d bytes, over the %d cap", len(err.Body), analyzer.MaxErrorBodyBytes)
	}
	msg := err.Error()
	if !strings.Contains(msg, "[truncated to") {
		t.Errorf("truncation is not stated:\n%s", msg[:200])
	}
	// The provider declared a length, so the message says how much was cut
	// rather than only that something was.
	if !strings.Contains(msg, "of "+itoa(len(body))+" bytes]") {
		t.Errorf("the declared length is missing:\n%s", msg[len(msg)-80:])
	}
}

// A body exactly at the cap is complete, not truncated. The reader is one
// byte past the limit precisely so this case is decided rather than guessed.
func TestProviderErrorExactlyAtTheCapIsNotTruncated(t *testing.T) {
	body := strings.Repeat("y", analyzer.MaxErrorBodyBytes)
	err := analyzer.NewProviderHTTPError("analyzer/anthropic", failedResponse(500, "500 Internal Server Error", body))

	if err.Truncated {
		t.Error("a body exactly at the cap was reported as truncated")
	}
}

// A remote string goes into a log line, so a newline in it is how somebody
// else forges a log record and an escape sequence is how they repaint the
// reader's terminal.
func TestProviderErrorSanitizesTheBody(t *testing.T) {
	atLevel(t, slog.LevelDebug)
	body := "line one\nline two\r\n\ttabbed   spaced\x1b[31mred\x00nul"
	err := analyzer.NewProviderHTTPError("analyzer/vertex", failedResponse(403, "403 Forbidden", body))

	if strings.ContainsAny(err.Body, "\n\r\t\x00\x1b") {
		t.Errorf("control characters survived: %q", err.Body)
	}
	if want := "line one line two tabbed spaced [31mred nul"; err.Body != want {
		// The ESC is dropped and its parameter bytes are ordinary printables,
		// so "[31m" remains as literal text. That is the point: it can no
		// longer act on a terminal.
		t.Errorf("Body = %q, want %q", err.Body, want)
	}
}

// Truncating on a byte boundary can cut a multi-byte rune in half. The
// leftover must not reach a log as a replacement character or worse.
func TestProviderErrorDropsAPartialRune(t *testing.T) {
	// Fill to one byte short of the cap, then a 3-byte rune straddles it.
	body := strings.Repeat("a", analyzer.MaxErrorBodyBytes-1) + "€" + "tail"
	err := analyzer.NewProviderHTTPError("analyzer/vertex", failedResponse(400, "400 Bad Request", body))

	if !err.Truncated {
		t.Fatal("expected truncation")
	}
	if !isValidUTF8(err.Body) {
		t.Errorf("body is not valid UTF-8: %q", err.Body[len(err.Body)-8:])
	}
}

// An error with no body still has to say something useful, and must not read
// as though the provider explained itself.
func TestProviderErrorWithNoBody(t *testing.T) {
	err := analyzer.NewProviderHTTPError("analyzer/openai", failedResponse(502, "502 Bad Gateway", ""))

	msg := err.Error()
	if !strings.Contains(msg, "502 Bad Gateway") || !strings.Contains(msg, "empty response body") {
		t.Errorf("unhelpful message: %s", msg)
	}
	if strings.HasSuffix(msg, ": ") {
		t.Errorf("message ends in a dangling separator: %q", msg)
	}
}

// The body is consumed so the connection can go back to the pool rather than
// being closed and re-handshaked on the next classification.
func TestProviderErrorDrainsTheBody(t *testing.T) {
	r := &countingReader{Reader: strings.NewReader(strings.Repeat("z", 1024))}
	resp := &http.Response{StatusCode: 429, Status: "429 Too Many Requests",
		Body: io.NopCloser(r), ContentLength: 1024}

	analyzer.NewProviderHTTPError("analyzer/anthropic", resp)
	if r.n != 1024 {
		t.Errorf("read %d of 1024 bytes; the rest would keep the connection unusable", r.n)
	}
}

type countingReader struct {
	io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.Reader.Read(p)
	c.n += n
	return n, err
}

func itoa(n int) string {
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func isValidUTF8(s string) bool { return bytes.ToValidUTF8([]byte(s), nil) != nil && utf8Valid(s) }

func utf8Valid(s string) bool {
	for i, r := range s {
		if r == '\uFFFD' {
			// Either a real replacement char in the input (there is none
			// here) or an invalid byte, which is what this guards against.
			_ = i
			return false
		}
	}
	return true
}

// The default. An operator sees the status, learns a body exists, and is told
// the one config line that reveals it — without statement text reaching a log
// that audit.redact_statements does not cover.
func TestProviderErrorWithholdsTheBodyBelowDebug(t *testing.T) {
	atLevel(t, slog.LevelInfo)
	body := `{"error":{"code":404,"message":"Publisher model gemini-3.5-flash was not found"}}`

	err := analyzer.NewProviderHTTPError("analyzer/vertex", failedResponse(404, "404 Not Found", body))

	if err.Body == "" {
		t.Error("the body was not CAPTURED; the gate is on rendering, not on reading")
	}
	msg := err.Error()
	if strings.Contains(msg, "Publisher model") {
		t.Errorf("the body leaked below debug:\n%s", msg)
	}
	for _, want := range []string{"404 Not Found", "withheld", "log_level: debug"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message is missing %q:\n%s", want, msg)
		}
	}
}

// Same error, both levels: the only difference is what Error() renders.
func TestProviderErrorSameCaptureEitherWay(t *testing.T) {
	body := `{"error":{"message":"nope"}}`

	atLevel(t, slog.LevelInfo)
	quiet := analyzer.NewProviderHTTPError("analyzer/openai", failedResponse(403, "403 Forbidden", body))
	atLevel(t, slog.LevelDebug)
	loud := analyzer.NewProviderHTTPError("analyzer/openai", failedResponse(403, "403 Forbidden", body))

	if quiet.Body != loud.Body {
		t.Errorf("capture differs by log level: %q vs %q", quiet.Body, loud.Body)
	}
	if !strings.Contains(loud.Error(), "nope") {
		t.Errorf("debug did not render the body: %s", loud.Error())
	}
}

// Below 400 the client stopped following redirects and the body is a redirect
// page, not a diagnosis. Nothing to capture, and no offer to show it.
func TestProviderErrorSkipsTheBodyUnder400(t *testing.T) {
	atLevel(t, slog.LevelDebug)
	err := analyzer.NewProviderHTTPError("analyzer/openai",
		failedResponse(302, "302 Found", "<html>go here instead</html>"))

	if err.Body != "" {
		t.Errorf("captured a body for a %d: %q", err.StatusCode, err.Body)
	}
	msg := err.Error()
	if strings.Contains(msg, "go here") || strings.Contains(msg, "withheld") {
		t.Errorf("unexpected body handling for a 3xx: %s", msg)
	}
}

package sidecartui

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// The boot retry logs before Run converts the daemon's output. TextWriter
// gives those lines the text format, the same rendering Run gives the rest.
func TestTextWriterRendersTheDaemonJSONAsText(t *testing.T) {
	var out bytes.Buffer
	log := slog.New(slog.NewJSONHandler(TextWriter(&out), nil))
	log.Warn("the control plane is unavailable; retrying the boot handshake",
		"url", "http://127.0.0.1:8109", "attempt", 2, "retry_in", "1.2s")

	got := strings.TrimSpace(out.String())
	if strings.HasPrefix(got, "{") {
		t.Fatalf("the line is still JSON: %s", got)
	}
	for _, want := range []string{`level=WARN`, `msg="the control plane is unavailable; retrying the boot handshake"`,
		`url=http://127.0.0.1:8109`, `attempt=2`, `retry_in=1.2s`} {
		if !strings.Contains(got, want) {
			t.Errorf("text line %q lacks %q", got, want)
		}
	}
}

// A write that is not a log record passes through unchanged.
func TestTextWriterPassesOtherLinesThrough(t *testing.T) {
	var out bytes.Buffer
	if _, err := TextWriter(&out).Write([]byte("not json\n")); err != nil {
		t.Fatal(err)
	}
	if out.String() != "not json\n" {
		t.Errorf("out = %q", out.String())
	}
}

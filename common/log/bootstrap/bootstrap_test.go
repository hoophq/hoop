package bootstrap

import (
	"testing"
	"time"
)

func TestShouldUseTTYExplicitEncoding(t *testing.T) {
	cases := []struct {
		encoding string
		want     bool
	}{
		{"human", true},
		{"json", false},
		{"verbose", false},
		{"console", false},
	}
	for _, c := range cases {
		t.Setenv("LOG_ENCODING", c.encoding)
		if got := shouldUseTTY(); got != c.want {
			t.Errorf("LOG_ENCODING=%q: shouldUseTTY=%v, want %v", c.encoding, got, c.want)
		}
	}
}

func TestShouldUseTTYUnsetNonTerminal(t *testing.T) {
	// In `go test` stdout is piped to the test harness, not a terminal,
	// so auto-detect must return false when LOG_ENCODING is unset.
	t.Setenv("LOG_ENCODING", "")
	if shouldUseTTY() {
		t.Errorf("shouldUseTTY() = true in non-terminal test harness; expected false")
	}
}

func TestNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	if noColor() {
		t.Errorf("noColor()=true with NO_COLOR unset")
	}
	t.Setenv("NO_COLOR", "1")
	if !noColor() {
		t.Errorf("noColor()=false with NO_COLOR=1")
	}
}

func TestFormatElapsed(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0ms"},
		{500 * time.Microsecond, "0ms"},
		{50 * time.Millisecond, "50ms"},
		{999 * time.Millisecond, "999ms"},
		{1 * time.Second, "1.0s"},
		{5234 * time.Millisecond, "5.2s"},
	}
	for _, c := range cases {
		if got := formatElapsed(c.d); got != c.want {
			t.Errorf("formatElapsed(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestSortedKeysStable(t *testing.T) {
	m := map[string]string{"Z": "1", "A": "2", "M": "3"}
	got := sortedKeys(m)
	want := []string{"A", "M", "Z"}
	if len(got) != len(want) {
		t.Fatalf("sortedKeys len=%d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sortedKeys[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestStepHandleFinalizerOnceOnly(t *testing.T) {
	h := Step("unit-test")
	h.OK("")
	// second OK must be a no-op; nothing to assert directly but should not panic.
	h.OK("")
	h.Fail(nil)
	h.Skip("")
}

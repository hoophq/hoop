package tunnelmgr

import (
	"context"
	"io"
	"log"
	"strings"
	"testing"

	"github.com/hoophq/hoop/tunnel/daemonconfig"
)

// silentLogger is a *log.Logger that writes to io.Discard so the test
// suite output stays clean. Reused by every test in this file.
func silentLogger() *log.Logger {
	return log.New(io.Discard, "", 0)
}

func TestNew_ValidatesRequiredFields(t *testing.T) {
	cases := []struct {
		name string
		opts Options
	}{
		{"missing Logger", Options{SessionSeed: "s", TLD: "hoop", UserAgent: "ua"}},
		{"missing SessionSeed", Options{Logger: silentLogger(), TLD: "hoop", UserAgent: "ua"}},
		{"missing TLD", Options{Logger: silentLogger(), SessionSeed: "s", UserAgent: "ua"}},
		{"missing UserAgent", Options{Logger: silentLogger(), SessionSeed: "s", TLD: "hoop"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.opts); err == nil {
				t.Errorf("New(%+v) succeeded; want error", tc.opts)
			}
		})
	}
}

func TestNew_HappyPath(t *testing.T) {
	m, err := New(Options{
		Logger:      silentLogger(),
		SessionSeed: "test",
		TLD:         "hoop",
		UserAgent:   "test/1.0",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if m == nil {
		t.Fatal("New returned nil manager")
	}
}

func TestInitialState(t *testing.T) {
	m := newTestManager(t)
	if got := m.State(); got != StateIdle {
		t.Errorf("State() = %v, want %v", got, StateIdle)
	}
	snap := m.Snapshot()
	if snap.State != StateIdle {
		t.Errorf("Snapshot().State = %v, want %v", snap.State, StateIdle)
	}
	if snap.Allocator != nil {
		t.Error("Snapshot().Allocator must be nil when idle")
	}
	if !snap.Since.IsZero() {
		t.Errorf("Snapshot().Since = %v, want zero", snap.Since)
	}
}

func TestTearDown_WhenIdleIsNoop(t *testing.T) {
	m := newTestManager(t)
	if err := m.TearDown(); err != nil {
		t.Errorf("TearDown on idle manager returned %v, want nil", err)
	}
	if got := m.State(); got != StateIdle {
		t.Errorf("State after no-op TearDown = %v", got)
	}
}

func TestBringUp_RejectsMissingCredentials(t *testing.T) {
	cases := []struct {
		name string
		cfg  daemonconfig.Config
	}{
		{"empty config", daemonconfig.Config{}},
		{"missing token", daemonconfig.Config{APIURL: "https://x"}},
		{"missing api_url", daemonconfig.Config{Token: "t"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestManager(t)
			err := m.BringUp(context.Background(), tc.cfg)
			if err == nil {
				t.Fatal("BringUp succeeded with missing credentials")
			}
			// Manager must stay idle on validation failure — no
			// half-applied state.
			if got := m.State(); got != StateIdle {
				t.Errorf("State after rejected BringUp = %v, want Idle", got)
			}
		})
	}
}

func TestStateString(t *testing.T) {
	cases := map[State]string{
		StateIdle:   "idle",
		StateUp:     "up",
		State(99):   "unknown",
	}
	for st, want := range cases {
		if got := st.String(); got != want {
			t.Errorf("State(%d).String() = %q, want %q", st, got, want)
		}
	}
}

func TestSnapshot_IsDecoupledFromInternal(t *testing.T) {
	// A snapshot taken when the manager is idle must not panic when
	// inspected — callers receive zero-values for the pointer fields,
	// not a nil-deref booby-trap.
	m := newTestManager(t)
	snap := m.Snapshot()
	// All accesses must be safe; we're not asserting any specific
	// value here, only that nothing panics.
	_ = snap.State
	_ = snap.Since
	_ = snap.HostAddr
	_ = snap.Gateway
	_ = snap.DeviceName
	_ = snap.Allocator     // nil OK
	_ = snap.SubTypeByName // nil OK
}

func TestIsInsecureScheme(t *testing.T) {
	cases := map[string]bool{
		"http://127.0.0.1:8010":  true,
		"grpc://127.0.0.1:8010":  true,
		"https://x.example.com":  false,
		"grpcs://x.example.com":  false,
		"x.example.com:8010":     false, // bare host:port → TLS by convention
		"HTTP://upper.example":   true,  // case-insensitive
	}
	for url, want := range cases {
		t.Run(url, func(t *testing.T) {
			got := isInsecureScheme(url)
			if got != want {
				t.Errorf("isInsecureScheme(%q) = %v, want %v", url, got, want)
			}
		})
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("a", "b"); got != "a" {
		t.Errorf("firstNonEmpty(a,b) = %q, want a", got)
	}
	if got := firstNonEmpty("", "b"); got != "b" {
		t.Errorf("firstNonEmpty(,b) = %q, want b", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("firstNonEmpty(,) = %q, want empty", got)
	}
}

// newTestManager constructs a manager with safe defaults. Tests that
// need to mutate Options before construction use New() directly.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m, err := New(Options{
		Logger:      silentLogger(),
		SessionSeed: "unit-test",
		TLD:         "hoop",
		UserAgent:   "tunnelmgr-test/1.0",
	})
	if err != nil {
		t.Fatalf("newTestManager: %v", err)
	}
	return m
}

// TestNew_ErrorMessagesAreUseful documents what callers see when they
// forget a required option. Keeping these strings stable lets us
// pattern-match in upstream tests / logs without churn.
func TestNew_ErrorMessagesAreUseful(t *testing.T) {
	_, err := New(Options{SessionSeed: "s", TLD: "hoop", UserAgent: "ua"})
	if err == nil || !strings.Contains(err.Error(), "Logger is required") {
		t.Errorf("missing Logger error = %v, want substring 'Logger is required'", err)
	}
}

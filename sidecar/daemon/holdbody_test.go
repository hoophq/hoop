package daemon

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// holdingHTTPLane is holdingLane on http, holding POSTs, with no http
// block: capture_body is off.
func holdingHTTPLane() *Config {
	cfg := holdingLane()
	lc := &cfg.Listeners[0]
	lc.Protocol = "http"
	lc.Analyzer.Trigger.Operations = []inspect.Operation{inspect.OpPost}
	return cfg
}

// nonHoldingHTTPLane is the same lane blocking instead of holding.
func nonHoldingHTTPLane() *Config {
	cfg := holdingHTTPLane()
	cfg.Listeners[0].Analyzer.HighRisk = "block"
	cfg.Listeners[0].Analyzer.ApprovalRule = ""
	return cfg
}

const transfer = "POST /transfers HTTP/1.1\r\nHost: h\r\nContent-Length: 14\r\n\r\n{\"amount\":100}"

func buildHTTPLane(t *testing.T, cfg *Config, cp *controlPlane) lane {
	t.Helper()
	lanes, err := buildLanes(cfg, nil, &analyzerDeps{cfg: cfg.Analyzer, provider: highRiskProvider{}, cp: cp})
	if err != nil {
		t.Fatalf("buildLanes: %v", err)
	}
	return lanes[0]
}

// decodeOne runs raw bytes through the lane's own codec, as a connection
// would.
func decodeOne(t *testing.T, factory func() inspect.Codec, dir inspect.Direction, raw string) inspect.Statement {
	t.Helper()
	stmts, _, err := factory().Decode(dir, []byte(raw))
	if err != nil || len(stmts) != 1 {
		t.Fatalf("Decode: %d statements, %v", len(stmts), err)
	}
	return stmts[0]
}

// EVL-339: with capture_body off, a review filed only the request line, and
// its approval released the next request to that target with any body. The
// response body still needs capture_body: a hold reads requests only.
func TestAHoldingHTTPLaneFilesTheBodyWithoutCaptureBody(t *testing.T) {
	cp, calls := reviewPlane(t, http.StatusCreated,
		`{"forward":false,"review":{"id":"9f97","status":"REJECTED"}}`)
	ln := buildHTTPLane(t, holdingHTTPLane(), cp)

	if v := ln.policy.Evaluate(decodeOne(t, ln.codecFactory, inspect.FromClient, transfer)); !v.Denied {
		t.Fatal("a held request was forwarded")
	}
	if len(*calls) != 1 {
		t.Fatalf("the plane saw %d requests, want 1", len(*calls))
	}
	filed, _ := base64.StdEncoding.DecodeString((*calls)[0].payload)
	if want := "POST /transfers\n\n{\"amount\":100}"; string(filed) != want {
		t.Errorf("filed %q, want %q", filed, want)
	}

	resp := decodeOne(t, ln.codecFactory, inspect.FromServer,
		"HTTP/1.1 200 OK\r\nContent-Length: 11\r\n\r\n{\"ok\":true}")
	if resp.HTTP.Body != "" {
		t.Errorf("response body %q captured without capture_body", resp.HTTP.Body)
	}
}

// A lane that holds nothing keeps the old default: no body without
// capture_body.
func TestANonHoldingHTTPLaneCapturesNoBody(t *testing.T) {
	ln := buildHTTPLane(t, nonHoldingHTTPLane(), nil)
	if got := decodeOne(t, ln.codecFactory, inspect.FromClient, transfer).HTTP.Body; got != "" {
		t.Errorf("captured body %q", got)
	}
}

// recordingSwapper is a relay lane's swap target that keeps what it was
// handed.
type recordingSwapper struct{ got *lane }

func (r recordingSwapper) swapLane(ln lane, drained func()) {
	*r.got = ln
	drained()
}

// The analyzer block hot-swaps, so a reload can turn holding on. The lane
// handed to the swap must carry a codec that keeps the body, or the next
// connection holds with a codec from the generation that dropped it.
func TestAReloadThatTurnsHoldingOnSwapsInABodyCapturingCodec(t *testing.T) {
	cp, _ := reviewPlane(t, http.StatusCreated, `{}`)
	start, err := json.Marshal(nonHoldingHTTPLane())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfigBytes(start)
	if err != nil {
		t.Fatalf("LoadConfigBytes: %v", err)
	}
	ac := &analyzerDeps{cfg: cfg.Analyzer, provider: highRiskProvider{}, cp: cp}
	lanes, err := buildLanes(cfg, nil, ac)
	if err != nil {
		t.Fatalf("buildLanes: %v", err)
	}
	var swapped lane
	view := &atomic.Pointer[laneState]{}
	view.Store(&laneState{lanes: lanes})
	rl, err := newReloader(cfg, lanes, map[string]ruleSwapper{lanes[0].name: recordingSwapper{&swapped}},
		nil, ac, view, newLicenseState(cfg.lic, cfg.dependsOnLicense()))
	if err != nil {
		t.Fatalf("newReloader: %v", err)
	}

	held, err := json.Marshal(holdingHTTPLane())
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if got := rl.apply(slog.New(slog.NewTextHandler(&buf, nil)), held); got != reloadApplied {
		t.Fatalf("outcome = %v, want applied; log:\n%s", got, &buf)
	}
	if swapped.codecFactory == nil {
		t.Fatal("the reload swapped no codec factory into the lane")
	}
	if got := decodeOne(t, swapped.codecFactory, inspect.FromClient, transfer).HTTP.Body; got != `{"amount":100}` {
		t.Errorf("the swapped codec captured %q, want the request body", got)
	}
}

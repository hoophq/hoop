package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

const settleReviewID = "0b2f4c1e-8d5a-4f7b-9c3e-2a1d6e8f9b0c"

// settlePlane is a control plane that answers every filing with the release
// it holds in `release` and records what each call sent.
type settlePlane struct {
	mu      sync.Mutex
	release string
	bodies  []string // path + " " + body, in order
	settles []string // "<id>=<accepted>", in order
}

func newSettlePlane(t *testing.T, release string) (*controlPlane, *settlePlane) {
	t.Helper()
	p := &settlePlane{release: release}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		p.mu.Lock()
		defer p.mu.Unlock()
		p.bodies = append(p.bodies, r.URL.Path+" "+string(raw))
		switch {
		case strings.HasSuffix(r.URL.Path, "/"+controlPlaneSettlePath):
			var req struct {
				Accepted bool `json:"accepted"`
			}
			_ = json.Unmarshal(raw, &req)
			id := strings.Split(strings.TrimPrefix(r.URL.Path, controlPlaneReviewsPath+"/"), "/")[0]
			p.settles = append(p.settles, settleCall(id, req.Accepted))
			status := map[bool]string{true: "EXECUTED", false: "APPROVED"}[req.Accepted]
			_, _ = w.Write([]byte(`{"forward":false,"review":{"id":"` + id + `","status":"` + status + `"}}`))
		default:
			_, _ = w.Write([]byte(p.release))
		}
	}))
	t.Cleanup(srv.Close)
	return &controlPlane{url: srv.URL, cred: tokenCredential("hsc_token")}, p
}

func (p *settlePlane) sent() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.bodies)
}

func (p *settlePlane) settled() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.settles)
}

const reservedRelease = `{"forward":true,"review":{"id":"` + settleReviewID + `","status":"PROCESSING"}}`

// A lane that settles asks the plane to reserve; every other lane sends the
// bodies it always sent, so an older plane reads them as before.
func TestOnlyASettlingLaneAsksThePlaneToReserve(t *testing.T) {
	cp, plane := newSettlePlane(t, reservedRelease)
	ctx := context.Background()
	for _, settle := range []bool{false, true} {
		r := cp.reviewer("api", "approvers", settle, nil)
		if _, err := r.File(ctx, "POST /transfers"); err != nil {
			t.Fatalf("File: %v", err)
		}
		if _, err := r.Claim(ctx, settleReviewID); err != nil {
			t.Fatalf("Claim: %v", err)
		}
	}
	got := plane.sent()
	if len(got) != 4 {
		t.Fatalf("the plane saw %d requests, want 4: %v", len(got), got)
	}
	if strings.Contains(got[0], "reserve") || !strings.HasSuffix(got[1], "/claim ") {
		t.Errorf("a lane that does not settle sent %q and %q", got[0], got[1])
	}
	if !strings.Contains(got[2], `"reserve":true`) || !strings.HasSuffix(got[3], `/claim {"reserve":true}`) {
		t.Errorf("a settling lane sent %q and %q", got[2], got[3])
	}
}

func TestSettleReviewReportsTheAnswer(t *testing.T) {
	cp, plane := newSettlePlane(t, reservedRelease)
	status, err := cp.settleReview(context.Background(), settleReviewID, false)
	if err != nil || status != "APPROVED" {
		t.Fatalf("settleReview = %q, %v; want APPROVED", status, err)
	}
	if got := plane.settled(); !slices.Equal(got, []string{settleReviewID + "=refused"}) {
		t.Errorf("the plane recorded %v", got)
	}

	other, _ := reviewPlane(t, http.StatusOK, `{"forward":false,"review":{"id":"other","status":"EXECUTED"}}`)
	if _, err := other.settleReview(context.Background(), settleReviewID, true); err == nil ||
		!strings.Contains(err.Error(), "answered about review") {
		t.Errorf("an answer about another review was accepted: %v", err)
	}

	gone, _ := reviewPlane(t, http.StatusNotFound, `{"message":"review not found"}`)
	if _, err := gone.settleReview(context.Background(), settleReviewID, true); err == nil ||
		!strings.Contains(err.Error(), "no review") {
		t.Errorf("a 404 was not an error naming the review: %v", err)
	}
}

// A plane older than settling spends the approval at release. The lane
// still forwards, and the operator learns why a refusal used it.
func TestASettlingLaneWarnsWhenThePlaneSpendsAtRelease(t *testing.T) {
	cp, _ := newSettlePlane(t, `{"forward":true,"review":{"id":"`+settleReviewID+`","status":"EXECUTED"}}`)
	var logs bytes.Buffer
	r := cp.reviewer("api", "approvers", true, slog.New(slog.NewTextHandler(&logs, nil)))
	res, err := r.File(context.Background(), "POST /transfers")
	if err != nil || !res.Forward {
		t.Fatalf("File = %+v, %v; want a release", res, err)
	}
	if !strings.Contains(logs.String(), "spent the approval at release") {
		t.Errorf("no warning in %q", logs.String())
	}
}

// End to end on an http lane: the plane reserves, the upstream answers, and
// the plane learns the answer before the client reads it.
func TestAnHTTPLaneSettlesOnTheUpstreamAnswer(t *testing.T) {
	for code, accepted := range map[int]bool{
		http.StatusUnauthorized:       false,
		http.StatusOK:                 true,
		http.StatusServiceUnavailable: true,
	} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			cp, plane := newSettlePlane(t, reservedRelease)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(code)
			}))
			t.Cleanup(upstream.Close)
			cfg := holdingHTTPLane()
			cfg.Listeners[0].Analyzer.SpendApprovalOn = SpendOnAccepted
			cfg.Listeners[0].Listen = "127.0.0.1:0"
			cfg.Listeners[0].Upstream = strings.TrimPrefix(upstream.URL, "http://")
			cfg.cp = cp

			ln := buildHTTPLane(t, cfg, cp)
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			srv, err := buildServer(ln, AuditConfig{}, nil, nil, cfg.reviewSettler(log), log)
			if err != nil {
				t.Fatalf("buildServer: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() { _ = srv.Serve(ctx) }()
			addr := waitForAddr(t, srv)

			resp, err := http.Post("http://"+addr+"/transfers", "application/json",
				strings.NewReader(`{"amount":100}`))
			if err != nil {
				t.Fatalf("POST: %v", err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != code {
				t.Fatalf("the client read %d, want the upstream's %d", resp.StatusCode, code)
			}
			// No wait: the settle runs before the response is forwarded.
			if got, want := plane.settled(), settleCall(settleReviewID, accepted); !slices.Equal(got, []string{want}) {
				t.Errorf("the plane recorded %v, want [%s]", got, want)
			}
		})
	}
}

// settleCall is how the doubles here record one settle.
func settleCall(id string, accepted bool) string {
	if accepted {
		return id + "=accepted"
	}
	return id + "=refused"
}

func TestSpendApprovalOnIsValidated(t *testing.T) {
	settling := func(cfg *Config, v SpendApprovalOn) *Config {
		cfg.Listeners[0].Analyzer.SpendApprovalOn = v
		return cfg
	}
	for name, cfg := range map[string]*Config{
		"http accepted": settling(holdingHTTPLane(), SpendOnAccepted),
		"http forward":  settling(holdingHTTPLane(), SpendOnForward),
		"pg forward":    settling(holdingLane(), SpendOnForward),
	} {
		if err := cfg.Validate(); err != nil {
			t.Errorf("%s was refused: %v", name, err)
		}
	}
	for name, tc := range map[string]struct {
		cfg  *Config
		want string
	}{
		"unknown value": {settling(holdingHTTPLane(), "later"), `unknown spend_approval_on "later"`},
		"holds nothing": {settling(nonHoldingHTTPLane(), SpendOnAccepted), "nothing on this lane would hold"},
		"postgres lane": {settling(holdingLane(), SpendOnAccepted), "http, grpc and spanner lanes only"},
	} {
		if err := tc.cfg.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}

// The terminal reviewer releases one statement at once; only a plane can
// hold an approval until the upstream answers.
func TestSettlingNeedsAControlPlane(t *testing.T) {
	deps := &analyzerDeps{
		cfg:      &AnalyzerConfig{Provider: "stub", Model: "m"},
		provider: highRiskProvider{},
		local:    func(string) analyzer.Reviewer { return &localReviewer{} },
	}
	cfg := holdingHTTPLane()
	cfg.ControlPlaneURL = ""
	cfg.Listeners[0].Analyzer.ApprovalRule = ""
	cfg.Listeners[0].Analyzer.SpendApprovalOn = SpendOnAccepted
	if _, err := buildLanes(cfg, nil, deps); err == nil || !strings.Contains(err.Error(), "spend_approval_on") {
		t.Fatalf("a settling lane with a terminal reviewer was not refused by name: %v", err)
	}
}

// "forward" is the default, so it is served as no key at all; "accepted"
// goes only to a build that decodes it.
func TestSpendApprovalOnIsServedOnlyToABuildThatDecodesIt(t *testing.T) {
	forward := holdingHTTPLane()
	forward.Listeners[0].Analyzer.SpendApprovalOn = SpendOnForward
	served := ServedForm(*forward)
	if got := served.Listeners[0].Analyzer.SpendApprovalOn; got != "" {
		t.Errorf("ServedForm kept spend_approval_on %q", got)
	}
	if forward.Listeners[0].Analyzer.SpendApprovalOn != SpendOnForward {
		t.Error("ServedForm changed the stored listener")
	}
	if err := CheckServable(served, Handshake{Capabilities: []string{CapabilityReviewMode}}); err != nil {
		t.Errorf("the default was refused to an older build: %v", err)
	}

	accepted := holdingHTTPLane()
	accepted.Listeners[0].Analyzer.SpendApprovalOn = SpendOnAccepted
	err := CheckServable(ServedForm(*accepted), Handshake{Capabilities: []string{CapabilityReviewMode}})
	if err == nil || !strings.Contains(err.Error(), CapabilitySpendApprovalOn) {
		t.Errorf("accepted was served to a build that cannot decode it: %v", err)
	}
	if err := CheckServable(ServedForm(*accepted), Handshake{Capabilities: SidecarCapabilities()}); err != nil {
		t.Errorf("a current build was refused: %v", err)
	}
}

// settleRecorder is a gate.SettleFunc that records "<id>=<accepted>".
type settleRecorder struct {
	mu  sync.Mutex
	got []string
}

func (r *settleRecorder) settle(_ context.Context, id string, accepted bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, settleCall(id, accepted))
}

func (r *settleRecorder) settled() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.got)
}

// grpcReservingPolicy releases the call's headers on a reserved approval, as
// the analyzer does on a lane that settles, and refuses its message when
// denyMessage is set.
type grpcReservingPolicy struct{ denyMessage bool }

func (p grpcReservingPolicy) Evaluate(stmt inspect.Statement) policy.Verdict {
	switch {
	case stmt.Direction != inspect.FromClient:
		return policy.Allow()
	case stmt.Text == "/test.v1.Echo/Say":
		return policy.Verdict{Review: &policy.Review{ID: settleReviewID, Status: "PROCESSING", Reserved: true}}
	case p.denyMessage:
		return policy.Deny("rule", "refused")
	}
	return policy.Allow()
}

func TestAGRPCLaneSettlesOnTheUpstreamStatus(t *testing.T) {
	for name, tc := range map[string]struct {
		upstream    string
		denyMessage bool
		want        string
	}{
		"unauthenticated": {upstream: "16", want: settleReviewID + "=refused"},
		"ok":              {upstream: "0", want: settleReviewID + "=accepted"},
		"unavailable":     {upstream: "14", want: settleReviewID + "=accepted"},
		// The lane's own PERMISSION_DENIED is not the upstream refusing:
		// the headers already went upstream.
		"refused by the lane": {upstream: "16", denyMessage: true, want: settleReviewID + "=accepted"},
	} {
		t.Run(name, func(t *testing.T) {
			upstream, stopUpstream := startGRPCTestH2C(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "application/grpc")
				w.Header().Set("Grpc-Status", tc.upstream)
			}))
			defer stopUpstream()
			rec := &settleRecorder{}
			server, _, err := buildGRPCServer(lane{
				cfg: ListenerConfig{
					Name: "settle", Protocol: "grpc", Listen: "127.0.0.1:0", Upstream: upstream,
					GRPC: &GRPCCodecConfig{Descriptors: DescriptorPaths{writeGRPCTestDescriptors(t)}, CapturePayload: true},
				},
				name:   "settle",
				policy: grpcReservingPolicy{denyMessage: tc.denyMessage},
			}, AuditConfig{}, nil, rec.settle, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			laneAddr, stop := startGRPCTestServer(t, server)
			defer stop()

			if _, err := holdTestRoundTrip(context.Background(), laneAddr, "/test.v1.Echo/Say",
				marshalGRPCTestMessage("s", "v")); err != nil {
				t.Fatalf("round trip: %v", err)
			}
			if got := rec.settled(); !slices.Equal(got, []string{tc.want}) {
				t.Errorf("settles = %v, want [%s]", got, tc.want)
			}
		})
	}
}

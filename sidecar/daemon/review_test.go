package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// reviewCall is what the plane received, in the shape a reviewer's authority
// depends on: which sidecar (the token), which listener, which rule, and the
// exact statement bytes.
type reviewCall struct {
	token   string
	path    string
	listen  string
	rule    string
	payload string
}

// reviewPlane is a control plane that answers one canned response and records
// every request.
func reviewPlane(t *testing.T, status int, body string) (*controlPlane, *[]reviewCall) {
	t.Helper()
	calls := &[]reviewCall{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ListenerName string `json:"listener_name"`
			ApprovalRule string `json:"approval_rule"`
			Payload      string `json:"payload"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		*calls = append(*calls, reviewCall{
			token:   r.Header.Get(sidecarTokenHeader),
			path:    r.URL.Path,
			listen:  req.ListenerName,
			rule:    req.ApprovalRule,
			payload: req.Payload,
		})
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &controlPlane{url: srv.URL, token: "hsc_token"}, calls
}

// The wire contract, asserted from the plane's side: the token identifies the
// sidecar (there is no field for it, and there must not be), the listener and
// the rule are what the plane authorizes against, and the statement travels
// base64 because it is bytes.
func TestAFiledReviewCarriesTheListenerRuleAndStatement(t *testing.T) {
	cp, calls := reviewPlane(t, http.StatusCreated,
		`{"forward":false,"review":{"id":"9f97","status":"PENDING"}}`)

	res, err := cp.reviewer("payments", "payments-approvers").File(
		context.Background(), "DELETE FROM users WHERE email = 'a@b.c'")
	if err != nil {
		t.Fatalf("fileReview: %v", err)
	}

	if len(*calls) != 1 {
		t.Fatalf("the plane saw %d requests, want 1", len(*calls))
	}
	got := (*calls)[0]
	if got.path != controlPlaneReviewsPath {
		t.Errorf("the request went to %q, want %q", got.path, controlPlaneReviewsPath)
	}
	if got.token != "hsc_token" {
		t.Errorf("the request presented token %q", got.token)
	}
	if got.listen != "payments" || got.rule != "payments-approvers" {
		t.Errorf("the request named listener %q rule %q", got.listen, got.rule)
	}
	decoded, derr := base64.StdEncoding.DecodeString(got.payload)
	if derr != nil {
		t.Fatalf("the payload is not standard base64: %v", derr)
	}
	if string(decoded) != "DELETE FROM users WHERE email = 'a@b.c'" {
		t.Errorf("the plane received %q", decoded)
	}
	if res.Forward {
		t.Error("a filed review released the statement")
	}
	if res.ID != "9f97" || res.Status != "PENDING" {
		t.Errorf("the answer decoded as %+v", res)
	}
}

// forward is the only field that releases a statement. A claim race leaves
// both callers reading EXECUTED, and only the one that consumed the review
// may forward, so the status must never stand in for it.
func TestOnlyForwardReleasesTheStatement(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       bool
	}{
		{"claimed", `{"forward":true,"review":{"id":"9f97","status":"EXECUTED"}}`, true},
		{"claim lost", `{"forward":false,"review":{"id":"9f97","status":"EXECUTED"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cp, _ := reviewPlane(t, http.StatusOK, tc.body)
			res, err := cp.fileReview(context.Background(), "payments", "rule", "DELETE FROM t")
			if err != nil {
				t.Fatalf("fileReview: %v", err)
			}
			if res.Forward != tc.want {
				t.Errorf("forward is %v, want %v", res.Forward, tc.want)
			}
		})
	}
}

// Every refusal has to come back as an error, because the caller denies on
// one. The message names the plane and what it said: these land in the audit
// trail and the log, and an operator reading one has to know whether to fix
// the token, the rule or the deployment mode.
func TestEveryRefusalIsAnError(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		body string
		want string
	}{
		{"unauthorized", http.StatusUnauthorized, `{"message":"access denied"}`, "rejected the token"},
		{"not a control plane", http.StatusPreconditionFailed,
			`{"message":"sidecar reviews are served by the control plane"}`, "does not serve sidecar reviews"},
		{"too large", http.StatusRequestEntityTooLarge,
			`{"message":"statement is larger than 100000 bytes"}`, "too large to review"},
		{"rule not authorized", http.StatusUnprocessableEntity,
			`{"message":"listener \"payments\" is not configured to use approval rule \"x\""}`,
			"refused a review for listener"},
		{"server error", http.StatusInternalServerError, `{"message":"boom"}`, "500"},
		{"unreadable body", http.StatusOK, `not json`, "could not be read"},
		// A release names the review it spent, always. These two are what
		// something that is not the control plane answers, and a statement
		// must not go through on them.
		{"forward with no review", http.StatusOK, `{"forward":true}`, "without naming a review"},
		{"forward with an empty review id", http.StatusOK,
			`{"forward":true,"review":{"id":"","status":"EXECUTED"}}`, "without naming a review"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cp, _ := reviewPlane(t, tc.code, tc.body)
			res, err := cp.fileReview(context.Background(), "payments", "x", "DELETE FROM t")
			if err == nil {
				t.Fatalf("the answer was accepted as %+v", res)
			}
			if res.Forward {
				t.Error("a refused review released the statement")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v does not contain %q", err, tc.want)
			}
		})
	}
}

// The token rides a custom header, which Go's redirect handling forwards
// across origins. A misdirected URL must surface, not hand the token to
// whoever answered the Location.
func TestAReviewNeverFollowsARedirect(t *testing.T) {
	var followed bool
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		followed = true
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(final.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(srv.Close)

	cp := &controlPlane{url: srv.URL, token: "hsc_token"}
	_, err := cp.fileReview(context.Background(), "payments", "x", "DELETE FROM t")
	if err == nil {
		t.Fatal("a redirect was accepted")
	}
	if followed {
		t.Error("the token was re-sent to the redirect target")
	}
	if !strings.Contains(err.Error(), "redirected") {
		t.Errorf("error %v does not name the redirect", err)
	}
}

// A plane behind a path prefix is a supported deployment, so the reviews path
// is appended to whatever the operator configured rather than replacing it.
func TestAPathPrefixedPlaneKeepsItsPrefix(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"forward":false,"review":{"id":"9f97","status":"PENDING"}}`))
	}))
	t.Cleanup(srv.Close)

	cp := &controlPlane{url: srv.URL + "/hoop", token: "hsc_token"}
	if _, err := cp.fileReview(context.Background(), "payments", "x", "DELETE FROM t"); err != nil {
		t.Fatalf("fileReview: %v", err)
	}
	if want := "/hoop" + controlPlaneReviewsPath; path != want {
		t.Errorf("the request went to %q, want %q", path, want)
	}
}

// A waiting hold asks about the review it was given, by id, and nothing else:
// no listener, no rule, no statement. That is what stops a poll from ever
// filing, since the plane has nothing to file from.
func TestAClaimAsksAboutOneReviewByID(t *testing.T) {
	cp, calls := reviewPlane(t, http.StatusOK,
		`{"forward":true,"review":{"id":"9f97","status":"EXECUTED"}}`)

	res, err := cp.reviewer("payments", "payments-approvers").Claim(context.Background(), "9f97")
	if err != nil {
		t.Fatalf("claimReview: %v", err)
	}
	if !res.Forward {
		t.Error("a claimed approval did not release the statement")
	}
	if len(*calls) != 1 {
		t.Fatalf("the plane saw %d requests, want 1", len(*calls))
	}
	got := (*calls)[0]
	if want := controlPlaneReviewsPath + "/9f97/claim"; got.path != want {
		t.Errorf("the claim went to %q, want %q", got.path, want)
	}
	if got.token != "hsc_token" {
		t.Errorf("the claim presented token %q", got.token)
	}
	if got.payload != "" || got.listen != "" || got.rule != "" {
		t.Errorf("the claim carried a filing body: %+v", got)
	}
}

// An id is one path segment whatever it carries, so a hostile or broken id
// cannot steer the claim to another route. Unescaped, JoinPath would clean
// a/../b into b and claim a review nobody named.
func TestAClaimEscapesTheReviewID(t *testing.T) {
	cp, calls := reviewPlane(t, http.StatusOK,
		`{"forward":false,"review":{"id":"a/../b","status":"PENDING"}}`)

	if _, err := cp.claimReview(context.Background(), "a/../b"); err != nil {
		t.Fatalf("claimReview: %v", err)
	}
	if want := controlPlaneReviewsPath + "/a/../b/claim"; (*calls)[0].path != want {
		t.Errorf("the claim went to %q, want the escaped id under %q", (*calls)[0].path, want)
	}
}

// Every refusal of a claim is an error, so the hold denies. The id check is
// the claim's own: an answer about another review is not an answer to the
// question the hold asked.
func TestEveryClaimRefusalIsAnError(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		body string
		want string
	}{
		{"older plane or unknown review", http.StatusNotFound, `404 page not found`, "older than this sidecar"},
		{"unauthorized", http.StatusUnauthorized, `{"message":"access denied"}`, "rejected the token"},
		{"not a control plane", http.StatusPreconditionFailed,
			`{"message":"sidecar reviews are served by the control plane"}`, "does not serve sidecar reviews"},
		{"another review", http.StatusOK,
			`{"forward":true,"review":{"id":"other","status":"EXECUTED"}}`, "when asked about"},
		{"server error", http.StatusInternalServerError, `{"message":"boom"}`, "500"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cp, _ := reviewPlane(t, tc.code, tc.body)
			res, err := cp.claimReview(context.Background(), "9f97")
			if err == nil {
				t.Fatalf("the answer was accepted as %+v", res)
			}
			if res.Forward {
				t.Error("a refused claim released the statement")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v does not contain %q", err, tc.want)
			}
		})
	}
}

// A hold with nowhere to file is refused when the lanes are BUILT, not when
// the document is validated: a plane-served document carries no
// control_plane_url, so the document cannot answer this and the connection
// has to.
func TestAHoldWithNoPlaceToFileIsRefusedAtBuild(t *testing.T) {
	deps := &analyzerDeps{
		cfg:      &AnalyzerConfig{Provider: "stub", Model: "m"},
		provider: highRiskProvider{},
	}
	cfg := holdingLane()

	// The file names a plane: this is what -validate reads, and it builds.
	if _, err := buildLanes(cfg, nil, deps); err != nil {
		t.Fatalf("a validate run against a file naming a plane was refused: %v", err)
	}

	// Nothing names one: there is nowhere to file, and no connection to
	// make up for it.
	cfg.ControlPlaneURL = ""
	_, err := buildLanes(cfg, nil, deps)
	if err == nil {
		t.Fatal("a holding lane with no control plane was accepted")
	}
	if !strings.Contains(err.Error(), "has no control plane") {
		t.Errorf("error %v does not name the reason", err)
	}

	// A running process HAS one, and its document is the plane's own, which
	// never carries the URL. This is the deployment the check must not
	// refuse.
	deps.cp = &controlPlane{url: "https://cp.example.com", token: "t"}
	if _, err := buildLanes(cfg, nil, deps); err != nil {
		t.Fatalf("a plane-connected process was refused its own document: %v", err)
	}
}

// A process with no control plane has no reviewer, which is what makes the
// hold deny instead of build-failing: -validate builds every lane without
// ever contacting a plane.
func TestNoControlPlaneMeansNoReviewer(t *testing.T) {
	var cp *controlPlane
	if cp.reviewer("payments", "payments-approvers") != nil {
		t.Error("a process with no control plane built a reviewer")
	}
}

// highRiskProvider rates everything high, so a lane that holds high risk
// holds every statement it is shown.
type highRiskProvider struct{}

func (highRiskProvider) Name() string { return "stub" }

func (highRiskProvider) Classify(context.Context, string, string) (*analyzer.Result, error) {
	return &analyzer.Result{RiskLevel: analyzer.RiskHigh, Title: "dangerous"}, nil
}

// End to end through the real build path, for the one fact that cannot be
// asserted anywhere else: the review names the LISTENER. The evaluator's own
// name is the listener here only because a block has no rule name, and a
// deprecated ai_analysis rule on the same lane would carry an
// operator-chosen one. Passing the wrong name means the plane refuses a
// review a correct configuration authorized.
func TestAHoldingLaneFilesUnderTheListenerName(t *testing.T) {
	// REJECTED so the hold ends on the filing: a pending review would wait
	// out the whole budget, and this test is about the name, not the wait.
	cp, calls := reviewPlane(t, http.StatusCreated,
		`{"forward":false,"review":{"id":"9f97","status":"REJECTED"}}`)
	deps := &analyzerDeps{
		cfg:      &AnalyzerConfig{Provider: "stub", Model: "m"},
		provider: highRiskProvider{},
		cp:       cp,
	}
	la := laneBlock()
	la.HighRisk = "require_review"
	la.ApprovalRule = "payments-approvers"

	pol, err := buildPolicy("payments", GuardrailsConfig{}, la, nil, nil, deps)
	if err != nil {
		t.Fatalf("buildPolicy: %v", err)
	}
	v := pol.Evaluate(inspect.Statement{
		Protocol:  inspect.Postgres,
		Direction: inspect.FromClient,
		Text:      "DELETE FROM users",
		Operation: inspect.OpDelete,
		Tables:    []string{"users"},
	})

	if !v.Denied {
		t.Fatal("a held statement was forwarded")
	}
	if len(*calls) != 1 {
		t.Fatalf("the plane saw %d requests, want 1", len(*calls))
	}
	if got := (*calls)[0].listen; got != "payments" {
		t.Errorf("the review named listener %q, want payments", got)
	}
	if got := (*calls)[0].rule; got != "payments-approvers" {
		t.Errorf("the review named rule %q", got)
	}
}

// An observing lane runs every rule and records what it would have done. A
// review is the one thing it must NOT do: filing one pages a human about a
// statement that is about to run anyway, and leaves a pending review nobody
// will ever consume.
func TestAnObservingLaneRecordsTheHoldAndFilesNothing(t *testing.T) {
	cp, calls := reviewPlane(t, http.StatusCreated,
		`{"forward":false,"review":{"id":"9f97","status":"PENDING"}}`)
	deps := &analyzerDeps{
		cfg:      &AnalyzerConfig{Provider: "stub", Model: "m"},
		provider: highRiskProvider{},
		cp:       cp,
	}
	la := laneBlock()
	la.HighRisk = "require_review"
	la.ApprovalRule = "payments-approvers"

	pol, err := buildPolicy("payments", GuardrailsConfig{Mode: ModeObserve}, la, nil, nil, deps)
	if err != nil {
		t.Fatalf("buildPolicy: %v", err)
	}
	v := pol.Evaluate(inspect.Statement{
		Protocol:  inspect.Postgres,
		Direction: inspect.FromClient,
		Text:      "DELETE FROM users",
		Operation: inspect.OpDelete,
		Tables:    []string{"users"},
	})

	if v.Denied {
		t.Error("observe mode denied a held statement")
	}
	if v.Annotations[policy.AnnotationWouldDeny] == "" {
		t.Error("the dry run recorded no would_deny, so the trail cannot say what was held")
	}
	if len(*calls) != 0 {
		t.Errorf("observe mode filed %d reviews", len(*calls))
	}
}

// The three cases the lane builder refuses to file in, each for a different
// reason, all landing on the same nil that denies.
func TestReviewerForFilesOnlyWhereItShould(t *testing.T) {
	deps := &analyzerDeps{cp: &controlPlane{url: "https://cp.example.com", token: "t"}}
	holding := func() *LaneAnalyzerConfig {
		la := laneBlock()
		la.HighRisk = "require_review"
		la.ApprovalRule = "payments-approvers"
		return la
	}

	if deps.reviewerFor("payments", holding(), false) == nil {
		t.Error("a holding lane with a plane got no reviewer")
	}
	if deps.reviewerFor("payments", holding(), true) != nil {
		t.Error("an observed lane got a reviewer, so a dry run would page an approver")
	}
	if deps.reviewerFor("payments", laneBlock(), false) != nil {
		t.Error("a lane that names no approval_rule got a reviewer")
	}
	none := &analyzerDeps{}
	if none.reviewerFor("payments", holding(), false) != nil {
		t.Error("a lane with no control plane got a reviewer")
	}
}

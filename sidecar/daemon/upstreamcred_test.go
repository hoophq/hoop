package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/upstreamcred/upstreamcredtest"
)

// The upstream tokens are long enough for the placement guard and distinct
// enough that a substring search finds only a leak.
const (
	upstreamToken        = upstreamcredtest.Token
	rotatedUpstreamToken = "rotated-api-token-8c4b6e2a1f9d7035"
)

// ENG-886: each refusal below is a configuration that would send the lane's
// token somewhere other than its upstream's header, or let it reach the
// trail, policy or the analyzer.
func TestUpstreamCredentialIsRefusedWhereItCannotWork(t *testing.T) {
	const ok = `"upstream_credential":{"header":"Authorization","scheme":"Bearer","file":"/etc/api-token"}`
	cred := func(header string) string {
		return `"upstream_credential":{"header":"` + header + `","file":"/etc/api-token"}`
	}
	for _, tc := range []struct{ name, listener, want string }{
		{"off http", `"protocol":"postgres","upstream":"h:1",` + ok,
			"upstream_credential is only supported on http"},
		{"grpc", `"protocol":"grpc","upstream":"h:1",` + ok,
			"upstream_credential is only supported on http"},
		{"host:port upstream", `"protocol":"http","upstream":"api.example.com:443",` + ok,
			"upstream_credential needs upstream written as a URL"},
		{"the caller token's header", `"protocol":"http","upstream":"https://api.example.com",` +
			`"caller_token":{"file":"/etc/tokens","name":"agents"},` + cred("X-Hoop-Token"),
			"is the header this listener reads the caller from"},
		{"identity_header", `"protocol":"http","upstream":"https://api.example.com","identity_header":"x-api-key",` + cred("X-API-Key"),
			"is the header this listener reads the caller from"},
		{"google_identity's Authorization", `"protocol":"http","upstream":"https://api.example.com","google_identity":{},` + ok,
			"is the header this listener reads the caller from"},
		{"captured by http.headers", `"protocol":"http","upstream":"https://api.example.com","http":{"headers":["X-API-Key"]},` + cred("x-api-key"),
			`http.headers lists "x-api-key", the upstream_credential header`},
		{"a client-mode header", `"protocol":"http","upstream":"https://api.example.com",` + cred(analyzer.HeaderReviewMode),
			"a header the sidecar reads from the client"},
		{"framing", `"protocol":"http","upstream":"https://api.example.com",` + cred("Content-Length"),
			"frames or routes the request"},
		{"routing", `"protocol":"http","upstream":"https://api.example.com",` + cred("Host"),
			"frames or routes the request"},
		{"x-forwarded", `"protocol":"http","upstream":"https://api.example.com",` + cred("X-Forwarded-For"),
			"frames or routes the request"},
		{"bad scheme", `"protocol":"http","upstream":"https://api.example.com",` +
			`"upstream_credential":{"header":"Authorization","scheme":"Bearer x","file":"/etc/api-token"}`,
			"upstream_credential.scheme"},
		{"no file", `"protocol":"http","upstream":"https://api.example.com","upstream_credential":{"header":"Authorization"}`,
			"upstream_credential.file is required"},
		{"no header", `"protocol":"http","upstream":"https://api.example.com","upstream_credential":{"file":"/etc/api-token"}`,
			"upstream_credential.header is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := writeConfig(t, `{"listeners": [{"listen":":1",`+tc.listener+`}]}`)
			if got := loadErr(t, p); !strings.Contains(got, tc.want) {
				t.Errorf("error %q does not say %q", got, tc.want)
			}
		})
	}
}

// The shapes a lane needs: Authorization beside a caller token (the
// AgentCore template), an API key with no scheme, a cookie. The file is the
// sidecar host's, so loading never reads it.
func TestUpstreamCredentialLoadsWithoutReadingTheFile(t *testing.T) {
	for name, block := range map[string]string{
		"authorization": `"caller_token":{"file":"/nonexistent/tokens","name":"agents"},` +
			`"upstream_credential":{"header":"Authorization","scheme":"Bearer","file":"/nonexistent/api-token"}`,
		"api key": `"upstream_credential":{"header":"X-API-Key","file":"/nonexistent/api-token"}`,
		"cookie":  `"upstream_credential":{"header":"Cookie","scheme":"session","file":"/nonexistent/api-token"}`,
	} {
		t.Run(name, func(t *testing.T) {
			p := writeConfig(t, `{"listeners": [{"protocol":"http","listen":"http://127.0.0.1:0",`+
				`"upstream":"https://api.example.com",`+block+`}]}`)
			if _, err := LoadConfig(p); err != nil {
				t.Fatalf("upstream_credential refused: %v", err)
			}
		})
	}
}

// -validate and startup read the file. The error names the lane and the
// file, and never what the file holds, though it reaches the operator's
// terminal and the startup log.
func TestValidateNamesAnUnusableUpstreamCredentialFile(t *testing.T) {
	loose := upstreamcredtest.File(t, upstreamToken)
	if err := os.Chmod(loose, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"missing":            filepath.Join(t.TempDir(), "api-token"),
		"readable by others": loose,
		"header injection":   upstreamcredtest.File(t, upstreamToken+"\r\nX-Injected: 1"),
	} {
		t.Run(name, func(t *testing.T) {
			p := writeConfig(t, `{"listeners": [{"name":"customer-api","protocol":"http","listen":"http://127.0.0.1:0",
			  "upstream":"https://api.example.com","upstream_credential":{"header":"Authorization","scheme":"Bearer","file":"`+path+`"}}]}`)
			cfg, err := LoadConfig(p)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Validate(cfg, nil)
			if err == nil || !strings.Contains(err.Error(), "customer-api: upstream_credential") || !strings.Contains(err.Error(), path) {
				t.Fatalf("Validate error = %v, want one naming the lane, upstream_credential and %s", err, path)
			}
			if strings.Contains(err.Error(), upstreamToken) || strings.Contains(err.Error(), "X-Injected") {
				t.Errorf("Validate error carries the file's content: %v", err)
			}
		})
	}
}

// A lane built without its file does not start: it would forward the
// client's copy of the header in place of the lane's.
func TestAnUpstreamCredentialLaneWithoutItsFileDoesNotBuild(t *testing.T) {
	ln := lane{name: "api", cfg: ListenerConfig{Name: "api", Protocol: "http", Listen: "http://127.0.0.1:0",
		Upstream: "http://127.0.0.1:1", UpstreamCredential: &UpstreamCredentialConfig{Header: "Authorization", File: "/x"}}}
	ln.codecFactory = laneCodecFactory(ln.cfg, nil)
	if _, err := buildServer(ln, AuditConfig{}, nil, nil, nil, slog.New(slog.DiscardHandler)); err == nil ||
		!strings.Contains(err.Error(), "upstream_credential") {
		t.Fatalf("buildServer = %v, want a refusal naming upstream_credential", err)
	}
}

// credLaneRun is one upstream_credential lane, served as Run serves it,
// with every reader of a statement recording what it was handed.
type credLaneRun struct {
	addr     string
	sink     *sshTestSink
	cfg      *Config
	lane     lane
	credPath string
	logs     *syncBuffer
	up       *recordingUpstream

	mu       sync.Mutex
	opa      []string // OPA request bodies
	prompts  []string // the analyzer's system and user prompts
	policyIn []inspect.Statement
}

// promptRecorder is an analyzer provider that keeps every prompt and calls
// each request low risk.
type promptRecorder struct{ run *credLaneRun }

func (promptRecorder) Name() string { return "stub" }

func (p promptRecorder) Classify(_ context.Context, system, user string) (*analyzer.Result, error) {
	p.run.mu.Lock()
	p.run.prompts = append(p.run.prompts, system, user)
	p.run.mu.Unlock()
	return &analyzer.Result{RiskLevel: analyzer.RiskLow}, nil
}

// recordingPolicy wraps the lane's policy and keeps every statement it is
// asked to judge: the input local rules, OPA and the analyzer all read.
type recordingPolicy struct {
	run  *credLaneRun
	next policy.Evaluator
}

func (r recordingPolicy) Evaluate(stmt inspect.Statement) policy.Verdict {
	r.run.mu.Lock()
	r.run.policyIn = append(r.run.policyIn, stmt)
	r.run.mu.Unlock()
	return r.next.Evaluate(stmt)
}

// credLane serves the AgentCore shape: a caller token admits the agent, and
// the lane sends its own Authorization upstream. It also captures a header,
// runs a local rule on it, asks OPA and the analyzer about every request,
// and logs to a buffer, so each reader a statement reaches can be searched
// for the token.
func credLane(t *testing.T) *credLaneRun {
	t.Helper()
	run := &credLaneRun{sink: &sshTestSink{}, logs: &syncBuffer{}, up: newRecordingUpstream(t)}
	opa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		run.mu.Lock()
		run.opa = append(run.opa, string(body))
		run.mu.Unlock()
		_, _ = io.WriteString(w, `{"result":{"allow":true}}`)
	}))
	t.Cleanup(opa.Close)

	dir := t.TempDir()
	tokens := filepath.Join(dir, "tokens")
	if err := os.WriteFile(tokens, []byte(goodToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run.credPath = upstreamcredtest.File(t, upstreamToken+"\n")
	rule := policy.Rule{Name: "no-flagged-requests", Type: policy.MatchHTTPHeader}.
		WithHeaders(map[string][]string{"X-Request-Id": {"flagged"}})
	doc, err := json.Marshal(&Config{
		Analyzer: &AnalyzerConfig{Provider: "stub", Model: "m"},
		Listeners: []ListenerConfig{{
			Name: "api", Protocol: "http", Listen: "http://127.0.0.1:0", Upstream: run.up.URL,
			HTTP:               &HTTPCodecConfig{Headers: []string{"X-Request-Id"}, CaptureBody: true},
			CallerToken:        &CallerTokenConfig{File: tokens, Name: "claude-agents"},
			UpstreamCredential: &UpstreamCredentialConfig{Header: "Authorization", Scheme: "Bearer", File: run.credPath},
			Guardrails:         &GuardrailsConfig{Rules: []policy.Rule{rule}},
			OPA:                &OPAConfig{URL: opa.URL + "/v1/data/hoop"},
			Analyzer: &LaneAnalyzerConfig{
				Trigger:  &policy.AITrigger{Operations: []inspect.Operation{inspect.OpGet, inspect.OpPost}},
				HighRisk: "block",
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	run.cfg, err = LoadConfigBytes(doc)
	if err != nil {
		t.Fatalf("LoadConfigBytes: %v", err)
	}
	lanes, err := buildLanes(run.cfg, nil, &analyzerDeps{cfg: run.cfg.Analyzer, provider: promptRecorder{run}})
	if err != nil {
		t.Fatalf("buildLanes: %v", err)
	}
	log := slog.New(slog.NewTextHandler(run.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	if _, err := openCallerTokens(lanes, log); err != nil {
		t.Fatal(err)
	}
	if _, err := openUpstreamCredentials(lanes, log); err != nil {
		t.Fatal(err)
	}
	lanes[0].policy = recordingPolicy{run: run, next: lanes[0].policy}
	run.lane = lanes[0]
	srv, err := buildServer(lanes[0], AuditConfig{}, run.sink, pendingReviews(), nil, log)
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.Serve(ctx) }()
	run.addr = waitForAddr(t, srv)
	return run
}

// authorizations is the Authorization each request the upstream received
// carried, one entry per request, every copy joined.
func (r *credLaneRun) authorizations() []string {
	var out []string
	for _, h := range r.up.received() {
		out = append(out, strings.Join(h.Values("Authorization"), " | "))
	}
	return out
}

// The ticket's core, end to end: whatever the agent sends in Authorization,
// none, a placeholder, several in mixed case, the upstream reads exactly
// one, the lane's.
func TestAnUpstreamCredentialLaneReplacesTheClientsCopy(t *testing.T) {
	run := credLane(t)
	caller := "X-Hoop-Token: " + goodToken
	for name, lines := range map[string][]string{
		"none":        {caller},
		"placeholder": {caller, "Authorization: Bearer placeholder-from-the-agent"},
		"two copies":  {"authorization: Bearer first-copy-0000000000", caller, "AUTHORIZATION: Bearer second-copy-000000000"},
	} {
		t.Run(name, func(t *testing.T) {
			code, body := rawRequest(t, run.addr, getWith("/v1/items", lines...))
			if code != http.StatusOK || body != "ok" {
				t.Fatalf("answer = %d %q, want the upstream's 200", code, body)
			}
		})
	}
	got := run.authorizations()
	if len(got) != 3 {
		t.Fatalf("the upstream received %d requests, want 3", len(got))
	}
	for i, a := range got {
		if a != "Bearer "+upstreamToken {
			t.Errorf("request %d reached the upstream with Authorization %q, want only the lane's", i, a)
		}
	}
	for _, h := range run.up.received() {
		if v := h.Get("X-Hoop-Token"); v != "" {
			t.Errorf("the upstream received the caller token %q", v)
		}
	}
}

// DoD: the value never reaches the audit trail, policy input (local rules
// and OPA), the analyzer, logs or error text. Every reader is fed requests
// that pass, a request a local rule refuses, one that sends the token
// where it does not belong, and a head the lane cannot parse; then the file
// rotates, breaks and recovers. The token is searched for in all of them.
func TestTheUpstreamTokenReachesNoReaderOfTheRequest(t *testing.T) {
	run := credLane(t)
	caller := "X-Hoop-Token: " + goodToken
	var answers []string
	send := func(head string, want int) {
		t.Helper()
		code, body := rawRequest(t, run.addr, head)
		if code != want {
			t.Errorf("answer to %q = %d %q, want %d", head, code, body, want)
		}
		answers = append(answers, body)
	}
	send(getWith("/v1/items", caller, "X-Request-Id: r-1"), http.StatusOK)
	send("POST /v1/items HTTP/1.1\r\nHost: api.internal\r\n"+caller+
		"\r\nContent-Type: application/json\r\nContent-Length: 12\r\nConnection: close\r\n\r\n{\"name\":\"a\"}", http.StatusOK)
	send(getWith("/v1/items", caller, "X-Request-Id: flagged"), http.StatusForbidden)
	send(getWith("/v1/items?key="+upstreamToken, caller), http.StatusForbidden)
	send(getWith("/v1/items", caller, "X-Request-Id: "+upstreamToken), http.StatusForbidden)
	echo := `{"t":"` + upstreamToken + `"}`
	send("POST /v1/echo HTTP/1.1\r\nHost: api.internal\r\n"+caller+
		"\r\nContent-Length: "+strconv.Itoa(len(echo))+"\r\nConnection: close\r\n\r\n"+echo, http.StatusForbidden)
	if code, _ := rawRequest(t, run.addr, "GET /v1/items HTTP/1.1\r\nHost api.internal\r\n"+caller+"\r\n\r\n"); code == http.StatusOK {
		t.Error("a head the lane cannot parse reached the upstream")
	}

	// Rotate, break, recover: the lines a running lane logs about its file.
	old := time.Now().Add(-time.Hour)
	for _, step := range []struct {
		content string
		mode    os.FileMode
	}{{rotatedUpstreamToken, 0o600}, {"", 0o600}, {rotatedUpstreamToken, 0o644}, {upstreamToken, 0o600}} {
		if err := os.WriteFile(run.credPath, []byte(step.content), step.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(run.credPath, step.mode); err != nil {
			t.Fatal(err)
		}
		old = old.Add(time.Second)
		if err := os.Chtimes(run.credPath, old, old); err != nil {
			t.Fatal(err)
		}
		run.lane.upstreamCred.Poll()
	}
	send(getWith("/v1/items", caller), http.StatusOK)

	run.mu.Lock()
	defer run.mu.Unlock()
	if len(run.opa) == 0 || len(run.prompts) == 0 || len(run.policyIn) == 0 {
		t.Fatalf("a reader saw nothing: %d OPA inputs, %d prompts, %d policy inputs; the test proves nothing",
			len(run.opa), len(run.prompts), len(run.policyIn))
	}
	policyIn, err := json.Marshal(run.policyIn)
	if err != nil {
		t.Fatal(err)
	}
	trail, err := json.Marshal(run.sink.snapshot())
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{upstreamToken, rotatedUpstreamToken} {
		for reader, text := range map[string]string{
			"the audit trail":  string(trail),
			"policy input":     string(policyIn),
			"OPA input":        strings.Join(run.opa, "\n"),
			"the analyzer":     strings.Join(run.prompts, "\n"),
			"the log":          run.logs.String(),
			"the client's 403": strings.Join(answers, "\n"),
		} {
			if strings.Contains(text, tok) {
				t.Errorf("%s holds the upstream token %q", reader, tok)
			}
		}
	}
	// The trail still says what happened, by name.
	if placementRefusals(run.sink, "GET /v1/items") != 2 || placementRefusals(run.sink, "POST /v1/echo") != 1 {
		t.Errorf("want 3 placement refusals in the trail: %+v", run.sink.snapshot())
	}
	if !strings.Contains(run.logs.String(), "upstream credential file reloaded") ||
		!strings.Contains(run.logs.String(), "the upstream credential file cannot be read") {
		t.Errorf("the log does not show the rotation and the failed read:\n%s", run.logs.String())
	}
}

// A token rotates without a restart: the next request carries the new one.
func TestAnUpstreamCredentialRotatesWithoutARestart(t *testing.T) {
	run := credLane(t)
	if err := os.WriteFile(run.credPath, []byte(rotatedUpstreamToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run.lane.upstreamCred.Reload() // SIGHUP's forced read: the stat may not have moved
	if code, _ := rawRequest(t, run.addr, getWith("/v1/items", "X-Hoop-Token: "+goodToken)); code != http.StatusOK {
		t.Fatalf("answer = %d, want 200", code)
	}
	if got := run.authorizations(); len(got) != 1 || got[0] != "Bearer "+rotatedUpstreamToken {
		t.Errorf("after a rotation the upstream received %q, want the new token", got)
	}
}

// A rule reload swaps in a codec factory built from config alone, which
// holds no file. The credential reaches each connection's codec through the
// gate, so the next connection still carries it.
func TestAnUpstreamCredentialSurvivesARuleReload(t *testing.T) {
	run := credLane(t)
	srvLanes, err := buildLanes(run.cfg, nil, &analyzerDeps{cfg: run.cfg.Analyzer, provider: promptRecorder{run}})
	if err != nil {
		t.Fatal(err)
	}
	if srvLanes[0].upstreamCred != nil {
		t.Fatal("a lane a reload builds holds a credential; the test no longer proves the gate path")
	}
	// The running server is not reachable from credLane, so build a second
	// one beside it from the same lane and swap the reload's lane into it.
	srv, err := buildServer(run.lane, AuditConfig{}, run.sink, pendingReviews(), nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.Serve(ctx) }()
	addr := waitForAddr(t, srv)
	relayRules{srv}.swapLane(srvLanes[0], func() {})

	if code, _ := rawRequest(t, addr, getWith("/v1/items", "X-Hoop-Token: "+goodToken, "Authorization: Bearer agent-copy-0000000000")); code != http.StatusOK {
		t.Fatalf("answer = %d, want 200", code)
	}
	if got := run.authorizations(); len(got) != 1 || got[0] != "Bearer "+upstreamToken {
		t.Errorf("after a reload the upstream received %q, want the lane's token", got)
	}
}

// Changing the block itself needs a restart, as a caller_token block does:
// the running lane keeps the file it opened.
func TestAnUpstreamCredentialChangeIsRestartBound(t *testing.T) {
	run := credLane(t)
	changed := *run.cfg
	changed.Listeners = append([]ListenerConfig(nil), run.cfg.Listeners...)
	uc := *changed.Listeners[0].UpstreamCredential
	uc.File = "/elsewhere/api-token"
	changed.Listeners[0].UpstreamCredential = &uc
	before, err := nonRuleDoc(run.cfg)
	if err != nil {
		t.Fatal(err)
	}
	after, err := nonRuleDoc(&changed)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before, after) {
		t.Error("a changed upstream_credential block hot-reloads; the running lane would keep the old file")
	}
}

// HTTP/2 is bridged to HTTP/1.1 bytes that pass the same filter, so an h2
// client's request carries the lane's credential too.
func TestAnUpstreamCredentialLaneSendsItOverH2(t *testing.T) {
	run := credLane(t)
	p := new(http.Protocols)
	p.SetUnencryptedHTTP2(true)
	tr := &http.Transport{Protocols: p}
	t.Cleanup(tr.CloseIdleConnections)
	req, err := http.NewRequest(http.MethodGet, "http://"+run.addr+"/v1/items", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Hoop-Token", goodToken)
	req.Header.Set("Authorization", "Bearer agent-copy-0000000000")
	resp, err := (&http.Client{Transport: tr, Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("h2 request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.ProtoMajor != 2 {
		t.Fatalf("answer = %d over HTTP/%d, want 200 over h2", resp.StatusCode, resp.ProtoMajor)
	}
	if got := run.authorizations(); len(got) != 1 || got[0] != "Bearer "+upstreamToken {
		t.Errorf("the upstream received %q, want the lane's token", got)
	}
}

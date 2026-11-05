// Package mcpgateway is the engine of a sidecar mcp lane: it
// embeds mcpproxy to proxy one remote MCP server and hands each tool call to
// the daemon's gate as a statement.
package mcpgateway

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/hoophq/hoop/sidecar/daemon"
	sidecarinspect "github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/telemetry"
	"github.com/hoophq/mcpproxy/audit"
	"github.com/hoophq/mcpproxy/backend"
	"github.com/hoophq/mcpproxy/checks"
	"github.com/hoophq/mcpproxy/config"
	mcpgw "github.com/hoophq/mcpproxy/gateway"
	"github.com/hoophq/mcpproxy/inspect"
	"github.com/hoophq/mcpproxy/jsonrpc"
	"github.com/hoophq/mcpproxy/mcp"
)

func init() { daemon.RegisterMCPGatewayEngine(Build) }

const checkName = "hoop_gate"

// errSessionEnded answers a message on an MCP session that already ended
// here: EndSessions closed it, or it ended while the message was in flight.
// The Kill it turns into makes mcpproxy drop the session, so the client gets
// 404 next and initializes again.
var errSessionEnded = errors.New("the sidecar ended this session; initialize a new session")

var errServerClosed = errors.New("the lane is shutting down")

const (
	// codeInvalidParams is JSON-RPC's invalid params; mcpproxy does not name it.
	codeInvalidParams = -32602
	// goneTTL is how long an ended MCP session stays refused: longer than any
	// message still in flight on it.
	goneTTL = time.Hour
	// connIdleTimeout closes a keep-alive connection idle between requests;
	// a streaming response is not idle.
	connIdleTimeout = 2 * time.Minute
	// maxAdvertised caps the tool names a lane's telemetry carries.
	maxAdvertised = 1024
)

// mcpMethods are the methods MCP defines. Telemetry names any other method
// telemetry.MCPOther.
var mcpMethods = map[string]bool{
	mcp.MethodInitialize: true, mcp.MethodPing: true,
	mcp.MethodToolsList: true, mcp.MethodToolsCall: true,
	mcp.MethodResourcesList: true, "resources/templates/list": true, mcp.MethodResourcesRead: true,
	"resources/subscribe": true, "resources/unsubscribe": true,
	mcp.MethodPromptsList: true, mcp.MethodPromptsGet: true,
	"completion/complete": true, "logging/setLevel": true, "roots/list": true,
	mcp.MethodSamplingCreateMessage: true, mcp.MethodElicitationCreate: true,
	mcp.MethodServerDiscover: true, mcp.MethodSubscriptionsListen: true,
	mcp.MethodInitialized: true, "notifications/cancelled": true, mcp.MethodProgress: true,
	mcp.MethodLoggingMessage: true, mcp.MethodToolsListChanged: true,
	"notifications/resources/updated": true, "notifications/resources/list_changed": true,
	"notifications/prompts/list_changed": true, "notifications/roots/list_changed": true,
}

// Build implements daemon.MCPGatewayBuilder. It binds nothing.
func Build(lane daemon.MCPGatewayLane) (daemon.MCPGatewayServer, error) {
	s := &server{lane: lane, log: lane.Log, sessions: map[string]daemon.MCPLaneSession{}, gone: map[string]time.Time{}, renamed: map[string]string{},
		opening: map[string]chan struct{}{}, advertised: map[string]bool{}}
	for real, ov := range lane.Policy.Overrides {
		if ov.Name != "" {
			s.renamed[ov.Name] = real
		}
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	transport := backend.TransportStreamableHTTP
	if lane.Transport == daemon.MCPTransportSSE {
		transport = backend.TransportSSE
	}
	bcfg := config.Backend{Transport: transport, URL: lane.Upstream}
	origin, err := url.Parse(lane.Upstream)
	if err != nil {
		return nil, fmt.Errorf("mcp: upstream: %w", err)
	}
	client := &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSClientConfig:       lane.UpstreamTLS,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ForceAttemptHTTP2:     true,
	}, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		// A redirect stays within the upstream's origin, which validation checked.
		if req.URL.Scheme != origin.Scheme || req.URL.Host != origin.Host {
			return fmt.Errorf("redirect to %s://%s refused: outside the upstream origin", req.URL.Scheme, req.URL.Host)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}}
	factory := func(context.Context) (backend.Backend, error) {
		switch transport {
		case backend.TransportSSE:
			b := backend.NewSSE(lane.Name, bcfg, s.log)
			b.Client = client
			return b, nil
		default:
			b := backend.NewRemote(lane.Name, bcfg, s.log)
			b.Client = client
			return b, nil
		}
	}

	pipeline := checks.Assemble(policyFor(lane.Policy), inspect.Hooks{}, s, false)
	// The hide check runs right after the budget check, so a call to a hidden
	// tool still counts, and before the others: the rug-pull check never
	// fingerprints a hidden tool, the policy check never refuses a call to
	// one as unadvertised, and overrides has not renamed the catalog yet.
	budget := slices.IndexFunc(pipeline, func(c inspect.Check) bool { return c.Name() == checks.NameBudget })
	if budget < 0 {
		return nil, errors.New("mcp: the mcpproxy pipeline has no budget check to place the tool filter after")
	}
	pipeline = slices.Insert(pipeline, budget+1, inspect.Check(&hideCheck{s: s}))
	// The hoop check runs after mcpproxy's own (budgets, overrides) and
	// before the terminal audit tap.
	last := len(pipeline) - 1
	pipeline = append(pipeline[:last:last], &hoopCheck{s: s}, pipeline[last])

	gw, err := mcpgw.New(mcpgw.Options{
		Backends:           map[string]backend.Factory{lane.Name: factory},
		Pipeline:           pipeline,
		Sink:               s,
		Observer:           s,
		Logger:             s.log,
		SessionIdleTimeout: lane.SessionIdleTimeout,
		Resolver: func(r *http.Request) (inspect.Identity, error) {
			id := inspect.Identity{Claims: map[string]any{"peer": r.RemoteAddr}}
			switch {
			case lane.Caller != nil:
				c, ok := r.Context().Value(callerKey{}).(caller)
				if !ok {
					return inspect.Identity{}, errors.New("the request did not pass the caller check")
				}
				id.Subject, id.Key = c.subject, c.key
			case lane.IdentityHeader != "":
				id.Subject = r.Header.Get(lane.IdentityHeader)
			}
			return id, nil
		},
	})
	if err != nil {
		return nil, err
	}
	s.gw = gw
	return s, nil
}

func policyFor(p daemon.MCPGatewayConfig) config.Policy {
	out := config.Policy{
		BlockSampling:    p.BlockSampling,
		BlockElicitation: p.BlockElicitation,
		OnRugPull:        p.OnRugPull,
		MaxResultKB:      p.MaxResultKB,
		MaxCallsPerSess:  p.MaxCallsPerSession,
		ToolRateLimits:   p.ToolRateLimits,
	}
	if len(p.Overrides) > 0 {
		out.Overrides = map[string]config.ToolOverride{}
		for k, v := range p.Overrides {
			out.Overrides[k] = config.ToolOverride{Name: v.Name, Description: v.Description}
		}
	}
	return out
}

type server struct {
	lane daemon.MCPGatewayLane
	log  *slog.Logger
	gw   *mcpgw.Gateway
	// renamed maps an overrides name to the real tool name. A hidden tool
	// never reaches overrides, so its session holds no rename for it.
	renamed map[string]string

	mu       sync.Mutex
	ln       net.Listener
	srv      *http.Server
	sessions map[string]daemon.MCPLaneSession
	// opening holds the MCP sessions whose sidecar session is being opened,
	// outside s.mu: a slow audit write must not stall the other sessions.
	opening map[string]chan struct{}
	// gone holds the MCP sessions that ended here, so a message still in
	// flight on one opens no new sidecar session.
	gone      map[string]time.Time
	lastPurge time.Time
	closed    bool

	total, denied atomic.Int64

	// advertised holds the real names of the tools the upstream listed, up
	// to maxAdvertised: the tools telemetry names.
	advertisedMu sync.Mutex
	advertised   map[string]bool
}

func (s *server) Serve(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.lane.Listen)
	if err != nil {
		return fmt.Errorf("mcp: %w", err)
	}
	if s.lane.DownstreamTLS != nil {
		ln = tls.NewListener(ln, s.lane.DownstreamTLS)
	}
	srv := &http.Server{
		Handler:           s.checkCaller(s.gw.Handler()),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       connIdleTimeout,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}
	s.mu.Lock()
	s.ln, s.srv = ln, srv
	s.mu.Unlock()
	s.log.Info("mcp lane listening", "listen", ln.Addr().String(), "upstream", s.lane.Upstream, "transport", s.lane.Transport)
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	select {
	case err := <-served:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		_ = s.Close()
		<-served
		return nil
	}
}

type callerKey struct{}

type caller struct{ subject, key string }

// checkCaller removes the caller token header before mcpproxy reads the
// request, and refuses with 403 as an http lane does: a 401 sends an MCP
// client into OAuth discovery against the sidecar.
func (s *server) checkCaller(next http.Handler) http.Handler {
	check := s.lane.Caller
	if check == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		credential := strings.Join(r.Header.Values(check.Header), ", ")
		r.Header.Del(check.Header)
		subject, key, err := check.Resolve(r.Context(), credential)
		if err != nil {
			s.log.Info("mcp request refused", "peer", r.RemoteAddr, "reason", err.Error())
			w.Header().Set("Connection", "close")
			http.Error(w, "request refused: "+err.Error(), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, caller{subject, key})))
	})
}

func (s *server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	srv := s.srv
	sessions := s.sessions
	s.sessions = map[string]daemon.MCPLaneSession{}
	s.gone = map[string]time.Time{}
	s.mu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
	}
	s.gw.Close()
	for _, ls := range sessions {
		ls.Close(context.Background())
	}
	return nil
}

func (s *server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln != nil {
		return s.ln.Addr()
	}
	a, _ := net.ResolveTCPAddr("tcp", s.lane.Listen)
	return a
}

func (s *server) Stats() (active, total, denied int64) {
	s.mu.Lock()
	active = int64(len(s.sessions))
	s.mu.Unlock()
	return active, s.total.Load(), s.denied.Load()
}

// ObserveMessage implements mcpproxy's gateway.Observer. A message the
// pipeline refused counts as denied, whichever check refused it.
func (s *server) ObserveMessage(dir inspect.Direction, _, method, tool, decision string, _ time.Duration) {
	if decision != "allow" {
		s.denied.Add(1)
	}
	if s.lane.Telemetry == nil {
		return
	}
	m := telemetry.MCPMessage{Direction: sidecarinspect.FromClient, Method: method, Tool: s.toolLabel(tool), Decision: decision}
	if dir == inspect.S2C {
		m.Direction = sidecarinspect.FromServer
	}
	if method != "" && !mcpMethods[method] {
		m.Method = telemetry.MCPOther
	}
	s.lane.Telemetry.MCPMessage(m)
}

func (s *server) advertise(names []string) {
	s.advertisedMu.Lock()
	defer s.advertisedMu.Unlock()
	for _, n := range names {
		if len(s.advertised) >= maxAdvertised {
			return
		}
		s.advertised[n] = true
	}
}

// toolLabel is tool's real name when the upstream listed it, else
// telemetry.MCPOther.
func (s *server) toolLabel(tool string) string {
	if tool == "" {
		return ""
	}
	if real, ok := s.renamed[tool]; ok {
		tool = real
	}
	s.advertisedMu.Lock()
	defer s.advertisedMu.Unlock()
	if s.advertised[tool] {
		return tool
	}
	return telemetry.MCPOther
}

func (s *server) Notes() []string {
	return []string{fmt.Sprintf("mcp: proxies %s over %s; tool calls are judged by this lane's rules", s.lane.Upstream, s.lane.Transport)}
}

// EndSessions implements daemon.MCPGatewayServer: every open sidecar session
// closes now, and the MCP session it belonged to is refused from its next
// message on.
func (s *server) EndSessions(reason string) {
	s.mu.Lock()
	sessions := s.sessions
	s.sessions = map[string]daemon.MCPLaneSession{}
	now := time.Now()
	for id := range sessions {
		s.markGone(id, now)
	}
	for id := range s.opening {
		s.markGone(id, now)
	}
	s.mu.Unlock()
	ctx := context.Background()
	for _, ls := range sessions {
		ls.Record(ctx, "rules_reloaded", map[string]string{"reason": reason})
		ls.Close(ctx)
	}
}

// laneSession returns the sidecar session for one MCP session, opening it on
// the first message the pipeline sees.
func (s *server) laneSession(ctx context.Context, ms inspect.Session) (daemon.MCPLaneSession, error) {
	sid := ms.ID()
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return nil, errServerClosed
		}
		if ls, ok := s.sessions[sid]; ok {
			s.mu.Unlock()
			return ls, nil
		}
		if _, ok := s.gone[sid]; ok {
			s.mu.Unlock()
			return nil, errSessionEnded
		}
		if s.lane.Open == nil {
			s.mu.Unlock()
			return nil, errors.New("this lane records nothing (validate run)")
		}
		wait, opening := s.opening[sid]
		if !opening {
			break
		}
		s.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	done := make(chan struct{})
	s.opening[sid] = done
	s.mu.Unlock()

	id := ms.Identity()
	peer, _ := id.Claims["peer"].(string)
	ls, err := s.lane.Open(ctx, daemon.MCPCaller{Subject: id.Subject, PeerAddr: peer, MCPSessionID: sid})

	s.mu.Lock()
	delete(s.opening, sid)
	close(done)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	// The session ended, or the lane closed, while it was opening.
	var refused error
	if s.closed {
		refused = errServerClosed
	} else if _, ok := s.gone[sid]; ok {
		refused = errSessionEnded
	}
	if refused != nil {
		s.mu.Unlock()
		ls.Close(ctx)
		return nil, refused
	}
	s.sessions[sid] = ls
	s.total.Add(1)
	s.mu.Unlock()
	return ls, nil
}

// markGone records an ended MCP session and drops the ones past goneTTL,
// at most once a minute. s.mu must be held.
func (s *server) markGone(id string, now time.Time) {
	s.gone[id] = now
	if now.Sub(s.lastPurge) < time.Minute {
		return
	}
	s.lastPurge = now
	for id, at := range s.gone {
		if now.Sub(at) > goneTTL {
			delete(s.gone, id)
		}
	}
}

// Emit implements audit.Sink: it ends the sidecar session with the MCP
// session and records mcpproxy's own refusals on it.
func (s *server) Emit(ctx context.Context, ev audit.Event) {
	switch ev.Type {
	case audit.EventSessionEnd, audit.EventSessionKilled:
		s.mu.Lock()
		ls := s.sessions[ev.Session]
		delete(s.sessions, ev.Session)
		s.markGone(ev.Session, time.Now())
		s.mu.Unlock()
		if ls == nil {
			return
		}
		if ev.Type == audit.EventSessionKilled {
			ls.Record(ctx, "mcp_session_killed", map[string]string{"reason": ev.Reason})
		}
		ls.Close(ctx)
	case audit.EventToolDenied, audit.EventToolsChanged, audit.EventBudgetExceeded, audit.EventServerReqDenied:
		if ev.Rule == checkName {
			return // the gate already recorded the statement
		}
		s.mu.Lock()
		ls := s.sessions[ev.Session]
		s.mu.Unlock()
		if ls == nil {
			return
		}
		ls.Record(ctx, string(ev.Type), map[string]string{"tool": ev.Tool, "rule": ev.Rule, "reason": ev.Reason})
	case audit.EventSessionRefused:
		s.mu.Lock()
		ls := s.sessions[ev.Session]
		s.mu.Unlock()
		if ls == nil {
			return
		}
		ls.Record(ctx, string(ev.Type), map[string]string{"caller": ev.User, "reason": ev.Reason})
	}
}

type hoopCheck struct{ s *server }

func (c *hoopCheck) Name() string { return checkName }

func (c *hoopCheck) Inspect(ctx context.Context, ms inspect.Session, m *inspect.Msg) inspect.Decision {
	if m == nil || m.Env == nil {
		return inspect.AllowAll
	}
	ls, err := c.s.laneSession(ctx, ms)
	if err != nil {
		if m.Env.IsRequest() && !errors.Is(err, errSessionEnded) {
			return inspect.Denyf(m.Env, jsonrpc.CodePolicyDenied, checkName, "sidecar session unavailable: "+err.Error())
		}
		return inspect.Decision{Verdict: inspect.Kill, Reason: err.Error(), Rule: checkName}
	}
	if m.Dir == inspect.C2S {
		return c.request(ctx, ls, m)
	}
	return c.response(ctx, ls, ms, m)
}

func (c *hoopCheck) request(ctx context.Context, ls daemon.MCPLaneSession, m *inspect.Msg) inspect.Decision {
	switch m.Env.Method {
	case mcp.MethodInitialize:
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
			ClientInfo      struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"clientInfo"`
		}
		if err := json.Unmarshal(m.Env.Params, &p); err != nil {
			c.s.log.Warn("mcp initialize params not decoded", "error", err)
		}
		ls.Record(ctx, "mcp_initialize", map[string]string{
			"protocol_version": p.ProtocolVersion, "client": p.ClientInfo.Name, "client_version": p.ClientInfo.Version,
		})
	case mcp.MethodToolsCall:
		name, args, err := toolCallParams(m.Env.Params)
		if err != nil {
			return inspect.Denyf(m.Env, codeInvalidParams, checkName, "invalid tools/call params: "+err.Error())
		}
		v := ls.ToolCall(ctx, name, args)
		if !v.Allowed {
			return deniedToolResult(m.Env, v)
		}
	}
	return inspect.AllowAll
}

const metaHidden = "hoop.hidden_tools"

// hiddenTools hands what hideCheck left out of a tools/list response to
// hoopCheck, which judges the catalog after overrides. Keyed by response id:
// requests of one session run concurrently.
type hiddenTools struct {
	mu sync.Mutex
	m  map[string][]string
}

func sessionHidden(ms inspect.Session) *hiddenTools {
	return ms.Meta(metaHidden, func() any { return &hiddenTools{m: map[string][]string{}} }).(*hiddenTools)
}

func (h *hiddenTools) put(id string, names []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.m[id] = names
}

func (h *hiddenTools) take(id string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	names := h.m[id]
	delete(h.m, id)
	return names
}

// hideCheck leaves out of a tools/list response every tool an mcp_tool rule
// denies, and judges a call to a hidden tool before mcpproxy refuses it as
// unadvertised, so the denial names the rule.
type hideCheck struct{ s *server }

func (c *hideCheck) Name() string { return "hoop_hide" }

func (c *hideCheck) Inspect(ctx context.Context, ms inspect.Session, m *inspect.Msg) inspect.Decision {
	if m == nil || m.Env == nil {
		return inspect.AllowAll
	}
	if m.Dir == inspect.C2S && m.Env.Method == mcp.MethodToolsCall {
		return c.call(ctx, ms, m)
	}
	if m.Dir != inspect.S2C || m.CorrelatedMethod != mcp.MethodToolsList ||
		!m.Env.IsResponse() || m.Env.Error != nil || len(m.Env.Result) == 0 {
		return inspect.AllowAll
	}
	ls, err := c.s.laneSession(ctx, ms)
	if err != nil {
		return inspect.AllowAll // hoopCheck refuses the message
	}
	names, hidden, err := hideTools(ls, m)
	if err != nil {
		return unreadableCatalog(ctx, ls, err)
	}
	c.s.advertise(names)
	if len(hidden) > 0 {
		sessionHidden(ms).put(m.Env.ID.Key(), hidden)
	}
	return inspect.AllowAll
}

func (c *hideCheck) call(ctx context.Context, ms inspect.Session, m *inspect.Msg) inspect.Decision {
	ls, err := c.s.laneSession(ctx, ms)
	if err != nil {
		return inspect.AllowAll // hoopCheck refuses the message
	}
	name, args, err := toolCallParams(m.Env.Params)
	if err != nil {
		ls.Record(ctx, "mcp_invalid_params", map[string]string{"reason": err.Error()})
		return inspect.Denyf(m.Env, codeInvalidParams, checkName, "invalid tools/call params: "+err.Error())
	}
	tool := checks.RealToolName(ms, name)
	if real, ok := c.s.renamed[name]; ok && tool == name {
		tool = real
	}
	if !ls.Hidden(tool) {
		return inspect.AllowAll
	}
	v := ls.ToolCall(ctx, tool, args)
	if v.Allowed {
		return inspect.AllowAll
	}
	return deniedToolResult(m.Env, v)
}

// toolCallParams reads a tools/call's tool name and arguments by their exact
// keys, as an MCP server does. Params that repeat a key, exactly or in another
// case, are refused: a later check and the server could each read a
// different value, and judge one tool while the server runs another.
func toolCallParams(params json.RawMessage) (string, json.RawMessage, error) {
	notObject := errors.New("params are not an object")
	dec := json.NewDecoder(bytes.NewReader(params))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return "", nil, notObject
	}
	fields := map[string]json.RawMessage{}
	seen := map[string]string{}
	for dec.More() {
		tok, err := dec.Token()
		k, isKey := tok.(string)
		if err != nil || !isKey {
			return "", nil, notObject
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return "", nil, notObject
		}
		f := foldKey(k)
		if prev, dup := seen[f]; dup {
			if prev == k {
				return "", nil, fmt.Errorf("key %q appears twice", k)
			}
			return "", nil, fmt.Errorf("keys %q and %q differ only in case", prev, k)
		}
		seen[f] = k
		fields[k] = v
	}
	if _, err := dec.Token(); err != nil {
		return "", nil, notObject
	}
	if _, err := dec.Token(); err != io.EOF {
		return "", nil, notObject
	}
	var name string
	if err := json.Unmarshal(fields["name"], &name); err != nil || name == "" {
		return "", nil, errors.New("a tool name is required")
	}
	return name, fields["arguments"], nil
}

// foldKey maps s to one member of its case-fold class: two strings are equal
// under strings.EqualFold, as Go's JSON decoder matches keys, exactly when
// their foldKeys are equal.
func foldKey(s string) string {
	var b strings.Builder
	for _, r := range s {
		low := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			low = min(low, f)
		}
		b.WriteRune(low)
	}
	return b.String()
}

// hideTools drops from a tools/list result every tool ls hides, keeping the
// other fields of the result as they came. It returns every name the result
// listed and the names it dropped.
func hideTools(ls daemon.MCPLaneSession, m *inspect.Msg) (names, hidden []string, err error) {
	var result map[string]json.RawMessage
	if err := json.Unmarshal(m.Env.Result, &result); err != nil {
		return nil, nil, err
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(result["tools"], &raw); err != nil {
		return nil, nil, fmt.Errorf("tools: %w", err)
	}
	kept := make([]json.RawMessage, 0, len(raw))
	for _, entry := range raw {
		t, err := catalogTool(entry)
		if err != nil {
			return nil, nil, err
		}
		names = append(names, t.Name)
		if ls.Hidden(t.Name) {
			hidden = append(hidden, t.Name)
			continue
		}
		kept = append(kept, entry)
	}
	if len(hidden) == 0 {
		return names, nil, nil
	}
	if result["tools"], err = json.Marshal(kept); err != nil {
		return nil, nil, err
	}
	out, err := json.Marshal(result)
	if err != nil {
		return nil, nil, err
	}
	m.Env.Result = out
	return names, hidden, nil
}

// catalogTools reads the tools of a tools/list result for the catalog
// statement.
func catalogTools(result json.RawMessage) ([]daemon.MCPToolInfo, error) {
	var r struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		return nil, err
	}
	tools := make([]daemon.MCPToolInfo, 0, len(r.Tools))
	for _, entry := range r.Tools {
		t, err := catalogTool(entry)
		if err != nil {
			return nil, err
		}
		tools = append(tools, t)
	}
	return tools, nil
}

// catalogTool reads one tools/list entry. A tool needs a string name; a
// description that is not a string is kept as its JSON text.
func catalogTool(entry json.RawMessage) (daemon.MCPToolInfo, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(entry, &fields); err != nil {
		return daemon.MCPToolInfo{}, fmt.Errorf("a tool entry: %w", err)
	}
	var t daemon.MCPToolInfo
	if err := json.Unmarshal(fields["name"], &t.Name); err != nil || t.Name == "" {
		return t, errors.New("a tool entry without a string name")
	}
	if d, ok := fields["description"]; ok && json.Unmarshal(d, &t.Description) != nil {
		t.Description = string(d)
	}
	return t, nil
}

// unreadableCatalog ends a session whose tools/list result the lane cannot
// read: it could neither hide tools from it nor judge it.
func unreadableCatalog(ctx context.Context, ls daemon.MCPLaneSession, err error) inspect.Decision {
	ls.Record(ctx, "mcp_catalog_unreadable", map[string]string{"reason": err.Error()})
	return inspect.Decision{Verdict: inspect.Kill, Reason: "tool catalog unreadable: " + err.Error(), Rule: checkName}
}

// deniedToolResult answers a refused tools/call with a tool result that has
// isError set: MCP reports a failed call as a result, so the model sees it.
func deniedToolResult(req *jsonrpc.Message, v daemon.MCPVerdict) inspect.Decision {
	msg := "denied by the hoop sidecar"
	if v.Rule != "" {
		msg += " (rule " + v.Rule + ")"
	}
	if v.Message != "" {
		msg += ": " + v.Message
	}
	reply, err := jsonrpc.NewResult(req.ID, map[string]any{
		"content": []map[string]any{{"type": "text", "text": msg}},
		"isError": true,
	})
	if err != nil {
		return inspect.Denyf(req, jsonrpc.CodePolicyDenied, checkName, msg)
	}
	return inspect.Decision{Verdict: inspect.Deny, Reply: reply, Reason: msg, Rule: checkName}
}

func (c *hoopCheck) response(ctx context.Context, ls daemon.MCPLaneSession, ms inspect.Session, m *inspect.Msg) inspect.Decision {
	if !m.Env.IsResponse() || m.Env.Error != nil || len(m.Env.Result) == 0 {
		return inspect.AllowAll
	}
	switch m.CorrelatedMethod {
	case mcp.MethodToolsList:
		tools, err := catalogTools(m.Env.Result)
		if err != nil {
			return unreadableCatalog(ctx, ls, err)
		}
		if v := ls.ToolList(ctx, tools, sessionHidden(ms).take(m.Env.ID.Key())); !v.Allowed {
			return inspect.Decision{Verdict: inspect.Kill, Reason: "tool catalog denied: " + v.Message, Rule: checkName}
		}
	}
	return inspect.AllowAll
}

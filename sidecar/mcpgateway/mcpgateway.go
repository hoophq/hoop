// Package mcpgateway is the engine of a sidecar mcp lane: it
// embeds mcpproxy to proxy one remote MCP server and hands each tool call to
// the daemon's gate as a statement.
package mcpgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoophq/hoop/sidecar/daemon"
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
)

// Build implements daemon.MCPGatewayBuilder. It binds nothing.
func Build(lane daemon.MCPGatewayLane) (daemon.MCPGatewayServer, error) {
	s := &server{lane: lane, log: lane.Log, sessions: map[string]daemon.MCPLaneSession{}, gone: map[string]time.Time{}}
	if s.log == nil {
		s.log = slog.Default()
	}
	transport := backend.TransportStreamableHTTP
	if lane.Transport == daemon.MCPTransportSSE {
		transport = backend.TransportSSE
	}
	bcfg := config.Backend{Transport: transport, URL: lane.Upstream}
	client := &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSClientConfig:       lane.UpstreamTLS,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ForceAttemptHTTP2:     true,
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
	// The hide check runs first: mcpproxy's rug-pull check never fingerprints
	// a hidden tool, its policy check never refuses a call to one as
	// unadvertised, and overrides has not renamed the catalog yet.
	pipeline = slices.Insert(pipeline, 0, inspect.Check(&hideCheck{s: s}))
	// The hoop check runs after mcpproxy's own (budgets, overrides) and
	// before the terminal audit tap.
	last := len(pipeline) - 1
	pipeline = append(pipeline[:last:last], &hoopCheck{s: s}, pipeline[last])

	gw, err := mcpgw.New(mcpgw.Options{
		Backends:           map[string]backend.Factory{lane.Name: factory},
		Pipeline:           pipeline,
		Sink:               s,
		Logger:             s.log,
		SessionIdleTimeout: lane.SessionIdleTimeout,
		Resolver: func(r *http.Request) (inspect.Identity, error) {
			id := inspect.Identity{Claims: map[string]any{"peer": r.RemoteAddr}}
			if lane.IdentityHeader != "" {
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

	mu       sync.Mutex
	ln       net.Listener
	srv      *http.Server
	sessions map[string]daemon.MCPLaneSession
	// gone holds the MCP sessions that ended here, so a message still in
	// flight on one opens no new sidecar session.
	gone      map[string]time.Time
	lastPurge time.Time
	closed    bool

	total, denied atomic.Int64
}

func (s *server) Serve(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.lane.Listen)
	if err != nil {
		return fmt.Errorf("mcp: %w", err)
	}
	srv := &http.Server{Handler: s.gw.Handler(), ReadHeaderTimeout: 10 * time.Second}
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errServerClosed
	}
	if ls, ok := s.sessions[ms.ID()]; ok {
		return ls, nil
	}
	if _, ok := s.gone[ms.ID()]; ok {
		return nil, errSessionEnded
	}
	if s.lane.Open == nil {
		return nil, errors.New("this lane records nothing (validate run)")
	}
	id := ms.Identity()
	peer, _ := id.Claims["peer"].(string)
	ls, err := s.lane.Open(ctx, daemon.MCPCaller{Subject: id.Subject, PeerAddr: peer, MCPSessionID: ms.ID()})
	if err != nil {
		return nil, err
	}
	s.sessions[ms.ID()] = ls
	s.total.Add(1)
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
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(m.Env.Params, &p); err != nil || p.Name == "" {
			c.s.denied.Add(1)
			return inspect.Denyf(m.Env, codeInvalidParams, checkName, "invalid tools/call params: a tool name is required")
		}
		v := ls.ToolCall(ctx, p.Name, p.Arguments)
		if !v.Allowed {
			c.s.denied.Add(1)
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
	hidden, err := hideTools(ls, m)
	if err != nil {
		return unreadableCatalog(ctx, ls, err)
	}
	if len(hidden) > 0 {
		sessionHidden(ms).put(m.Env.ID.Key(), hidden)
	}
	return inspect.AllowAll
}

func (c *hideCheck) call(ctx context.Context, ms inspect.Session, m *inspect.Msg) inspect.Decision {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if json.Unmarshal(m.Env.Params, &p) != nil || p.Name == "" {
		return inspect.AllowAll // hoopCheck refuses it as invalid params
	}
	ls, err := c.s.laneSession(ctx, ms)
	if err != nil {
		return inspect.AllowAll
	}
	tool := checks.RealToolName(ms, p.Name)
	if !ls.Hidden(tool) {
		return inspect.AllowAll
	}
	v := ls.ToolCall(ctx, tool, p.Arguments)
	if v.Allowed {
		return inspect.AllowAll
	}
	c.s.denied.Add(1)
	return deniedToolResult(m.Env, v)
}

// hideTools drops from a tools/list result every tool ls hides, keeping the
// other fields of the result as they came, and returns the names it dropped.
func hideTools(ls daemon.MCPLaneSession, m *inspect.Msg) ([]string, error) {
	var result map[string]json.RawMessage
	if err := json.Unmarshal(m.Env.Result, &result); err != nil {
		return nil, err
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(result["tools"], &raw); err != nil {
		return nil, fmt.Errorf("tools: %w", err)
	}
	kept := make([]json.RawMessage, 0, len(raw))
	var hidden []string
	for _, entry := range raw {
		t, err := catalogTool(entry)
		if err != nil {
			return nil, err
		}
		if ls.Hidden(t.Name) {
			hidden = append(hidden, t.Name)
			continue
		}
		kept = append(kept, entry)
	}
	if len(hidden) == 0 {
		return nil, nil
	}
	var err error
	if result["tools"], err = json.Marshal(kept); err != nil {
		return nil, err
	}
	out, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	m.Env.Result = out
	return hidden, nil
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
			c.s.denied.Add(1)
			return inspect.Decision{Verdict: inspect.Kill, Reason: "tool catalog denied: " + v.Message, Rule: checkName}
		}
	}
	return inspect.AllowAll
}

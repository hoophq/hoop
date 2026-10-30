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

// errSessionEnded answers a message on an MCP session EndSessions closed.
// The Kill it turns into makes mcpproxy drop the session, so the client
// gets 404 next and initializes again.
var errSessionEnded = errors.New("the sidecar ended this session (rules reloaded); initialize a new session")

// Build implements daemon.MCPGatewayBuilder. It binds nothing.
func Build(lane daemon.MCPGatewayLane) (daemon.MCPGatewayServer, error) {
	s := &server{lane: lane, log: lane.Log, sessions: map[string]daemon.MCPLaneSession{}, stale: map[string]struct{}{}}
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
	// The hoop check runs after mcpproxy's own (tool filtering, overrides,
	// budgets) and before the terminal audit tap.
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
		AllowedTools:     p.AllowedTools,
		DeniedTools:      p.DeniedTools,
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
	// stale holds the MCP sessions EndSessions closed and mcpproxy still
	// knows: the next message on one is refused with a Kill.
	stale  map[string]struct{}
	closed bool

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
	s.stale = map[string]struct{}{}
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
	for id := range sessions {
		s.stale[id] = struct{}{}
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
	if ls, ok := s.sessions[ms.ID()]; ok {
		return ls, nil
	}
	if _, ok := s.stale[ms.ID()]; ok {
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

// Emit implements audit.Sink: it ends the sidecar session with the MCP
// session and records mcpproxy's own refusals on it.
func (s *server) Emit(ctx context.Context, ev audit.Event) {
	switch ev.Type {
	case audit.EventSessionEnd, audit.EventSessionKilled:
		s.mu.Lock()
		ls := s.sessions[ev.Session]
		delete(s.sessions, ev.Session)
		delete(s.stale, ev.Session)
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
	return c.response(ctx, ls, m)
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
		_ = json.Unmarshal(m.Env.Params, &p)
		ls.Record(ctx, "mcp_initialize", map[string]string{
			"protocol_version": p.ProtocolVersion, "client": p.ClientInfo.Name, "client_version": p.ClientInfo.Version,
		})
	case mcp.MethodToolsCall:
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(m.Env.Params, &p)
		v := ls.ToolCall(ctx, p.Name, p.Arguments)
		if !v.Allowed {
			c.s.denied.Add(1)
			return deniedToolResult(m.Env, v)
		}
	}
	return inspect.AllowAll
}

// deniedToolResult answers a refused tools/call with a tool result that has
// isError set, so the agent reads a tool error and keeps the server.
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

func (c *hoopCheck) response(ctx context.Context, ls daemon.MCPLaneSession, m *inspect.Msg) inspect.Decision {
	if !m.Env.IsResponse() || m.Env.Error != nil || len(m.Env.Result) == 0 {
		return inspect.AllowAll
	}
	switch m.CorrelatedMethod {
	case mcp.MethodToolsList:
		var r struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(m.Env.Result, &r); err != nil {
			return inspect.AllowAll
		}
		tools := make([]daemon.MCPToolInfo, 0, len(r.Tools))
		for _, t := range r.Tools {
			tools = append(tools, daemon.MCPToolInfo{Name: t.Name, Description: t.Description})
		}
		if v := ls.ToolList(ctx, tools); !v.Allowed {
			c.s.denied.Add(1)
			return inspect.Decision{Verdict: inspect.Kill, Reason: "tool catalog denied: " + v.Message, Rule: checkName}
		}
	}
	return inspect.AllowAll
}

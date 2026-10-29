package daemon

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
)

// RD-379 spike. The engine (mcpproxy) lives in the nested sidecar/mcpgateway
// module; this file is the contract, as ica.go is for libhoop-ica.

func isMCPGateway(lc ListenerConfig) bool {
	return inspect.Protocol(lc.Protocol) == inspect.MCP
}

// MCPGatewayConfig is a listener's mcp_gateway block: the upstream transport
// and mcpproxy's tool policy, field for field.
type MCPGatewayConfig struct {
	Transport          string                     `json:"transport,omitempty"`
	AllowedTools       []string                   `json:"allowed_tools,omitempty"`
	DeniedTools        []string                   `json:"denied_tools,omitempty"`
	BlockSampling      *bool                      `json:"block_sampling,omitempty"`
	BlockElicitation   *bool                      `json:"block_elicitation,omitempty"`
	OnRugPull          string                     `json:"on_rug_pull,omitempty"`
	MaxCallsPerSession int                        `json:"max_calls_per_session,omitempty"`
	MaxResultKB        int                        `json:"max_result_kb,omitempty"`
	ToolRateLimits     map[string]int             `json:"tool_rate_limits,omitempty"`
	Overrides          map[string]MCPToolOverride `json:"overrides,omitempty"`
}

type MCPToolOverride struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

const (
	MCPTransportStreamableHTTP = "streamable-http"
	MCPTransportSSE            = "sse"
)

func (m *MCPGatewayConfig) transport() string {
	if m == nil || m.Transport == "" {
		return MCPTransportStreamableHTTP
	}
	return m.Transport
}

func validateMCPGatewayLane(lc ListenerConfig, name string) []string {
	var problems []string
	u, err := url.Parse(lc.Upstream)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		problems = append(problems, name+": an mcp listener needs upstream as an http(s) URL of the MCP server, such as https://mcp.example.com/mcp")
	}
	if lc.Network == "unix" {
		problems = append(problems, name+": an mcp listener listens on tcp; MCP clients do not connect to unix sockets")
	} else if host, _, err := net.SplitHostPort(lc.Listen); err == nil && !loopbackHost(host) {
		problems = append(problems, name+": an mcp listener may bind only a loopback address until it authenticates its callers (I-18 R6)")
	}
	if lc.MaxConns > 0 {
		problems = append(problems, name+": max_conns does not apply to an mcp listener; MCP runs over HTTP requests, not long connections")
	}
	if lc.DownstreamTLS != nil {
		problems = append(problems, name+": downstream_tls is not supported on an mcp listener yet")
	}
	m := lc.MCPGateway
	if m == nil {
		return problems
	}
	switch m.Transport {
	case "", MCPTransportStreamableHTTP, MCPTransportSSE:
	default:
		problems = append(problems, fmt.Sprintf("%s: mcp_gateway.transport %q is not streamable-http or sse", name, m.Transport))
	}
	switch m.OnRugPull {
	case "", "kill", "alert":
	default:
		problems = append(problems, fmt.Sprintf("%s: mcp_gateway.on_rug_pull %q is not kill or alert", name, m.OnRugPull))
	}
	if m.MaxCallsPerSession < 0 || m.MaxResultKB < 0 {
		problems = append(problems, name+": mcp_gateway budgets cannot be negative")
	}
	for glob, n := range m.ToolRateLimits {
		if n <= 0 {
			problems = append(problems, fmt.Sprintf("%s: mcp_gateway.tool_rate_limits[%q] must be positive", name, glob))
		}
	}
	return problems
}

func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	a, err := netip.ParseAddr(host)
	return err == nil && a.IsLoopback()
}

// MCPCaller is who opened one MCP session, as the engine resolved it.
type MCPCaller struct {
	Subject      string
	PeerAddr     string
	MCPSessionID string
}

// MCPVerdict is the daemon's answer for one statement the engine handed over.
type MCPVerdict struct {
	Allowed bool
	Message string
	Rule    string
}

// MCPToolInfo is one tool of a tools/list answer.
type MCPToolInfo struct {
	Name        string
	Description string
}

// MCPLaneSession is one sidecar session per MCP session. Calls are
// serialized: a gate is not safe for concurrent use.
type MCPLaneSession interface {
	ID() string
	ToolCall(ctx context.Context, tool string, args []byte) MCPVerdict
	ToolList(ctx context.Context, tools []MCPToolInfo) MCPVerdict
	Mask(ctx context.Context, text string) (string, int)
	Record(ctx context.Context, activity string, attrs map[string]string)
	Close(ctx context.Context)
}

// MCPGatewayLane is the daemon's input to the engine for one lane.
type MCPGatewayLane struct {
	Name           string
	Listen         string
	Upstream       string
	Transport      string
	Policy         MCPGatewayConfig
	UpstreamTLS    *tls.Config
	IdentityHeader string
	// SessionIdleTimeout is idle_timeout_sec; zero keeps mcpproxy's 30 minutes.
	SessionIdleTimeout time.Duration
	// Open starts the sidecar session for one MCP session. Nil on a
	// -validate run.
	Open func(ctx context.Context, caller MCPCaller) (MCPLaneSession, error)
	Log  *slog.Logger
}

// MCPGatewayServer satisfies endpointServer. The builder must not bind a
// socket: Validate constructs it on the control plane.
type MCPGatewayServer interface {
	Serve(ctx context.Context) error
	Close() error
	Addr() net.Addr
	Stats() (active, total, denied int64)
	Notes() []string
}

type MCPGatewayBuilder func(lane MCPGatewayLane) (MCPGatewayServer, error)

var (
	mcpGatewayMu    sync.Mutex
	mcpGatewayBuild MCPGatewayBuilder
)

func RegisterMCPGatewayEngine(build MCPGatewayBuilder) {
	if build == nil {
		panic("sidecar/daemon: RegisterMCPGatewayEngine called with a nil builder")
	}
	mcpGatewayMu.Lock()
	defer mcpGatewayMu.Unlock()
	if mcpGatewayBuild != nil {
		panic("sidecar/daemon: duplicate MCP gateway engine registration")
	}
	mcpGatewayBuild = build
}

func registeredMCPGatewayEngine() MCPGatewayBuilder {
	mcpGatewayMu.Lock()
	defer mcpGatewayMu.Unlock()
	return mcpGatewayBuild
}

func checkMCPGateway(cfg *Config) error {
	if registeredMCPGatewayEngine() != nil {
		return nil
	}
	for i, l := range cfg.Listeners {
		if isMCPGateway(l) {
			return fmt.Errorf("%s: protocol mcp needs the MCP gateway engine, and this build did not link it; "+
				"use the hoop-inspect binary, which imports sidecar/mcpgateway", l.displayName(i))
		}
	}
	return nil
}

func buildMCPGatewayServer(ln lane, ac AuditConfig, sink audit.Sink, log *slog.Logger) (MCPGatewayServer, *liveRules, error) {
	build := registeredMCPGatewayEngine()
	if build == nil {
		return nil, nil, fmt.Errorf("%s: protocol mcp needs the MCP gateway engine, and this build did not link it", ln.name)
	}
	lc := ln.cfg
	tlsConf, err := lc.UpstreamTLS.BuildTLS(ln.trustRoots)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: upstream_tls: %w", ln.name, err)
	}
	if tlsConf == nil {
		tlsConf = &tls.Config{RootCAs: ln.trustRoots, MinVersion: tls.VersionTLS12}
	}
	rules := newLiveRules(ln)
	laneLog := log.With("listener", ln.name)
	resolved := MCPGatewayLane{
		Name:               ln.name,
		Listen:             lc.Listen,
		Upstream:           lc.Upstream,
		Transport:          lc.MCPGateway.transport(),
		UpstreamTLS:        tlsConf,
		IdentityHeader:     lc.IdentityHeader,
		SessionIdleTimeout: time.Duration(lc.IdleTimeoutSec) * time.Second,
		Log:                laneLog,
	}
	if lc.MCPGateway != nil {
		resolved.Policy = *lc.MCPGateway
	}
	if sink != nil {
		failOnAuditError := ac.failOnAuditError()
		resolved.Open = func(ctx context.Context, caller MCPCaller) (MCPLaneSession, error) {
			return openMCPLaneSession(ctx, ln, rules, sink, failOnAuditError, caller)
		}
	}
	srv, err := build(resolved)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", ln.name, err)
	}
	return srv, rules, nil
}

type mcpLaneSession struct {
	mu      sync.Mutex
	g       *gate.Gate
	release func()
	mcpSID  string
	closed  bool
}

func openMCPLaneSession(ctx context.Context, ln lane, rules *liveRules, sink audit.Sink, failOnAuditError bool, caller MCPCaller) (MCPLaneSession, error) {
	sess := session.New(inspect.MCP, session.Identity{Subject: caller.Subject, PeerAddr: caller.PeerAddr})
	sess.Connection = ln.name
	sess.Upstream = ln.cfg.Upstream
	live, release := rules.acquire()
	g, err := gate.NewStatementGate(sess, gate.Config{
		Protocol:         inspect.MCP,
		Policy:           live.policy,
		Audit:            sink,
		Masker:           live.masker,
		FailOnAuditError: failOnAuditError,
		Metrics:          ln.metrics,
		Telemetry:        ln.telemetry,
	})
	if err != nil {
		release()
		return nil, err
	}
	if err := g.Start(ctx); err != nil && failOnAuditError {
		_ = g.Close(ctx)
		release()
		return nil, fmt.Errorf("audit trail unavailable: %w", err)
	}
	return &mcpLaneSession{g: g, release: release, mcpSID: caller.MCPSessionID}, nil
}

func (s *mcpLaneSession) ID() string { return string(s.g.Session().ID) }

func (s *mcpLaneSession) ToolCall(ctx context.Context, tool string, args []byte) MCPVerdict {
	stmt := inspect.Statement{
		Protocol:  inspect.MCP,
		Direction: inspect.FromClient,
		Text:      tool + "\n" + string(args),
		Operation: inspect.OpCall,
		Tables:    []string{strings.ToLower(tool)},
		Metadata:  map[string]string{"mcp.method": "tools/call", "mcp.tool": tool, "mcp.session": s.mcpSID},
	}
	return s.evaluate(ctx, stmt)
}

func (s *mcpLaneSession) ToolList(ctx context.Context, tools []MCPToolInfo) MCPVerdict {
	var b strings.Builder
	for _, t := range tools {
		fmt.Fprintf(&b, "%s: %s\n", t.Name, t.Description)
	}
	stmt := inspect.Statement{
		Protocol:  inspect.MCP,
		Direction: inspect.FromServer,
		Text:      b.String(),
		Operation: inspect.OpShow,
		Metadata:  map[string]string{"mcp.method": "tools/list", "mcp.session": s.mcpSID},
	}
	return s.evaluate(ctx, stmt)
}

func (s *mcpLaneSession) evaluate(ctx context.Context, stmt inspect.Statement) MCPVerdict {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return MCPVerdict{Message: "session closed"}
	}
	d := s.g.EvaluateStatement(ctx, stmt)
	return MCPVerdict{Allowed: d.Allowed, Message: d.Message, Rule: d.Rule}
}

func (s *mcpLaneSession) Mask(ctx context.Context, text string) (string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.g.Masker()
	if m == nil || s.closed {
		return text, 0
	}
	out, entities, n := m.Mask([]byte(text))
	if n > 0 {
		_ = s.g.RecordMasked(ctx, entities, n)
	}
	return string(out), n
}

func (s *mcpLaneSession) Record(ctx context.Context, activity string, attrs map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	_ = s.g.RecordActivity(ctx, activity, attrs)
}

func (s *mcpLaneSession) Close(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	_ = s.g.Close(ctx)
	s.release()
}

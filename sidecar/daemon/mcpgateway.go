package daemon

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
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
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/session"
	telem "github.com/hoophq/hoop/sidecar/telemetry"
)

// The engine (mcpproxy) lives in the nested sidecar/mcpgateway module; this
// file is the contract, as ica.go is for libhoop-ica.

func isMCPGateway(lc ListenerConfig) bool {
	return inspect.Protocol(lc.Protocol) == inspect.MCP
}

// MCPGatewayConfig is a listener's mcp_gateway block: the upstream transport
// and mcpproxy's session policy. Tool allow and deny are guardrail rules of
// type mcp_tool.
type MCPGatewayConfig struct {
	Transport          string                     `json:"transport,omitempty"`
	BlockSampling      *bool                      `json:"block_sampling,omitempty"`
	BlockElicitation   *bool                      `json:"block_elicitation,omitempty"`
	OnRugPull          string                     `json:"on_rug_pull,omitempty"`
	MaxCallsPerSession int                        `json:"max_calls_per_session,omitempty"`
	MaxResultKB        int                        `json:"max_result_kb,omitempty"`
	ToolRateLimits     map[string]int             `json:"tool_rate_limits,omitempty"`
	Overrides          map[string]MCPToolOverride `json:"overrides,omitempty"`
	SQL                map[string]MCPSQLTool      `json:"sql,omitempty" cap:"mcp_sql" ui:"-"`
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

// columnMaskRules splits off the rules that name columns: an mcp lane masks
// strings without their keys, so it never matches them. ok is false when the
// rules do not parse; the plugin reports that.
func columnMaskRules(raw json.RawMessage) (kept [][]byte, columns []string, ok bool) {
	var rules []json.RawMessage
	if json.Unmarshal(raw, &rules) != nil {
		return nil, nil, false
	}
	for i, r := range rules {
		var rule struct {
			Name    string   `json:"name"`
			Columns []string `json:"columns"`
		}
		if json.Unmarshal(r, &rule) != nil || len(rule.Columns) == 0 {
			kept = append(kept, r)
			continue
		}
		label := fmt.Sprintf("mask rule %d", i+1)
		if rule.Name != "" {
			label = fmt.Sprintf("mask rule %q", rule.Name)
		}
		columns = append(columns, label)
	}
	return kept, columns, true
}

// validateMCPMasking refuses a column rule in the lane's own mask block.
func validateMCPMasking(lc ListenerConfig, name string) []string {
	if lc.Mask == nil || !lc.Mask.hasRules() {
		return nil
	}
	_, columns, _ := columnMaskRules(lc.Mask.Rules)
	problems := make([]string, 0, len(columns))
	for _, label := range columns {
		problems = append(problems, fmt.Sprintf(
			"%s: %s names columns, which an mcp listener never matches: it masks result strings without their keys. Name entities instead",
			name, label))
	}
	return problems
}

// mcpMask drops the inherited rules that name columns and labels them in
// skipped. They are not refused: 1.290 accepts them on an mcp lane.
func mcpMask(lc ListenerConfig, mc MaskConfig) (applied MaskConfig, skipped []string) {
	if !mc.hasRules() || (lc.Mask != nil && lc.Mask.hasRules()) {
		return mc, nil
	}
	kept, skipped, ok := columnMaskRules(mc.Rules)
	if !ok || len(skipped) == 0 {
		return mc, nil
	}
	mc.Rules = append(append([]byte("["), bytes.Join(kept, []byte(","))...), ']')
	return mc, skipped
}

func validateMCPGatewayLane(lc ListenerConfig, name string) []string {
	var problems []string
	u, err := url.Parse(lc.Upstream)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		problems = append(problems, name+": an mcp listener needs upstream as an http(s) URL of the MCP server, such as https://mcp.example.com/mcp")
	} else if u.Scheme == "http" && !loopbackHost(u.Hostname()) {
		problems = append(problems, name+": an mcp upstream is an https URL; plain http is accepted on loopback only")
	} else if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		problems = append(problems, name+": an mcp upstream URL carries no user, password, query or fragment; a credential does not belong in the config")
	}
	if lc.Network == "unix" {
		problems = append(problems, name+": an mcp listener listens on tcp; MCP clients do not connect to unix sockets")
	} else if host, _, err := net.SplitHostPort(lc.Listen); err != nil {
		problems = append(problems, name+": an mcp listener needs listen as host:port")
	} else if !loopbackHost(host) && lc.CallerToken == nil {
		problems = append(problems, name+": an mcp listener binds a non-loopback address only with caller_token, which authenticates its callers")
	}
	if lc.MaxConns > 0 {
		problems = append(problems, name+": max_conns does not apply to an mcp listener; MCP runs over HTTP requests, not long connections")
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
	return append(problems, validateMCPSQL(name, m.SQL)...)
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
	// Hidden reports whether an mcp_tool rule denies tool, so tools/list
	// leaves it out. It records nothing.
	Hidden(tool string) bool
	// ToolList judges the catalog the client sees; hidden names the tools
	// left out of it.
	ToolList(ctx context.Context, tools []MCPToolInfo, hidden []string) MCPVerdict
	Record(ctx context.Context, activity string, attrs map[string]string)
	// Mask rewrites one result string with the session's mask rules and
	// returns the entities it masked. An error withholds the result.
	Mask(text string) (out string, entities []string, count int, err error)
	// RecordMasked audits what Mask rewrote in one result. An error
	// withholds the result.
	RecordMasked(ctx context.Context, entities []string, count int) error
	Close(ctx context.Context)
}

// MCPCallerCheck admits a request by its Header, which the engine removes
// before mcpproxy reads the request.
type MCPCallerCheck struct {
	Header string
	// Resolve names the caller of credential, the header's values joined by
	// ", ". Key tells apart the callers one subject stands for; it never
	// leaves the process.
	Resolve func(ctx context.Context, credential string) (subject, key string, err error)
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
	// DownstreamTLS is the certificate MCP clients are served with; nil
	// serves plain HTTP.
	DownstreamTLS *tls.Config
	// Caller, when set, admits only the requests it resolves.
	Caller *MCPCallerCheck
	// SessionIdleTimeout is idle_timeout_sec; zero keeps mcpproxy's 30 minutes.
	SessionIdleTimeout time.Duration
	// MaskResults is set when the lane has mask rules: tool and resource
	// results then pass MCPLaneSession.Mask. Fixed when the lane is built.
	MaskResults bool
	// Telemetry is the lane's recorder; nil without a telemetry block.
	Telemetry telem.LaneRecorder
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
	// Stats counts MCP sessions, and the messages the lane refused.
	Stats() (active, total, denied int64)
	Notes() []string
	// EndSessions closes every open MCP session: the next message on one is
	// refused, so the client initializes again.
	EndSessions(reason string)
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
	downstream, err := lc.DownstreamTLS.BuildDownstreamTLS()
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", ln.name, err)
	}
	caller, err := mcpCallerCheck(ln)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", ln.name, err)
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
		DownstreamTLS:      downstream,
		Caller:             caller,
		SessionIdleTimeout: time.Duration(lc.IdleTimeoutSec) * time.Second,
		MaskResults:        ln.masker != nil,
		Telemetry:          ln.telemetry,
		Log:                laneLog,
	}
	if lc.MCPGateway != nil {
		resolved.Policy = *lc.MCPGateway
	}
	if sink != nil {
		failOnAuditError := ac.failOnAuditError()
		resolved.Open = func(ctx context.Context, caller MCPCaller) (MCPLaneSession, error) {
			return openMCPLaneSession(ctx, ln, rules, sink, failOnAuditError, caller, laneLog)
		}
	}
	srv, err := build(resolved)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", ln.name, err)
	}
	return srv, rules, nil
}

// mcpLaneRules is the lane rule swapper. A session reads its rules once,
// when its gate opens, and an MCP session can live for hours, so a swap ends
// the open sessions and the client initializes again under the new rules.
type mcpLaneRules struct {
	*liveRules
	srv MCPGatewayServer
}

func (r mcpLaneRules) swapLane(ln lane, drained func()) {
	r.liveRules.swapLane(ln, drained)
	r.srv.EndSessions("rules reloaded")
}

type mcpLaneSession struct {
	mu      sync.Mutex
	g       *gate.Gate
	release func()
	mcpSID  string
	hide    *policy.Rules
	sql     map[string]MCPSQLTool
	log     *slog.Logger
	closed  bool
}

func openMCPLaneSession(ctx context.Context, ln lane, rules *liveRules, sink audit.Sink, failOnAuditError bool, caller MCPCaller, log *slog.Logger) (MCPLaneSession, error) {
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
	if err := g.Start(ctx); err != nil {
		if failOnAuditError {
			_ = g.Close(ctx)
			release()
			return nil, fmt.Errorf("audit trail unavailable: %w", err)
		}
		log.Warn("mcp session start not recorded", "error", err)
	}
	ls := &mcpLaneSession{g: g, release: release, mcpSID: caller.MCPSessionID, hide: live.mcpHide, log: log}
	if ln.cfg.MCPGateway != nil {
		ls.sql = ln.cfg.MCPGateway.SQL
	}
	return ls, nil
}

func (s *mcpLaneSession) ID() string { return string(s.g.Session().ID) }

func (s *mcpLaneSession) ToolCall(ctx context.Context, tool string, args []byte) MCPVerdict {
	stmt := inspect.Statement{
		Protocol:  inspect.MCP,
		Direction: inspect.FromClient,
		Text:      tool + "\n" + string(args),
		Operation: inspect.OpCall,
		Tables:    []string{strings.ToLower(tool)},
		Metadata: map[string]string{
			inspect.MetadataMCPMethod: "tools/call", inspect.MetadataMCPTool: tool, inspect.MetadataMCPSession: s.mcpSID,
		},
	}
	t, isSQL, err := mcpSQLFor(s.sql, tool)
	switch {
	case err != nil:
		return s.refuseSQL(ctx, tool, "", err)
	case isSQL:
		return s.sqlCall(ctx, stmt, t, args)
	}
	return s.evaluate(ctx, stmt)
}

func (s *mcpLaneSession) Hidden(tool string) bool {
	if s.hide == nil {
		return false
	}
	return s.hide.Evaluate(inspect.Statement{
		Protocol:  inspect.MCP,
		Direction: inspect.FromClient,
		Operation: inspect.OpCall,
		Metadata:  map[string]string{inspect.MetadataMCPTool: tool},
	}).Denied
}

func (s *mcpLaneSession) ToolList(ctx context.Context, tools []MCPToolInfo, hidden []string) MCPVerdict {
	var b strings.Builder
	for _, t := range tools {
		fmt.Fprintf(&b, "%s: %s\n", t.Name, t.Description)
	}
	stmt := inspect.Statement{
		Protocol:  inspect.MCP,
		Direction: inspect.FromServer,
		Text:      b.String(),
		Operation: inspect.OpShow,
		Metadata:  map[string]string{inspect.MetadataMCPMethod: "tools/list", inspect.MetadataMCPSession: s.mcpSID},
	}
	if len(hidden) > 0 {
		stmt.Metadata[inspect.MetadataMCPToolsHidden] = strings.Join(hidden, ",")
	}
	return s.evaluate(ctx, stmt)
}

// mcpHideRules returns the rules that hide tools from an mcp lane's
// tools/list: its mcp_tool rules that deny outright. A deferring rule leaves
// the decision to OPA, so it hides nothing.
func mcpHideRules(rules []policy.Rule) (*policy.Rules, error) {
	var hide []policy.Rule
	for _, r := range rules {
		if r.Type == policy.MatchMCPTool && r.Action != policy.ActionDefer {
			hide = append(hide, r)
		}
	}
	if len(hide) == 0 {
		return nil, nil
	}
	return policy.NewRules(hide)
}

// evaluate judges stmts in order under one lock and stops at the first deny.
func (s *mcpLaneSession) evaluate(ctx context.Context, stmts ...inspect.Statement) MCPVerdict {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return MCPVerdict{Message: "session closed"}
	}
	var v MCPVerdict
	for _, stmt := range stmts {
		d := s.g.EvaluateStatement(ctx, stmt)
		if v = (MCPVerdict{Allowed: d.Allowed, Message: d.Message, Rule: d.Rule}); !v.Allowed {
			break
		}
	}
	return v
}

func (s *mcpLaneSession) Record(ctx context.Context, activity string, attrs map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if err := s.g.RecordActivity(ctx, activity, attrs); err != nil {
		s.log.Warn("mcp activity not recorded", "activity", activity, "error", err)
	}
}

var errMCPSessionClosed = errors.New("the sidecar session is closed")

func (s *mcpLaneSession) Mask(text string) (string, []string, int, error) {
	s.mu.Lock()
	closed, m := s.closed, s.g.Masker()
	s.mu.Unlock()
	switch {
	case closed:
		return "", nil, 0, errMCPSessionClosed
	case m == nil:
		return "", nil, 0, errors.New("the sidecar session has no masker")
	}
	out, entities, n := m.Mask([]byte(text))
	return string(out), entities, n, nil
}

func (s *mcpLaneSession) RecordMasked(ctx context.Context, entities []string, count int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errMCPSessionClosed
	}
	return s.g.RecordMasked(ctx, entities, count)
}

func (s *mcpLaneSession) Close(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	if err := s.g.Close(ctx); err != nil {
		s.log.Warn("mcp session close reported an error", "error", err)
	}
	s.release()
}

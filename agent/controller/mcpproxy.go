package controller

// Agent side of the protocol-aware MCP path (ADR-0004).
//
// Structurally this mirrors httpproxy.go — same per-(session, connection)
// packetQueue, same connStore lifecycle, same chunked response writer — but
// instead of handing the bytes to libhoop's byte relay it builds an mcpproxy
// gateway and lets libhoop's adapter feed it parsed HTTP requests.
//
// The queue is not optional here. A held tool call blocks for as long as a
// human takes to approve it; processing inline would park the agent's recv
// loop and deadlock the very cancellation meant to abort it.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/mcpproxy/audit"
	mcpbackend "github.com/hoophq/mcpproxy/backend"
	"github.com/hoophq/mcpproxy/checks"
	mcpconfig "github.com/hoophq/mcpproxy/config"
	mcpgateway "github.com/hoophq/mcpproxy/gateway"
	"github.com/hoophq/mcpproxy/inspect"
	"libhoop"
	"libhoop/agent/mcpadapter"
)

// mcpResponseChunkSize bounds each gRPC packet carrying a response back to the
// gateway, mirroring httpProxyResponseChunkSize. Tool results can be large
// (a query returning rows); anything above MaxRecvMsgSize is a stream-fatal
// error that would drop every session on this agent.
const mcpResponseChunkSize = 1024 * 1024 * 4

// processMCPProxyWriteServer runs on the agent's recv loop. See
// processHttpProxyWriteServer for why the work is queued rather than handled
// inline; for MCP the argument is stronger, because a tool call held for human
// review blocks for minutes by design.
func (a *Agent) processMCPProxyWriteServer(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	clientConnectionID := string(pkt.Spec[pb.SpecClientConnectionID])
	if clientConnectionID == "" {
		log.With("sid", sessionID).Info("connection id not found in packet specification")
		a.sendClientSessionClose(sessionID, "mcp proxy connection id not found")
		return
	}
	queueKey := fmt.Sprintf("%s:%s", sessionID, clientConnectionID)
	obj, _ := a.mcpProxyQueues.LoadOrStore(queueKey, &packetQueue{})
	queue := obj.(*packetQueue)
	startWorker, overflow := queue.push(pkt)
	if overflow {
		// The drain worker is wedged — most likely on a held tool call — while
		// the gateway keeps streaming. Fail this connection rather than buffer
		// unbounded payload; sibling connections on the session stay alive.
		log.With("sid", sessionID, "conn", clientConnectionID).
			Errorf("mcp proxy packet queue overflow, closing connection")
		a.sendClientTCPConnectionClose(sessionID, clientConnectionID)
		return
	}
	if startWorker {
		go queue.drain(a.handleMCPProxyWrite)
	}
}

// handleMCPProxyWrite performs the blocking request handling. It must only be
// invoked from packetQueue.drain, which serializes calls per
// (sessionID, connectionID).
func (a *Agent) handleMCPProxyWrite(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	clientConnectionID := string(pkt.Spec[pb.SpecClientConnectionID])
	log := log.With("sid", sessionID, "conn", clientConnectionID)

	connParams := a.connectionParams(sessionID)
	if connParams == nil {
		log.Infof("connection params not found")
		a.sendClientSessionClose(sessionID, "connection params not found, contact the administrator")
		return
	}

	connKey := fmt.Sprintf("%s:%s", sessionID, clientConnectionID)
	if proxy, ok := a.connStore.Get(connKey).(io.WriteCloser); ok {
		if _, err := proxy.Write(pkt.Payload); err != nil {
			log.Infof("failed writing packet, err=%v", err)
			_ = proxy.Close()
			a.connStore.Del(connKey)
			if isGuardrailsError(err) {
				log.Infof("guardrails validation failed, closing session: %v", err)
				a.sendClientSessionCloseFromError(sessionID, err)
				return
			}
			a.sendClientTCPConnectionClose(sessionID, clientConnectionID)
		}
		return
	}

	connenv, err := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionType(connParams.ConnectionType))
	if err != nil {
		log.Infof("missing connection credentials in memory, err=%v", err)
		a.sendClientSessionClose(sessionID, "credentials are empty, contact the administrator")
		return
	}

	opts := mcpProxyOpts(connenv, connParams, sessionID, clientConnectionID)

	gw, err := a.mcpGatewayFor(sessionID, connenv, connParams, opts, pkt.Spec)
	if err != nil {
		log.Infof("failed building mcp gateway, err=%v", err)
		a.sendClientSessionClose(sessionID, fmt.Sprintf("failed starting mcp proxy: %v", err))
		return
	}

	streamClient := pb.NewStreamWriter(a.client, pbclient.MCPProxyConnectionWrite, pkt.Spec)
	chunkedClient := pb.NewChunkedWriter(streamClient, mcpResponseChunkSize)

	// The adapter is per HTTP request; the gateway behind it is not. Closing
	// the gateway here would destroy the MCP session state every later
	// request depends on, so the adapter's onClose is a no-op and the
	// gateway is torn down by closeMCPProxyConnections at session cleanup.
	proxy, err := libhoop.NewMCPProxy(context.Background(), chunkedClient, gw.Handler(), func() {}, opts)
	if err != nil {
		log.Infof("failed starting mcp proxy, err=%v", err)
		a.sendClientSessionClose(sessionID, fmt.Sprintf("failed starting mcp proxy: %v", err))
		return
	}

	proxy.Run(func(exitCode int, errMsg string) {
		log.Infof("mcp proxy exited, code=%v, msg=%v", exitCode, errMsg)
		a.connStore.Del(connKey)
		a.sendClientTCPConnectionClose(sessionID, clientConnectionID)
	})
	a.connStore.Set(connKey, proxy)

	log.Infof("started mcp proxy, transport=%v", opts["mcp_transport"])

	if _, err := proxy.Write(pkt.Payload); err != nil {
		log.Infof("failed writing first packet, err=%v", err)
		_ = proxy.Close()
		a.connStore.Del(connKey)
		if isGuardrailsError(err) {
			a.sendClientSessionCloseFromError(sessionID, err)
			return
		}
		a.sendClientTCPConnectionClose(sessionID, clientConnectionID)
	}
}

// mcpGatewayHolder memoises one gateway per hoop session. sync.Once rather
// than a plain LoadOrStore because building the gateway is expensive and, for
// the client-stdio transport, has side effects (it registers a backend
// registry entry); two concurrent HTTP requests on a fresh session must not
// both run it.
type mcpGatewayHolder struct {
	once sync.Once
	gw   *mcpgateway.Gateway
	err  error
}

// mcpGatewayFor returns the session's mcpproxy gateway, building it once.
//
// One gateway per hoop SESSION, not per HTTP request. An MCP session lives
// inside exactly one gateway: the gateway mints an Mcp-Session-Id on
// `initialize` and resolves every later message against its own session map,
// answering 404 on a miss. The hoop gateway's HTTP listener mints a fresh
// connection id per inbound request (that id is its response-routing key), so
// building a gateway per connection id meant the client's second message
// reached a gateway that had never seen its session — `initialize` worked and
// every tools/list and tools/call after it failed, while each request leaked
// another gateway and another stdio child.
//
// Sharing is safe: gateway.Options is frozen at construction and never
// mutated here, and the gateway guards its own session map, which is exactly
// the concurrency the standalone daemon runs under.
func (a *Agent) mcpGatewayFor(
	sessionID string,
	connenv *connEnv,
	connParams *pb.AgentConnectionParams,
	opts map[string]string,
	spec map[string][]byte,
) (*mcpgateway.Gateway, error) {
	obj, _ := a.mcpGateways.LoadOrStore(sessionID, &mcpGatewayHolder{})
	holder := obj.(*mcpGatewayHolder)
	holder.once.Do(func() {
		// Guardrails and masking come from the same redactor configuration
		// every other protocol uses; the gateway calls them at the points
		// where MCP has free text (tool arguments, descriptions, results).
		hooks, err := libhoop.NewMCPHooks(opts)
		if err != nil {
			holder.err = fmt.Errorf("failed configuring data protection: %v", err)
			return
		}
		holder.gw, holder.err = a.buildMCPGateway(
			connenv, connParams, sessionID, hooks, a.mcpAuditSink(sessionID, spec))
	})
	if holder.err != nil {
		// Leave nothing memoised: a transient failure (e.g. the client's
		// stdio child refusing to spawn) must not poison the session.
		a.mcpGateways.Delete(sessionID)
		return nil, holder.err
	}
	return holder.gw, nil
}

// buildMCPGateway assembles the mcpproxy gateway for one MCP connection.
//
// One gateway per hoop session (see mcpGatewayFor): mcpproxy freezes
// gateway.Options at construction, so the options map built here is never
// mutated afterwards.
func (a *Agent) buildMCPGateway(
	connenv *connEnv,
	connParams *pb.AgentConnectionParams,
	sessionID string,
	hooks mcpadapter.Hooks,
	sink audit.Sink,
) (*mcpgateway.Gateway, error) {
	backendCfg := mcpconfig.Backend{
		Transport: connenv.mcpTransport,
		Command:   connParams.CmdList,
		Env:       connenv.mcpEnv,
		URL:       connenv.httpProxyRemoteURL,
		Headers:   mcpBackendHeaders(connenv.httpProxyHeaders),
		Auth:      connenv.mcpAuth,
	}

	mcpHooks := inspect.Hooks{}
	if hooks.GuardInput != nil {
		mcpHooks.GuardInput = func(ctx context.Context, dir inspect.Direction, text string) error {
			direction := "input"
			if dir == inspect.S2C {
				direction = "output"
			}
			return hooks.GuardInput(ctx, direction, text)
		}
	}
	if hooks.Redact != nil {
		mcpHooks.Redact = hooks.Redact
	}

	policy := connenv.mcpPolicy()

	// Held verdicts degrade to deny while the review path is unimplemented:
	// an approval-matched tool must never execute unreviewed (fail closed).
	pipeline := checks.Assemble(policy, mcpHooks, sink, false)

	name := connParams.ConnectionName
	if name == "" {
		name = "mcp"
	}

	// mcpTransportClientStdio runs the MCP server on the connecting user's
	// machine instead of this agent, so the backend is a tunnel down the
	// client stream rather than a local child process. Everything after this
	// point — pipeline, policy, masking, audit — is identical: the gateway
	// only ever sees a backend.Backend.
	factory := mcpbackend.NewFactory(name, backendCfg, nil)
	if connenv.mcpTransport == mcpTransportClientStdio {
		factory = a.clientStdioFactory(name, sessionID)
	}

	return mcpgateway.New(mcpgateway.Options{
		Backends: map[string]mcpbackend.Factory{
			name: factory,
		},
		Pipeline: pipeline,
		Sink:     sink,
		Resolver: func(*http.Request) (inspect.Identity, error) {
			// The gateway authenticated the caller before the bytes ever
			// reached this agent; re-authenticating here would be a second
			// identity system disagreeing with the first.
			return inspect.Identity{
				Subject: connParams.UserID,
				Email:   connParams.UserEmail,
				Claims: map[string]any{
					"hoop_sid":   sessionID,
					"connection": connParams.ConnectionName,
				},
			}, nil
		},
		// RequestTimeout bounds a held tool call, not just a backend round
		// trip, so it must cover a human review rather than a network hop.
		RequestTimeout: 30 * time.Minute,
	})
}

// mcpProxyOpts assembles the flat opts map libhoop consumes, mirroring the
// httpproxy path so redaction, guardrails and masking read identical keys.
func mcpProxyOpts(connenv *connEnv, connParams *pb.AgentConnectionParams, sessionID, connectionID string) map[string]string {
	var guardRailRules string
	if connParams.GuardRailRules != nil {
		guardRailRules = string(connParams.GuardRailRules)
	}
	var dataMaskingEntityTypesData string
	if connParams.DataMaskingEntityTypesData != nil {
		dataMaskingEntityTypesData = string(connParams.DataMaskingEntityTypesData)
	}
	return map[string]string{
		"sid":                       sessionID,
		"connection_id":             connectionID,
		"mcp_transport":             connenv.mcpTransport,
		"remote_url":                connenv.httpProxyRemoteURL,
		"insecure":                  strconv.FormatBool(connenv.insecure),
		"dlp_provider":              connParams.DlpProvider,
		"dlp_mode":                  connParams.DlpMode,
		"dlp_masking_character":     "#",
		"mspresidio_anonymizer_url": connParams.DlpPresidioAnonymizerURL,
		"mspresidio_analyzer_url":   connParams.DlpPresidioAnalyzerURL,
		"guard_rail_rules":          guardRailRules,
		"data_masking_entity_data":  dataMaskingEntityTypesData,
	}
}

// closeMCPProxyConnections tears down every MCP resource belonging to a
// session. Called from session cleanup so a disconnect never orphans a stdio
// child, a tunnelled backend, or a dispatch slot.
func (a *Agent) closeMCPProxyConnections(sessionID string) {
	prefix := sessionID + ":"
	a.mcpProxyQueues.Range(func(key, _ any) bool {
		k, _ := key.(string)
		if strings.HasPrefix(k, prefix) {
			a.mcpProxyQueues.Delete(key)
		}
		return true
	})
	// The per-request adapters are closed by the connStore loop in
	// sessionCleanup, but the gateway outlives them by design (it holds the
	// MCP session state they share), so it is closed here. gw.Close shuts
	// down every MCP session, which closes every backend: a local stdio
	// child is signalled and reaped, a tunnelled one releases its waiters.
	if obj, ok := a.mcpGateways.LoadAndDelete(sessionID); ok {
		if holder, _ := obj.(*mcpGatewayHolder); holder != nil && holder.gw != nil {
			holder.gw.Close()
		}
	}
	a.closeClientStdioBackends(sessionID)
}

// ---- env parsing helpers ---------------------------------------------------

// splitGlobList parses a comma-separated tool glob list. Empty entries are
// dropped so a trailing comma in the UI does not become a glob matching "".
func splitGlobList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// parseOptionalBool distinguishes "unset" from "false". The MCP policy treats
// nil as "use the secure default" (block), so an absent setting must not be
// read as an explicit opt-out.
func parseOptionalBool(v string) *bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "":
		return nil
	case "true", "1", "yes", "on":
		t := true
		return &t
	default:
		f := false
		return &f
	}
}

// parseIntOrZero returns 0 (the library's "unlimited") for anything unparseable
// rather than failing the session: a malformed budget should not deny access,
// and the value is operator-supplied through a numeric UI field.
func parseIntOrZero(v string) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// stripPrefixKeys rewrites map keys by removing prefix, case-insensitively.
// MCPENV_FIGMA_TOKEN becomes FIGMA_TOKEN in the child's environment.
func stripPrefixKeys(in map[string]string, prefix string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if len(k) >= len(prefix) && strings.EqualFold(k[:len(prefix)], prefix) {
			out[k[len(prefix):]] = v
			continue
		}
		out[k] = v
	}
	return out
}

// mcpBackendHeaders turns the connection's HEADER_* env vars into real HTTP
// header names for the MCP backend.
//
// The env store preserves the original key, so the map arrives as
// {HEADER_AUTHORIZATION: "Bearer x"}; mcpproxy's remote and SSE backends call
// req.Header.Set verbatim. Without stripping, the upstream receives a bogus
// "HEADER_AUTHORIZATION" and no "Authorization" at all, breaking every static
// and frozen-token OAuth backend.
//
// Only the prefix is removed. The rest of the name is passed through byte for
// byte because providers disagree on the separator and getting it wrong is an
// unauthenticated request, not a warning: context7 requires
// CONTEXT7_API_KEY while google-maps requires X-Goog-Api-Key. Translating
// underscores to hyphens (as libhoop's httpproxy parseHeaderOpts does for its
// own provider set) would silently break the former.
func mcpBackendHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range stripPrefixKeys(in, "header_") {
		out[k] = strings.TrimSpace(v)
	}
	return out
}

// validateMCPProxyEnv rejects a misconfigured MCP connection at parse time.
//
// Without this, an unknown transport or a missing REMOTE_URL surfaces from
// mcpproxy's backend factory on the first tool call, as an opaque session
// close long after the admin saved the connection. The stdio COMMAND is not
// checked here because it travels in AgentConnectionParams, not the env vars;
// buildMCPGateway covers it.
func validateMCPProxyEnv(env *connEnv) error {
	switch env.mcpTransport {
	case "stdio", mcpTransportClientStdio:
	case "streamable-http", "sse":
		if env.httpProxyRemoteURL == "" {
			return fmt.Errorf("missing required environment for mcpproxy connection [REMOTE_URL]")
		}
		if _, err := url.Parse(env.httpProxyRemoteURL); err != nil {
			return fmt.Errorf("failed parsing REMOTE_URL env, reason=%v", err)
		}
	case "":
		return fmt.Errorf("missing required environment for mcpproxy connection [MCP_TRANSPORT]")
	default:
		return fmt.Errorf("invalid MCP_TRANSPORT %q, accept only: %v",
			env.mcpTransport, []string{"stdio", mcpTransportClientStdio, "streamable-http", "sse"})
	}

	// The agent supplies no outbound TokenSource: hoop brokers OAuth itself
	// and freezes the result into HEADER_AUTHORIZATION, which the static path
	// carries. Accepting "oauth" or "passthrough" here would silently produce
	// an unauthenticated backend, so they are refused until the token-store
	// seam is wired (ADR-0004 phase 6).
	switch env.mcpAuth {
	case "", "none", "static":
	default:
		return fmt.Errorf("unsupported MCP_AUTH %q, accept only: %v",
			env.mcpAuth, []string{"none", "static"})
	}

	switch env.mcpOnRugPull {
	case "", "kill", "alert":
	default:
		return fmt.Errorf("invalid MCP_ON_RUG_PULL %q, accept only: %v",
			env.mcpOnRugPull, []string{"kill", "alert"})
	}
	return nil
}

// mcpPolicy converts the connection's env-var settings into the gateway's
// inspection policy.
func (c *connEnv) mcpPolicy() mcpconfig.Policy {
	return mcpconfig.Policy{
		AllowedTools:     c.mcpAllowedTools,
		DeniedTools:      c.mcpDeniedTools,
		ApprovalTools:    c.mcpApprovalTools,
		BlockSampling:    c.mcpBlockSampling,
		BlockElicitation: c.mcpBlockElicitation,
		OnRugPull:        c.mcpOnRugPull,
		MaxCallsPerSess:  c.mcpMaxCalls,
		MaxResultKB:      c.mcpMaxResultKB,
	}
}

// ---- audit sink -------------------------------------------------------------

// mcpAuditSink returns the sink that turns MCP protocol events into hoop
// session events.
//
// Each event is written back to the gateway on the client stream as a
// structured line, so the existing session recorder stores it alongside the
// protocol bytes and the session viewer renders a tool-call timeline rather
// than HTTP blobs. Emit must not block the inspection pipeline, so a failed
// write is logged and dropped rather than retried.
func (a *Agent) mcpAuditSink(sessionID string, spec map[string][]byte) audit.Sink {
	return &mcpEventSink{agent: a, sid: sessionID, spec: spec}
}

type mcpEventSink struct {
	agent *Agent
	sid   string
	spec  map[string][]byte
}

func (s *mcpEventSink) Emit(_ context.Context, ev audit.Event) {
	line, err := json.Marshal(ev)
	if err != nil {
		log.With("sid", s.sid).Warnf("failed encoding mcp audit event: %v", err)
		return
	}
	pkt := &pb.Packet{
		Type:    pbclient.MCPProxyConnectionWrite,
		Spec:    map[string][]byte{},
		Payload: append(line, '\n'),
	}
	for k, v := range s.spec {
		pkt.Spec[k] = v
	}
	// Tag the packet so the gateway records it as a protocol event rather than
	// forwarding it to the MCP client as response bytes.
	pkt.Spec[pb.SpecMCPEventKey] = []byte("1")
	if err := s.agent.client.Send(pkt); err != nil {
		log.With("sid", s.sid).Warnf("failed sending mcp audit event: %v", err)
	}
}

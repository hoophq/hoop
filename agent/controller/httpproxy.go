package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"libhoop"
	libhoopaianalyzer "libhoop/aianalyzer"
	redactortypes "libhoop/redactor/types"
	"strings"

	"github.com/hoophq/hoop/agent/controller/featureflagstate"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

// httpProxyClientAuthorizationFlag gates per-user upstream identity on
// httpproxy connections. When on, a connection with
// ALLOW_CLIENT_AUTHORIZATION=true lets the client supply the upstream
// Authorization credential via the X-Hoop-Upstream-Authorization header.
// When off, the agent leaves request headers untouched.
const httpProxyClientAuthorizationFlag = "experimental.httpproxy_client_authorization"

// httpProxyResponseChunkSize bounds the size of each gRPC packet emitted for an
// HTTP proxy response. libhoop fully buffers a response and writes it to the
// client stream in a single Write; without chunking, a large response (e.g. a
// big ClickHouse result) becomes one oversized gRPC message and is rejected with
// a ResourceExhausted error. The value must stay safely below
// common/grpc.MaxRecvMsgSize.
const httpProxyResponseChunkSize = 1024 * 1024 * 4 // 4 MiB

// processHttpProxyWriteServer runs on the agent's packet recv loop. Handling a
// proxied HTTP request is blocking: libhoop's Write performs the upstream
// round-trip and does not return until response headers arrive, which for a
// long-context LLM request can take minutes of time-to-first-byte. Processing
// it inline would park the recv loop — stalling every other session on this
// agent and, critically, preventing the TCPConnectionClose the gateway sends
// when it abandons this same request from ever being dispatched: the
// cancellation would deadlock against the request it is meant to cancel.
//
// Each packet is therefore appended to a per-(session, connection) FIFO
// drained by a single worker goroutine. The recv loop never blocks, packets
// for one connection keep their arrival order (required for chunked request
// bodies and WebSocket frames), and connections proceed independently.
func (a *Agent) processHttpProxyWriteServer(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	clientConnectionID := string(pkt.Spec[pb.SpecClientConnectionID])
	if clientConnectionID == "" {
		log.With("sid", sessionID).Info("connection not found in packet specfication")
		a.sendClientSessionClose(sessionID, "http proxy connection id not found")
		return
	}
	queueKey := fmt.Sprintf("%s:%s", sessionID, clientConnectionID)
	obj, _ := a.httpProxyQueues.LoadOrStore(queueKey, &packetQueue{})
	queue := obj.(*packetQueue)
	startWorker, overflow := queue.push(pkt)
	if overflow {
		// The drain worker is wedged (upstream not answering) while the
		// gateway keeps streaming request data. Fail THIS connection
		// explicitly instead of buffering unbounded payload in agent
		// memory — sibling connections multiplexed on the same session
		// stay alive, consistent with the handler's other per-connection
		// failure paths. The queue entry deliberately stays in place
		// (see the httpProxyQueues field comment) so a late packet can't
		// spawn a second worker; it is dropped with the session.
		log.With("sid", sessionID, "conn", clientConnectionID).
			Errorf("http proxy packet queue overflow, closing connection")
		a.sendClientTCPConnectionClose(sessionID, clientConnectionID)
		return
	}
	if startWorker {
		go queue.drain(a.handleHttpProxyWrite)
	}
}

// handleHttpProxyWrite performs the actual (blocking) request handling. It
// must only be invoked from packetQueue.drain, which serializes
// calls per (sessionID, connectionID).
func (a *Agent) handleHttpProxyWrite(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	clientConnectionID := string(pkt.Spec[pb.SpecClientConnectionID])
	proxyBaseURL := string(pkt.Spec[pb.SpecHttpProxyBaseUrl])
	log := log.With("sid", sessionID, "conn", clientConnectionID)
	connParams := a.connectionParams(sessionID)
	if connParams == nil {
		log.Infof("connection params not found")
		a.sendClientSessionClose(sessionID, "connection params not found, contact the administrator")
		return
	}
	clientConnectionIDKey := fmt.Sprintf("%s:%s", sessionID, string(clientConnectionID))
	if httpServer, ok := a.connStore.Get(clientConnectionIDKey).(io.WriteCloser); ok {
		if _, err := httpServer.Write(pkt.Payload); err != nil {
			// ErrWebSocketMode is not an error - it signals WebSocket mode was activated
			if isWebSocketModeError(err) {
				log.Debugf("websocket mode activated, continuing")
				return
			}
			log.Infof("failed writing packet, err=%v", err)
			_ = httpServer.Close()
			a.connStore.Del(clientConnectionIDKey)
			// Check if this is a guardrails error - these should close the session with the error message
			if isGuardrailsError(err) {
				log.Infof("guardrails validation failed, closing session: %v", err)
				a.sendClientSessionCloseFromError(sessionID, err)
				return
			}
			// If we have and multiple websocket connection open and we kill one of them
			// then we should not close the entire session, just this connection only.
			// the proxy session will be closed when the hoop connect <role> is closed.
			a.sendClientTCPConnectionClose(sessionID, clientConnectionID)
		}
		return
	}
	httpStreamClient := pb.NewStreamWriter(a.client, pbclient.HttpProxyConnectionWrite, pkt.Spec)
	connenv, err := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionType(connParams.ConnectionType))
	if err != nil {
		log.Infof("missing connection credentials in memory, err=%v", err)
		a.sendClientSessionClose(sessionID, "credentials are empty, contact the administrator")
		return
	}

	log.Infof("starting http proxy connection at %v", connenv.httpProxyRemoteURL)

	var guardRailRules string
	if connParams.GuardRailRules != nil {
		guardRailRules = string(connParams.GuardRailRules)
	}
	var dataMaskingEntityTypesData string
	if connParams.DataMaskingEntityTypesData != nil {
		dataMaskingEntityTypesData = string(connParams.DataMaskingEntityTypesData)
	}
	connenv.httpProxyHeaders["remote_url"] = connenv.httpProxyRemoteURL
	connenv.httpProxyHeaders["connection_id"] = clientConnectionID
	connenv.httpProxyHeaders["sid"] = sessionID
	connenv.httpProxyHeaders["insecure"] = fmt.Sprintf("%v", connenv.insecure)
	connenv.httpProxyHeaders["proxy_base_url"] = proxyBaseURL

	connenv.httpProxyHeaders["dlp_provider"] = connParams.DlpProvider
	connenv.httpProxyHeaders["dlp_mode"] = connParams.DlpMode
	connenv.httpProxyHeaders["dlp_masking_character"] = "#"
	connenv.httpProxyHeaders["mspresidio_anonymizer_url"] = connParams.DlpPresidioAnonymizerURL
	connenv.httpProxyHeaders["mspresidio_analyzer_url"] = connParams.DlpPresidioAnalyzerURL
	connenv.httpProxyHeaders["guard_rail_rules"] = guardRailRules
	connenv.httpProxyHeaders["data_masking_entity_data"] = dataMaskingEntityTypesData

	// Per-user upstream identity: the connection opted in via
	// ALLOW_CLIENT_AUTHORIZATION=true, so libhoop promotes a client-supplied
	// X-Hoop-Upstream-Authorization header to the upstream Authorization
	// header (superseding any header_* configured on the connection).
	if connenv.httpProxyAllowClientAuth && featureflagstate.IsEnabled(httpProxyClientAuthorizationFlag) {
		connenv.httpProxyHeaders["allow_client_authorization"] = "true"
	}

	// add default values for kubernetes type
	if connParams.ConnectionType == pb.ConnectionTypeKubernetes.String() {
		connenv.httpProxyHeaders["remote_url"] = connenv.kubernetesClusterURL
		if !strings.HasPrefix(connenv.kubernetesToken, "Bearer ") {
			connenv.kubernetesToken = fmt.Sprintf("Bearer %s", connenv.kubernetesToken)
		}
		connenv.httpProxyHeaders["HEADER_AUTHORIZATION"] = connenv.kubernetesToken
		connenv.httpProxyHeaders["insecure"] = fmt.Sprintf("%v", connenv.kubernetesInsecureSkipVerify)
	}

	// Google Vertex AI federation for claude-code connections: when the
	// connection carries a service-account key and the feature is enabled,
	// mint a short-lived OAuth bearer and inject it as the upstream
	// Authorization header so Claude Code (running in Vertex mode) reaches
	// Vertex as the connection's GCP identity. The bearer supersedes any
	// static API-key header configured on the connection.
	if connenv.gcpServiceAccountJSON != "" && featureflagstate.IsEnabled(claudeCodeVertexFlag) {
		token, err := a.gcpVertexBearer(sessionID, connenv.gcpServiceAccountJSON)
		if err != nil {
			log.Infof("failed obtaining gcp vertex credentials, err=%v", err)
			a.sendClientSessionClose(sessionID, fmt.Sprintf("failed obtaining GCP credentials: %v", err))
			return
		}
		removeHeader(connenv.httpProxyHeaders, "HEADER_X_API_KEY")
		connenv.httpProxyHeaders["HEADER_AUTHORIZATION"] = "Bearer " + token
	}

	// Build the per-connection AI session analyzer from the gateway-resolved
	// config (opt-in). The engine lives in common/aianalyzer (shared with the
	// gateway's exec path); the agent wraps it in an adapter that satisfies
	// libhoop's injected Analyzer contract. When no config is present, or it is
	// invalid, the proxy receives a nil analyzer and skips analysis (fail-open).
	var analyzer libhoopaianalyzer.Analyzer
	if cfg := connParams.AISessionAnalyzer; cfg != nil {
		a, aerr := newHTTPAnalyzer(cfg, clientConnectionID)
		if aerr != nil {
			log.Warnf("failed building ai session analyzer, forwarding requests without analysis: %v", aerr)
		} else {
			analyzer = a
		}
	}

	// Wrap the stream writer so an oversized buffered response is split into
	// multiple sub-limit gRPC packets instead of a single message that exceeds
	// MaxRecvMsgSize. The client reassembles the byte stream in order.
	chunkedClient := pb.NewChunkedWriter(httpStreamClient, httpProxyResponseChunkSize)
	httpProxy, err := libhoop.NewHttpProxy(context.Background(), chunkedClient, analyzer, connenv.httpProxyHeaders)
	if err != nil {
		log.Infof("failed connecting to %v, err=%v", connenv.host, err)
		a.sendClientSessionClose(sessionID, fmt.Sprintf("failed connecting to internal service, reason=%v", err))
		return
	}

	// Register the proxy before the first Write. Write blocks for the whole
	// upstream round-trip, and while it is in flight the gateway may abandon
	// the request (TCPConnectionClose) or end the session (SessionClose);
	// those handlers cancel in-flight requests by looking the proxy up in the
	// connStore and closing it, which cancels the proxy context and aborts the
	// upstream call. Registering after Write (the previous behavior) made an
	// in-flight first request — i.e. every plain HTTP request — uncancellable.
	a.connStore.Set(clientConnectionIDKey, httpProxy)

	// write the first packet when establishing the connection
	if _, err := httpProxy.Write(pkt.Payload); err != nil {
		// ErrWebSocketMode is not an error - it signals WebSocket mode was activated
		if isWebSocketModeError(err) {
			log.Infof("websocket mode activated on first request")
			return
		}
		log.Infof("failed writing first packet, err=%v", err)
		a.connStore.Del(clientConnectionIDKey)
		_ = httpProxy.Close()
		// Check if this is a guardrails error - send the actual error message
		if isGuardrailsError(err) {
			log.Infof("guardrails validation failed on first request, closing session: %v", err)
			a.sendClientSessionCloseFromError(sessionID, err)
			return
		}
		a.sendClientTCPConnectionClose(sessionID, clientConnectionID)
		return
	}
}

// isGuardrailsError checks if the error is a guardrails validation failure.
//
// Note: This uses string matching because the error originates from libhoop
// (a separate Go module) and the agent cannot import libhoop's typed errors.
// The error message format is defined in libhoop/redactor/mspresidio/client.go.
//
// When modifying the guardrails error message format in mspresidio/client.go,
// ensure this function is updated accordingly.
func isGuardrailsError(err error) bool {
	if err == nil {
		return false
	}
	var guardrailErr *redactortypes.ErrGuardrailsValidation
	if errors.As(err, &guardrailErr) {
		return true
	}
	errStr := err.Error()
	// Check for the standard guardrails error message format from mspresidio/client.go:
	// "Blocked by the following Hoop Guardrails Rules: <rule_names>"
	return strings.Contains(errStr, "Blocked by the following Hoop Guardrails Rules")
}

// isWebSocketModeError checks if the error is the special ErrWebSocketMode
// which signals that WebSocket mode was activated (not a real error)
func isWebSocketModeError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "websocket mode activated")
}

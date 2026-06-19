package transport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/gateway/aianalyzer"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

const (
	// httpAnalyzerTimeout bounds the per-request AI analysis call so a slow or
	// hung provider cannot hold a proxied request open indefinitely.
	httpAnalyzerTimeout = 25 * time.Second
	// httpAnalyzerMaxBodyBytes caps how much request body is sent to the model.
	httpAnalyzerMaxBodyBytes = 8 * 1024
)

// httpConnAnalysis is the per-connection analyzer state for a connect-path HTTP
// proxy connection. A connection is analyzed exactly once — on the first packet
// that carries a complete HTTP request — after which it is "decided" and either
// passes through (allow/warn) or drops trailing bytes (blocked).
type httpConnAnalysis struct {
	decided bool
	blocked bool
}

// httpAnalyzerStore holds per-session, per-connection analyzer state. Access to
// the maps is guarded by mu. Each session's packets are processed on a single
// transport goroutine (listenClientMessages), so the *httpConnAnalysis values
// themselves are only ever touched by that one goroutine.
type httpAnalyzerStore struct {
	mu       sync.Mutex
	sessions map[string]map[string]*httpConnAnalysis
}

var httpAnalyzer = &httpAnalyzerStore{sessions: map[string]map[string]*httpConnAnalysis{}}

func (s *httpAnalyzerStore) conn(sid, connID string) *httpConnAnalysis {
	s.mu.Lock()
	defer s.mu.Unlock()
	conns := s.sessions[sid]
	if conns == nil {
		conns = map[string]*httpConnAnalysis{}
		s.sessions[sid] = conns
	}
	st := conns[connID]
	if st == nil {
		st = &httpConnAnalysis{}
		conns[connID] = st
	}
	return st
}

func (s *httpAnalyzerStore) dropSession(sid string) {
	s.mu.Lock()
	delete(s.sessions, sid)
	s.mu.Unlock()
}

// analyzeHTTPProxyConnect runs the AI session analyzer on requests flowing
// through the `hoop connect` HTTP proxy. On that path the client-side local
// proxy streams raw bytes as pbagent.HttpProxyConnectionWrite, so this inspects
// the byte stream gateway-side and reuses the same aianalyzer.AnalyzeHTTPRequest
// brain as the credential-token proxy server.
//
// It returns drop=true when the caller must NOT forward the packet to the agent;
// when clientPkt is non-nil the caller must send it back to the client (a 403
// for a blocked request).
//
// Only the first request of each connection is analyzed, decided from the first
// packet that carries a complete HTTP request:
//   - gives allow/deny at connect time for WebSocket upgrades — post-upgrade
//     frames never parse as a request and pass through untouched;
//   - covers the common case where a request's header block arrives in a single
//     packet (the client proxy copies in 32KiB chunks).
//
// Requests whose header block is split across multiple packets are passed
// through unanalyzed (fail open), as are connections with no analyzer rule and
// any analyzer/provider error — an analyzer outage must not take the resource
// down. Server-originated packets (the gateway HTTP proxy server, which analyzes
// in handleRequest) carry SpecHttpProxyServerKey and are skipped here.
func analyzeHTTPProxyConnect(pctx plugintypes.Context, pkt *pb.Packet) (drop bool, clientPkt *pb.Packet) {
	if !featureflag.IsEnabled(pctx.OrgID, featureflag.FlagHTTPSessionAnalyzer) {
		return false, nil
	}
	if _, ok := pkt.Spec[pb.SpecHttpProxyServerKey]; ok {
		return false, nil // already handled by the gateway HTTP proxy server
	}
	connID := string(pkt.Spec[pb.SpecClientConnectionID])
	if connID == "" {
		return false, nil
	}

	st := httpAnalyzer.conn(pctx.SID, connID)
	if st.decided {
		// Allowed → forward. Blocked → keep dropping the trailing bytes the
		// client may still send before it reads the 403 and closes.
		return st.blocked, nil
	}

	req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(pkt.Payload)))
	if err != nil {
		// Not a parseable HTTP request in this packet (binary, a post-upgrade
		// WebSocket frame, or a header block split across packets) — stop
		// inspecting this connection and let it through.
		st.decided = true
		return false, nil
	}
	st.decided = true

	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(io.LimitReader(req.Body, httpAnalyzerMaxBodyBytes))
	}

	orgID, err := uuid.Parse(pctx.OrgID)
	if err != nil {
		log.With("sid", pctx.SID).Warnf("ai analyzer: invalid org id %q, skipping analysis: %v", pctx.OrgID, err)
		return false, nil
	}

	ctx, cancel := context.WithTimeout(pctx.Context, httpAnalyzerTimeout)
	defer cancel()

	decision, err := aianalyzer.AnalyzeHTTPRequest(ctx, orgID, pctx.ConnectionName, req.Method, req.URL.RequestURI(), body)
	if err != nil {
		// Fail open: an analyzer/provider/LLM failure is an infrastructure
		// problem, not a risk signal, and blocking all traffic on it would take
		// the whole HTTP resource down.
		log.With("sid", pctx.SID, "conn", connID, "method", req.Method, "path", req.URL.Path).
			Errorf("ai analyzer: failed analyzing connect request, allowing through: %v", err)
		return false, nil
	}
	if decision == nil {
		return false, nil // no analyzer rule configured for this connection
	}

	logger := log.With("sid", pctx.SID, "org", pctx.OrgID, "conn", connID,
		"connection", pctx.ConnectionName, "method", req.Method, "path", req.URL.Path,
		"rule", decision.RuleName, "risk", string(decision.RiskLevel), "title", decision.Title)

	switch decision.Outcome {
	case aianalyzer.OutcomeBlock:
		logger.Warnf("ai analyzer blocked connect request: %s", decision.Explanation)
		st.blocked = true
		return true, httpProxyForbiddenPacket(pctx.SID, connID, decision)
	case aianalyzer.OutcomeWarn:
		logger.Warnf("ai analyzer flagged connect request (allowed): %s", decision.Explanation)
	}
	return false, nil
}

// httpProxyForbiddenPacket builds the raw HTTP 403 returned to the client when a
// connect-path request is blocked. The client-side local proxy writes the
// payload straight to the caller's TCP connection; "Connection: close" makes the
// caller close so its serveConn loop unwinds. The session stays alive.
func httpProxyForbiddenPacket(sid, connID string, d *aianalyzer.HTTPDecision) *pb.Packet {
	bodyJSON, _ := json.Marshal(map[string]any{
		"error":       "request blocked by hoop ai session analyzer",
		"risk_level":  string(d.RiskLevel),
		"title":       d.Title,
		"explanation": d.Explanation,
	})
	raw := fmt.Sprintf("HTTP/1.1 403 Forbidden\r\nContent-Type: application/json\r\n"+
		"Content-Length: %d\r\nConnection: close\r\n\r\n%s", len(bodyJSON), bodyJSON)
	return &pb.Packet{
		Type:    pbclient.HttpProxyConnectionWrite,
		Payload: []byte(raw),
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:   []byte(sid),
			pb.SpecClientConnectionID: []byte(connID),
		},
	}
}

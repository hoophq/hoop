package httpproxy

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

// A session with no explicit packet types is a plain httpproxy relay. Getting
// this wrong would silently route every existing connection's traffic to the
// agent's MCP adapter.
func TestSessionPacketTypesDefaultToHttpProxy(t *testing.T) {
	sess := &httpProxySession{}
	if got := sess.agentWriteType(); got != pbagent.HttpProxyConnectionWrite {
		t.Fatalf("agent write type = %q, want %q", got, pbagent.HttpProxyConnectionWrite)
	}
	if got := sess.clientWriteType(); got != pbclient.HttpProxyConnectionWrite {
		t.Fatalf("client write type = %q, want %q", got, pbclient.HttpProxyConnectionWrite)
	}
}

// A protocol-aware MCP session overrides both directions so the agent
// dispatches to the MCP adapter instead of the byte relay.
func TestSessionPacketTypesMCPOverride(t *testing.T) {
	sess := &httpProxySession{
		agentWritePacketType:  pbagent.MCPProxyConnectionWrite,
		clientWritePacketType: pbclient.MCPProxyConnectionWrite,
	}
	if got := sess.agentWriteType(); got != pbagent.MCPProxyConnectionWrite {
		t.Fatalf("agent write type = %q, want %q", got, pbagent.MCPProxyConnectionWrite)
	}
	if got := sess.clientWriteType(); got != pbclient.MCPProxyConnectionWrite {
		t.Fatalf("client write type = %q, want %q", got, pbclient.MCPProxyConnectionWrite)
	}
}

// A protocol-aware MCP session can park a tool call on a human reviewer, so
// the gateway must wait at least as long as the agent will hold that call.
// A gateway that gives up first answers a bare 504 and orphans a call the
// agent is still holding, which is what a hardcoded five minutes did the
// moment reviews were enabled on an MCP connection.
func TestResponseWaitCoversTheAgentHeldCallBudget(t *testing.T) {
	mcp := &httpProxySession{
		agentWritePacketType:  pbagent.MCPProxyConnectionWrite,
		clientWritePacketType: pbclient.MCPProxyConnectionWrite,
	}
	if got := mcp.responseWaitTimeout(); got <= pb.MCPHeldCallBudget {
		t.Fatalf("mcp response wait = %v, want more than the agent's held-call budget %v", got, pb.MCPHeldCallBudget)
	}

	// The byte relay never parks on a human, so it keeps the machine timeout.
	relay := &httpProxySession{}
	if got := relay.responseWaitTimeout(); got != httpProxyResponseWait {
		t.Fatalf("httpproxy response wait = %v, want %v", got, httpProxyResponseWait)
	}
}

// The two MCP packet types must differ from their httpproxy counterparts;
// colliding values would route MCP traffic through the opaque relay.
func TestMCPPacketTypesAreDistinct(t *testing.T) {
	if pbagent.MCPProxyConnectionWrite == pbagent.HttpProxyConnectionWrite {
		t.Fatal("agent MCP packet type collides with httpproxy")
	}
	if pbclient.MCPProxyConnectionWrite == pbclient.HttpProxyConnectionWrite {
		t.Fatal("client MCP packet type collides with httpproxy")
	}
}

// The mcpproxy connection type must resolve from its subtype under every
// parent type the UI can file it under, and must not collide with the legacy
// "mcp" httpproxy alias.
//
// The httpproxy parent is the load-bearing case: every MCP surface in the
// webapp files connections there (that is where the legacy "mcp" subtype
// lives), and the parent used to short-circuit before reading the subtype.
// A regression is silent — the session resolves to the byte relay, the agent
// never reaches its MCP adapter, and no policy or audit event is produced.
func TestMcpProxyConnectionTypeResolution(t *testing.T) {
	for _, parent := range []string{"application", "custom", "httpproxy"} {
		if got := pb.ToConnectionType(parent, "mcpproxy"); got != pb.ConnectionTypeMcpProxy {
			t.Fatalf("ToConnectionType(%q, mcpproxy) = %q, want %q", parent, got, pb.ConnectionTypeMcpProxy)
		}
	}
	// The legacy alias must keep resolving to httpproxy: ADR-0004 leaves it
	// untouched so the new path carries zero regression risk.
	if got := pb.ToConnectionType("httpproxy", "mcp"); got != pb.ConnectionTypeHttpProxy {
		t.Fatalf("legacy mcp subtype = %q, want %q", got, pb.ConnectionTypeHttpProxy)
	}
	// A plain httpproxy connection (no subtype, or any other subtype) must
	// keep taking the byte relay.
	for _, subtype := range []string{"", "httpproxy", "grafana"} {
		if got := pb.ToConnectionType("httpproxy", subtype); got != pb.ConnectionTypeHttpProxy {
			t.Fatalf("ToConnectionType(httpproxy, %q) = %q, want %q", subtype, got, pb.ConnectionTypeHttpProxy)
		}
	}
}

// The forwarded bytes must carry a Host line.
//
// net/http's server moves Host out of Request.Header into Request.Host, so
// rebuilding the request from the header map alone drops it. The agent's MCP
// adapter parses those bytes with a real http.Server, which rejects an
// HTTP/1.1 request without Host at the connection layer — the handler never
// runs and the client sees "400 Bad Request: missing required Host header"
// on `initialize`.
func TestBuildRawRequestPreservesHost(t *testing.T) {
	r := httptest.NewRequest("POST", "http://gw.example.com/mcp", strings.NewReader(`{"jsonrpc":"2.0"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(proxyTokenHeader, "Bearer secret")

	raw := buildRawRequest(r, r.Host, []byte(`{"jsonrpc":"2.0"}`))

	parsed, err := http.ReadRequest(bufio.NewReader(strings.NewReader(raw)))
	if err != nil {
		t.Fatalf("forwarded bytes do not parse: %v", err)
	}
	if parsed.Host != "gw.example.com" {
		t.Fatalf("forwarded Host = %q, want gw.example.com", parsed.Host)
	}
	// The proxy credential must never reach the upstream server.
	if got := parsed.Header.Get(proxyTokenHeader); got != "" {
		t.Fatalf("proxy token leaked upstream: %q", got)
	}
}

// End-to-end on the strict parser: feed the forwarded bytes to an http.Server
// exactly as libhoop's mcpadapter does (in-memory pipe, real net/http). Only
// this reproduces the 400, since http.ReadRequest tolerates a missing Host.
func TestForwardedRequestIsAcceptedByStrictHTTPServer(t *testing.T) {
	r := httptest.NewRequest("POST", "http://gw.example.com/mcp", strings.NewReader(`{"jsonrpc":"2.0"}`))
	r.Header.Set("Content-Type", "application/json")
	raw := buildRawRequest(r, r.Host, []byte(`{"jsonrpc":"2.0"}`))

	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintf(w, "handled %s", req.Host)
	})}
	go srv.Serve(newSingleConnListener(serverSide))
	defer srv.Close()

	_ = clientSide.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := clientSide.Write([]byte(raw)); err != nil {
		t.Fatalf("write: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(clientSide), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var body bytes.Buffer
		_, _ = body.ReadFrom(resp.Body)
		t.Fatalf("strict server rejected forwarded request: %s %s", resp.Status, body.String())
	}
}

// singleConnListener hands http.Server exactly one connection, mirroring
// libhoop/agent/mcpadapter's pipeListener.
type singleConnListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newSingleConnListener(c net.Conn) *singleConnListener {
	l := &singleConnListener{conns: make(chan net.Conn, 1), closed: make(chan struct{})}
	l.conns <- c
	return l
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *singleConnListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *singleConnListener) Addr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "mcpadapter-test" }

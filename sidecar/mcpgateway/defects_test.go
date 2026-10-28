package mcpgateway

// RD-379: each test pins a defect of mcpproxy v0.1.1 as it behaves today.
// They pass while the defect exists; a fix upstream makes them fail.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/mcpgateway/internal/testupstream"
	"github.com/hoophq/mcpproxy/backend"
	"github.com/hoophq/mcpproxy/checks"
	"github.com/hoophq/mcpproxy/config"
	mcpgw "github.com/hoophq/mcpproxy/gateway"
	"github.com/hoophq/mcpproxy/inspect"
)

func newProxy(t *testing.T, pol config.Policy, timeout time.Duration) string {
	t.Helper()
	up := httptest.NewServer(testupstream.Handler(testupstream.NewServer("")))
	t.Cleanup(up.Close)
	gw, err := mcpgw.New(mcpgw.Options{
		Backends: map[string]backend.Factory{
			"u": backend.NewFactory("u", config.Backend{Transport: backend.TransportStreamableHTTP, URL: up.URL + "/mcp"}, nil),
		},
		Pipeline:       checks.Assemble(pol, inspect.Hooks{}, nil, false),
		RequestTimeout: timeout,
		Resolver: func(r *http.Request) (inspect.Identity, error) {
			return inspect.Identity{Subject: r.Header.Get("x-user")}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	front := httptest.NewServer(gw.Handler())
	t.Cleanup(front.Close)
	return front.URL + "/mcp"
}

type client struct {
	url, sid, version string
	hdr               map[string]string
}

func (c *client) post(t *testing.T, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, c.url, bytes.NewBufferString(body))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json, text/event-stream")
	if c.sid != "" {
		req.Header.Set("Mcp-Session-Id", c.sid)
	}
	if c.version != "" {
		req.Header.Set("MCP-Protocol-Version", c.version)
	}
	for k, v := range c.hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.sid = sid
	}
	return resp.StatusCode, string(b)
}

func (c *client) init(t *testing.T, version string) {
	t.Helper()
	code, body := c.post(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":%q,"capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`, version))
	if code != 200 || c.sid == "" {
		t.Fatalf("initialize: %d %s", code, body)
	}
	c.version = version
	c.post(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if code, body := c.post(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`); code != 200 {
		t.Fatalf("tools/list: %d %s", code, body)
	}
}

// Defect 1: the proxy lets a client negotiate 2025-06-18, then refuses the
// MCP-Protocol-Version header the spec makes the client send afterwards.
func TestDefectRefusesNegotiatedProtocolVersion(t *testing.T) {
	url := newProxy(t, config.Policy{}, 10*time.Second)
	c := &client{url: url}
	code, body := c.post(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
	if code != 200 || !strings.Contains(body, `"protocolVersion":"2025-06-18"`) {
		t.Fatalf("initialize did not negotiate 2025-06-18: %d %s", code, body)
	}
	c.version = "2025-06-18"
	code, body = c.post(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "-32022") {
		t.Fatalf("defect gone: tools/list with the negotiated version header answered %d %s", code, body)
	}
	t.Logf("negotiated 2025-06-18, then tools/list answered %d %s", code, body)
}

// Defect 2: concurrent calls on one session write the rate-limit window map
// without a lock. Run with -race: the detector reports it.
func TestDefectRateLimitStateRace(t *testing.T) {
	url := newProxy(t, config.Policy{ToolRateLimits: map[string]int{"echo": 100000}}, 10*time.Second)
	c := &client{url: url}
	c.init(t, "2025-11-25")
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cc := &client{url: c.url, sid: c.sid, version: c.version}
			cc.post(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"echo","arguments":{"message":"x"}}}`, 100+i))
		}()
	}
	wg.Wait()
}

// Defect 3: server-to-client messages go to a 64-slot channel that only a GET
// stream drains. With no GET stream, the 65th message blocks the pump, and
// the response of the call that caused them never arrives.
func TestDefectStallsWithoutGETStream(t *testing.T) {
	url := newProxy(t, config.Policy{}, 4*time.Second)
	c := &client{url: url}
	c.init(t, "2025-11-25")
	if code, body := c.post(t, `{"jsonrpc":"2.0","id":3,"method":"logging/setLevel","params":{"level":"info"}}`); code != 200 {
		t.Fatalf("setLevel: %d %s", code, body)
	}
	call := func(id, n int) (time.Duration, int, string) {
		start := time.Now()
		code, body := c.post(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"chatty","arguments":{"count":%d}}}`, id, n))
		return time.Since(start), code, body
	}
	d, code, body := call(10, 10)
	if !strings.Contains(body, "sent 10 notifications") {
		t.Fatalf("10 notifications should pass: %v %d %s", d, code, body)
	}
	d, code, body = call(11, 100)
	if strings.Contains(body, "sent 100 notifications") {
		t.Fatalf("defect gone: 100 notifications passed in %v", d)
	}
	t.Logf("100 notifications: answered after %v with %d %s", d.Round(time.Millisecond), code, body)
}

// Defect 4: a session id works for any caller, and the work is attributed to
// the caller who opened it.
func TestDefectSessionNotBoundToCaller(t *testing.T) {
	url := newProxy(t, config.Policy{}, 10*time.Second)
	alice := &client{url: url, hdr: map[string]string{"x-user": "alice"}}
	alice.init(t, "2025-11-25")
	mallory := &client{url: url, sid: alice.sid, version: alice.version, hdr: map[string]string{"x-user": "mallory"}}
	code, body := mallory.post(t, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"echo","arguments":{"message":"as mallory"}}}`)
	var r struct {
		Result json.RawMessage `json:"result"`
	}
	_ = json.Unmarshal([]byte(body), &r)
	if code != 200 || len(r.Result) == 0 {
		t.Fatalf("defect gone: another caller's request on alice's session answered %d %s", code, body)
	}
}

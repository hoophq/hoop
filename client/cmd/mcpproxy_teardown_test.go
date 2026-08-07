//go:build !windows

package cmd

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hoophq/hoop/client/proxy"
	"github.com/hoophq/hoop/common/memory"
	pb "github.com/hoophq/hoop/common/proto"
)

// nullTransport is a pb.ClientTransport that swallows everything. The teardown
// paths under test never read from the stream, and the MCP replies they cause
// go nowhere useful.
type nullTransport struct{}

func (nullTransport) Send(*pb.Packet) error          { return nil }
func (nullTransport) Recv() (*pb.Packet, error)      { select {} }
func (nullTransport) StreamContext() context.Context { return context.Background() }
func (nullTransport) StartKeepAlive()                {}
func (nullTransport) Close() (error, error)          { return nil, nil }

// freePort reserves a port and releases it, so HttpProxy can bind it. Host()
// reports the configured address rather than the listener's, so "0" would
// leave the test with no address to dial.
func freePort(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	_, port, _ := net.SplitHostPort(lis.Addr().String())
	_ = lis.Close()
	return port
}

// startStdioChild drives a real MCPStdio into spawning a child and returns its
// pid. A live process is what makes the leak observable: an mcp-stdio owner
// that is never closed keeps the MCP server running on the user's machine,
// with the user's credentials, for as long as the CLI process lives.
func startStdioChild(t *testing.T, stdio *proxy.MCPStdio) int {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "pid")
	command, err := pb.GobEncode([]string{"/bin/sh", "-c", `echo $$ > "` + pidFile + `"; exec cat`})
	if err != nil {
		t.Fatalf("encode command: %v", err)
	}
	stdio.PacketWriteClient(&pb.Packet{
		Spec: map[string][]byte{
			pb.SpecMCPStdioBackendKey: []byte("b1"),
			pb.SpecMCPStdioRequestKey: []byte("r1"),
			pb.SpecMCPStdioCommandKey: command,
		},
		Payload: []byte(`{"id":1}`),
	})

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && pid > 0 {
				t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the mcp child never started")
	return 0
}

func waitDead(t *testing.T, pid int) bool {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, syscall.Signal(0)) != nil {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// An mcpproxy session stores two proxies: the HTTP front door under the bare
// session id and the client-stdio owner under mcpStdioStoreKey. SessionClose
// used to close only the first, so the MCP servers this machine was running on
// the session's behalf outlived the session that authorised them.
func TestSessionCloseReapsTheMCPStdioOwner(t *testing.T) {
	c := &connect{connStore: memory.New()}
	const sid = "session-1"

	front := proxy.NewHttpProxy(freePort(t), nullTransport{}, "")
	if err := front.Serve(sid); err != nil {
		t.Fatalf("serve: %v", err)
	}
	addr := front.Host().Addr()
	stdio := proxy.NewMCPStdio(nullTransport{}, sid)
	c.connStore.Set(sid, front)
	c.connStore.Set(mcpStdioStoreKey(sid), stdio)

	pid := startStdioChild(t, stdio)

	c.closeSessionProxies(sid)

	if !waitDead(t, pid) {
		t.Errorf("pid %d survived the session close; the MCP server keeps the user's credentials", pid)
	}
	if c.connStore.Has(mcpStdioStoreKey(sid)) {
		t.Error("the mcp-stdio entry is still in the store after the session closed")
	}
	if c.connStore.Has(sid) {
		t.Error("the http proxy entry is still in the store after the session closed")
	}
	if conn, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		_ = conn.Close()
		t.Errorf("the http listener on %s still accepts connections", addr)
	}
}

// The error-exit path has no session id, so it closes whatever the store
// holds. It used to close inside a type switch whose every branch ends the
// process, which meant only the first entry the map handed back was closed —
// and map order is random, so the client-stdio owner leaked about half the
// time.
func TestCloseAllProxiesReapsEveryEntry(t *testing.T) {
	c := &connect{connStore: memory.New()}
	const sid = "session-1"

	front := proxy.NewHttpProxy(freePort(t), nullTransport{}, "")
	if err := front.Serve(sid); err != nil {
		t.Fatalf("serve: %v", err)
	}
	stdio := proxy.NewMCPStdio(nullTransport{}, sid)
	c.connStore.Set(sid, front)
	c.connStore.Set(mcpStdioStoreKey(sid), stdio)

	pid := startStdioChild(t, stdio)

	objs := c.closeAllProxies()

	if len(objs) != 2 {
		t.Fatalf("closeAllProxies reported %d proxies, want both", len(objs))
	}
	if !waitDead(t, pid) {
		t.Errorf("pid %d survived the error exit; the MCP server keeps the user's credentials", pid)
	}
	if n := len(c.connStore.List()); n != 0 {
		t.Errorf("%d entries left in the store after closing every proxy", n)
	}
}

// A packet tagged with SpecMCPEventKey is a structured audit record for the
// session recorder, not response bytes. Writing it to the local listener
// splices audit JSON into a keep-alive HTTP response stream and the MCP
// client's parser sees a corrupt frame.
func TestMCPEventPacketsNeverReachTheMCPClient(t *testing.T) {
	c := &connect{connStore: memory.New()}
	const sid = "session-1"

	front := proxy.NewHttpProxy(freePort(t), nullTransport{}, "")
	if err := front.Serve(sid); err != nil {
		t.Fatalf("serve: %v", err)
	}
	t.Cleanup(func() { _ = front.Close() })
	c.connStore.Set(sid, front)

	client, err := net.DialTimeout("tcp", front.Host().Addr(), 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	// The proxy assigns connection ids as it accepts, starting at 1. Writes
	// for an unregistered connection are dropped with a warning, so the test
	// would be vacuous if it started asserting before the accept landed:
	// probe until a byte comes back, then drain what the probing produced.
	const connID = "1"
	write := func(spec map[string][]byte, payload string) {
		spec[pb.SpecGatewaySessionID] = []byte(sid)
		spec[pb.SpecClientConnectionID] = []byte(connID)
		c.writeMCPProxyResponse(&pb.Packet{Spec: spec, Payload: []byte(payload)})
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("the proxy never registered the accepted connection")
		}
		write(map[string][]byte{}, "probe")
		if _, err := readWithin(client, 512, 200*time.Millisecond); err == nil {
			break
		}
	}
	for {
		if _, err := readWithin(client, 512, 300*time.Millisecond); err != nil {
			break
		}
	}

	write(map[string][]byte{pb.SpecMCPEventKey: []byte("1")}, `{"event":"tool_call"}`+"\n")
	// A real response after it, so the read below has something to find
	// instead of merely timing out on an empty stream.
	write(map[string][]byte{}, "HTTP/1.1 200 OK\r\n")

	data, err := readWithin(client, 512, 5*time.Second)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(data), "tool_call") {
		t.Fatalf("the audit event reached the MCP client: %q", data)
	}
	if !strings.Contains(string(data), "HTTP/1.1 200 OK") {
		t.Fatalf("the real response did not arrive, got %q", data)
	}
}

func readWithin(conn net.Conn, n int, d time.Duration) ([]byte, error) {
	if err := conn.SetReadDeadline(time.Now().Add(d)); err != nil {
		return nil, err
	}
	buf := make([]byte, n)
	read, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:read], nil
}

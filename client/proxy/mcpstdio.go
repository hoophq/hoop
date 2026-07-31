package proxy

// Client-hosted MCP stdio server.
//
// The counterpart of the agent's clientStdioBackend. The agent runs the whole
// inspection pipeline — tool policy, guardrails, masking, audit — and then,
// instead of writing the approved JSON-RPC envelope to a child of its own, it
// sends it here. This process owns the child, so the MCP server runs on the
// developer's machine with their filesystem and their credentials.
//
// Unlike every other proxy in this package there is no listener: work arrives
// from the agent rather than from a local socket. That makes it the only
// client proxy that acts on an unsolicited request, so it is deliberately
// narrow — it will only ever run the command the connection is configured
// with, which reaches it on each request and never from local input.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
)

const (
	// mcpStdioMaxLine bounds one JSON-RPC line read from the child. It matches
	// the agent-side library's limit, so a message that would be rejected
	// there is not silently truncated here.
	mcpStdioMaxLine = 16 << 20
	// mcpStdioTermGrace is how long a child gets to exit after its stdin is
	// closed before the process group is signalled.
	mcpStdioTermGrace = 3 * time.Second
)

// MCPStdio owns the MCP server child processes for one hoop session.
//
// One child per backend id: a user who restarts their MCP client gets a fresh
// MCP session and therefore a fresh child, while the old one is reaped by the
// close packet the agent sends when its backend shuts down.
type MCPStdio struct {
	client    pb.ClientTransport
	sessionID string

	mu       sync.Mutex
	children map[string]*mcpChild
	closed   bool
}

func NewMCPStdio(client pb.ClientTransport, sessionID string) *MCPStdio {
	return &MCPStdio{
		client:    client,
		sessionID: sessionID,
		children:  map[string]*mcpChild{},
	}
}

// mcpChild is one running MCP server process and its pipes.
type mcpChild struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	// wmu serialises writes to the child's stdin. The agent serialises tool
	// calls per MCP session, but a server-initiated request can be denied
	// concurrently, so two writes can race.
	wmu sync.Mutex

	closeOnce sync.Once
}

// startMCPChild spawns the MCP server with its stdio wired to pipes.
//
// The child inherits nothing but the environment the connection configured.
// stderr goes to the CLI's own stderr so a server that fails to start says
// why, instead of the user seeing a silent timeout.
func startMCPChild(command []string, env map[string]string) (*mcpChild, error) {
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Env = mcpChildEnv(env)
	cmd.Stderr = os.Stderr
	// Its own process group, so close() can signal the whole tree: an
	// `npx`-style server is a shell that execs node, and signalling only the
	// direct child leaves the server running.
	setMCPChildProcGroup(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed opening mcp server stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed opening mcp server stdout: %v", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed starting mcp server %q: %v", command[0], err)
	}
	return &mcpChild{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}

// mcpChildEnv builds the child's environment from the connection's MCPENV_*
// settings on top of the user's own. Inheriting matters: an MCP server
// typically needs PATH to find node, and HOME to find the credentials that
// are the whole reason for running it on this machine.
func mcpChildEnv(env map[string]string) []string {
	if len(env) == 0 {
		return os.Environ()
	}
	base := os.Environ()
	out := make([]string, 0, len(base)+len(env))
	for _, kv := range base {
		if k, _, ok := strings.Cut(kv, "="); ok {
			if _, overridden := env[k]; overridden {
				continue
			}
		}
		out = append(out, kv)
	}
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// PacketWriteClient handles one MCPStdioRequest: it spawns the child if this
// is the first message for the backend, writes the envelope to its stdin, and
// acknowledges the write.
//
// The ack is what unblocks the agent's Send. Whatever the server answers
// travels separately, pushed by pumpStdout as it is produced — a server may
// emit notifications before, between or after responses, and pairing output
// lines with input requests here would be wrong.
//
// Failures are reported rather than logged and dropped: the agent is blocked
// on this write and would otherwise wait out the full request timeout for a
// child that will never answer.
func (m *MCPStdio) PacketWriteClient(pkt *pb.Packet) {
	backendID := string(pkt.Spec[pb.SpecMCPStdioBackendKey])
	requestID := string(pkt.Spec[pb.SpecMCPStdioRequestKey])

	child, err := m.childFor(backendID, pkt)
	if err != nil {
		m.replyError(backendID, requestID, err)
		return
	}

	if err := child.write(pkt.Payload); err != nil {
		// A dead child is not recoverable for this request, but the next one
		// should be able to respawn rather than inherit the corpse.
		m.dropChild(backendID)
		m.replyError(backendID, requestID, err)
		return
	}
	m.send(&pb.Packet{Type: pbagent.MCPStdioReply, Spec: m.spec(backendID, requestID)})
}

// childFor returns the backend's child, spawning it on first use.
func (m *MCPStdio) childFor(backendID string, pkt *pb.Packet) (*mcpChild, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, fmt.Errorf("session closed")
	}
	if c, ok := m.children[backendID]; ok {
		return c, nil
	}

	var command []string
	if raw := pkt.Spec[pb.SpecMCPStdioCommandKey]; len(raw) > 0 {
		if err := pb.GobDecodeInto(raw, &command); err != nil {
			return nil, fmt.Errorf("failed decoding command: %v", err)
		}
	}
	if len(command) == 0 {
		return nil, fmt.Errorf("no command configured for this connection")
	}
	var env map[string]string
	if raw := pkt.Spec[pb.SpecMCPStdioEnvKey]; len(raw) > 0 {
		if err := pb.GobDecodeInto(raw, &env); err != nil {
			return nil, fmt.Errorf("failed decoding environment: %v", err)
		}
	}

	c, err := startMCPChild(command, env)
	if err != nil {
		return nil, err
	}
	m.children[backendID] = c

	log.Printf("session=%v | mcp-backend=%s - started local mcp server: %s",
		m.sessionID, backendID, strings.Join(command, " "))

	go m.pumpStdout(backendID, c)
	return c, nil
}

// pumpStdout forwards every JSON-RPC line the child emits to the agent.
//
// Responses and server-initiated messages travel the same way: the agent's
// backend puts them on its Recv channel, and the mcpproxy gateway correlates
// responses by JSON-RPC id exactly as it does for a local child. The request
// id is therefore not echoed here — this reader has no idea which request a
// given line answers, and does not need to.
func (m *MCPStdio) pumpStdout(backendID string, c *mcpChild) {
	scanner := bufio.NewScanner(c.stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), mcpStdioMaxLine)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Servers started through npx and friends print banners to stdout.
		// Anything that is not a JSON value is not protocol traffic.
		if line == "" || (line[0] != '{' && line[0] != '[') {
			continue
		}
		if !json.Valid([]byte(line)) {
			continue
		}
		m.send(&pb.Packet{
			Type:    pbagent.MCPStdioReply,
			Spec:    m.spec(backendID, ""),
			Payload: []byte(line),
		})
	}
	m.dropChild(backendID)
}

// write delivers one envelope to the child as a single newline-terminated
// line, the framing every stdio MCP server expects.
func (c *mcpChild) write(msg []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	line := append(trimTrailingNewline(msg), '\n')
	if _, err := c.stdin.Write(line); err != nil {
		return fmt.Errorf("failed writing to mcp server: %v", err)
	}
	return nil
}

func trimTrailingNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

// PacketCloseClient reaps the child named in the packet. Sent by the agent
// when its backend shuts down while the hoop session stays open.
func (m *MCPStdio) PacketCloseClient(pkt *pb.Packet) {
	m.dropChild(string(pkt.Spec[pb.SpecMCPStdioBackendKey]))
}

func (m *MCPStdio) dropChild(backendID string) {
	m.mu.Lock()
	c := m.children[backendID]
	delete(m.children, backendID)
	m.mu.Unlock()
	if c != nil {
		c.close()
		log.Printf("session=%v | mcp-backend=%s - stopped local mcp server", m.sessionID, backendID)
	}
}

// Close terminates every child. Satisfies the shutdown path in connect.go,
// which closes whatever the session left in its store.
func (m *MCPStdio) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	children := m.children
	m.children = map[string]*mcpChild{}
	m.mu.Unlock()

	for _, c := range children {
		c.close()
	}
	return nil
}

// CloseTCPConnection satisfies the Closer interface shared by the socket-based
// proxies. There are no TCP connections here, so it is a no-op: the agent
// reaps children with MCPStdioClose, which carries a backend id.
func (m *MCPStdio) CloseTCPConnection(string) {}

// close ends the child politely (stdin EOF is how a stdio MCP server is asked
// to exit) before signalling the process group.
func (c *mcpChild) close() {
	c.closeOnce.Do(func() {
		c.wmu.Lock()
		_ = c.stdin.Close()
		c.wmu.Unlock()

		done := make(chan struct{})
		go func() {
			_ = c.cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
			return
		case <-time.After(mcpStdioTermGrace):
		}
		// The child spawns its own children (npx -> node), so signal the
		// group; killing only the direct child orphans the server.
		if c.cmd.Process != nil {
			killProcessGroup(c.cmd.Process.Pid)
		}
		<-done
	})
}

func (m *MCPStdio) replyError(backendID, requestID string, err error) {
	log.Printf("session=%v | mcp-backend=%s - %v", m.sessionID, backendID, err)
	spec := m.spec(backendID, requestID)
	spec[pb.SpecMCPStdioErrorKey] = []byte(err.Error())
	m.send(&pb.Packet{Type: pbagent.MCPStdioReply, Spec: spec})
}

func (m *MCPStdio) send(pkt *pb.Packet) {
	if err := m.client.Send(pkt); err != nil {
		log.Printf("session=%v - failed sending mcp stdio packet: %v", m.sessionID, err)
	}
}

func (m *MCPStdio) spec(backendID, requestID string) map[string][]byte {
	spec := map[string][]byte{
		pb.SpecGatewaySessionID:   []byte(m.sessionID),
		pb.SpecMCPStdioBackendKey: []byte(backendID),
	}
	if requestID != "" {
		spec[pb.SpecMCPStdioRequestKey] = []byte(requestID)
	}
	return spec
}

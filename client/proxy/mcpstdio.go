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
	// mcpStdioKillGrace is how long the group gets after SIGTERM before
	// SIGKILL. Shorter than the stdin grace on purpose: a server that meant
	// to shut down cleanly already had its chance, and this window is only
	// for flushing whatever the signal handler is finishing.
	mcpStdioKillGrace = 2 * time.Second
	// mcpStdioReapGrace bounds the wait for cmd.Wait() after SIGKILL.
	// SIGKILL cannot be caught, so this expires only for a process stuck in
	// uninterruptible sleep. Short, because at that point waiting longer has
	// no plausible upside and shutdown is already blocked on it.
	mcpStdioReapGrace = 2 * time.Second
	// mcpStdioQueueSize bounds one backend's pending requests.
	//
	// The agent serialises tool calls per MCP session and waits for each write
	// ack, so the steady-state depth is one. A queue this deep only fills when
	// the child has stopped reading its stdin and the worker is wedged in the
	// pipe write. Past the bound a request is answered with an error rather
	// than dropped: the agent is blocked on the ack and would otherwise wait
	// out its whole request timeout for a child that will never answer.
	mcpStdioQueueSize = 64
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
	backends map[string]*mcpBackend
	closed   bool
}

// mcpBackend is the dispatch state for one backend id.
//
// Requests for a backend are handled by one goroutine reading queue, so the
// envelopes reach the child's stdin in the order the agent sent them. An MCP
// session is a stateful conversation: `initialize` overtaking the call that
// follows it is not something the server recovers from. Backends are
// independent of each other, so each gets its own goroutine — spawning a child
// for one MCP session must never stall another.
type mcpBackend struct {
	// queue is nil when the backend has only ever been reaped, which happens
	// when a close arrives for a backend that never issued a request.
	queue chan *pb.Packet

	// reaped records that the agent's MCPStdioClose for this backend has been
	// seen. This flag, not the close packet's position in the queue, is what
	// enforces the wire order: the agent's sendMu guarantees it never sends a
	// request after the close (agent/controller/mcpstdio.go writeRequest), so
	// once the close lands every later request is a straggler and must be
	// refused. Without that, a straggler spawns a REPLACEMENT MCP server on
	// the user's machine whose reaping packet has already been spent, so it
	// keeps running — holding the connection's credentials — until the whole
	// hoop session ends.
	reaped bool
}

func NewMCPStdio(client pb.ClientTransport, sessionID string) *MCPStdio {
	return &MCPStdio{
		client:    client,
		sessionID: sessionID,
		children:  map[string]*mcpChild{},
		backends:  map[string]*mcpBackend{},
	}
}

// mcpChild is one running MCP server process and its pipes.
type mcpChild struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	// wmu serialises writes to the child's stdin. The agent serialises tool
	// calls per MCP session, but a server-initiated request can be denied
	// concurrently, so two writes can race. close() deliberately does not
	// take it — see the comment there.
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

// PacketWriteClient queues one MCPStdioRequest for its backend and returns.
//
// None of the work happens here. The caller is the CLI's receive loop, which
// is also carrying the HTTP traffic of the request that triggered this one,
// and spawning a child must not stall it. The work runs on the backend's own
// goroutine, which is also what keeps requests in arrival order: handing each
// packet to its own `go` instead would let a request overtake the close that
// reaps the child it targets.
func (m *MCPStdio) PacketWriteClient(pkt *pb.Packet) {
	backendID := string(pkt.Spec[pb.SpecMCPStdioBackendKey])

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		m.rejectRequest(backendID, pkt, fmt.Errorf("session closed"))
		return
	}
	b := m.backends[backendID]
	if b == nil {
		b = &mcpBackend{queue: make(chan *pb.Packet, mcpStdioQueueSize)}
		m.backends[backendID] = b
		go m.serveBackend(b.queue)
	}
	if b.reaped {
		m.mu.Unlock()
		m.rejectRequest(backendID, pkt, fmt.Errorf("mcp backend was already closed by the agent"))
		return
	}
	// The send is non-blocking, so holding mu across it costs nothing and is
	// what makes closing the queue elsewhere safe: no goroutine can be about
	// to send on a queue whose owner has taken the lock to close it.
	select {
	case b.queue <- pkt:
		m.mu.Unlock()
	default:
		m.mu.Unlock()
		m.rejectRequest(backendID, pkt, fmt.Errorf(
			"mcp server is not reading its input, %d requests already queued", mcpStdioQueueSize))
	}
}

// serveBackend runs one backend's requests, in order, until its queue is
// closed by reapBackend or by session teardown.
func (m *MCPStdio) serveBackend(queue <-chan *pb.Packet) {
	for pkt := range queue {
		m.handleRequest(pkt)
	}
}

// rejectRequest answers a request that will never reach a child.
//
// On a goroutine because the caller is the receive loop and replyError writes
// to the gRPC stream. Ordering does not matter here the way it does for the
// child's stdin: the agent correlates the reply by request id, and this reply
// is the only thing that stops it waiting out the full request timeout.
func (m *MCPStdio) rejectRequest(backendID string, pkt *pb.Packet, err error) {
	requestID := string(pkt.Spec[pb.SpecMCPStdioRequestKey])
	go m.replyError(backendID, requestID, err)
}

// handleRequest spawns the child if this is the first message for the backend,
// writes the envelope to its stdin, and acknowledges the write.
//
// The ack is what unblocks the agent's Send. Whatever the server answers
// travels separately, pushed by pumpStdout as it is produced — a server may
// emit notifications before, between or after responses, and pairing output
// lines with input requests here would be wrong.
//
// Failures are reported rather than logged and dropped: the agent is blocked
// on this write and would otherwise wait out the full request timeout for a
// child that will never answer.
func (m *MCPStdio) handleRequest(pkt *pb.Packet) {
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
		m.dropChildIf(backendID, child)
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
	// Refusing here is what stops a straggling request from outliving its own
	// reaping packet. Backend ids are never reused, so a reaped backend stays
	// reaped for the rest of the session.
	if b := m.backends[backendID]; b != nil && b.reaped {
		return nil, fmt.Errorf("mcp backend was already closed by the agent")
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
	m.dropChildIf(backendID, c)
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
	m.reapBackend(string(pkt.Spec[pb.SpecMCPStdioBackendKey]))
}

// reapBackend closes a backend for good: no request may spawn for it again,
// whatever requests are still queued are refused, and whatever child is mapped
// to it is ended.
//
// Unconditional on the child, unlike dropChildIf. The agent is saying "reap
// whatever is running for this backend" and it is the only party that knows
// the backend is finished, so identity is not this caller's business.
//
// It does not wait its turn behind the backend's queued requests. A child that
// has stopped reading its stdin wedges the worker in the pipe write, and
// queueing the reap behind that would leave the MCP server running on the
// user's machine for the rest of the session — the exact leak this packet
// exists to prevent. Jumping the queue is safe because reaped, not queue
// position, refuses the stragglers; closing the child's stdin also unwedges
// the worker, which then drains what is left and exits.
//
// The child is ended on a goroutine because the caller is the receive loop and
// close() walks a signal ladder that can take seconds.
func (m *MCPStdio) reapBackend(backendID string) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	b := m.backends[backendID]
	if b == nil {
		// A close for a backend that never issued a request. Record the reap
		// anyway: the request that raced ahead of it on the gateway must not
		// spawn a server whose reaping packet has already been spent.
		b = &mcpBackend{}
		m.backends[backendID] = b
	}
	if b.reaped {
		m.mu.Unlock()
		return
	}
	b.reaped = true
	if b.queue != nil {
		close(b.queue)
	}
	child := m.children[backendID]
	delete(m.children, backendID)
	m.mu.Unlock()

	if child != nil {
		go func() {
			child.close()
			log.Printf("session=%v | mcp-backend=%s - stopped local mcp server", m.sessionID, backendID)
		}()
	}
}

// dropChildIf ends the backend's child only when the mapped child is still c.
//
// The identity check is the whole point. pumpStdout calls this when its own
// child's stdout closes, but by then the map may hold a healthy child spawned
// for a later request; a plain delete would have a corpse's reader reap a live
// MCP server, and the MCP client would see its session die mid-conversation.
func (m *MCPStdio) dropChildIf(backendID string, c *mcpChild) {
	m.mu.Lock()
	if m.children[backendID] != c {
		m.mu.Unlock()
		return
	}
	delete(m.children, backendID)
	m.mu.Unlock()

	c.close()
	log.Printf("session=%v | mcp-backend=%s - stopped local mcp server", m.sessionID, backendID)
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
	// Closing the queues is what stops the per-backend workers. One wedged in
	// a pipe write is freed by its child's stdin closing below, drains what is
	// left — every straggler now refused by childFor's closed check — and
	// exits.
	for _, b := range m.backends {
		if b.queue != nil && !b.reaped {
			close(b.queue)
		}
	}
	m.backends = map[string]*mcpBackend{}
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

// close ends the child and returns in bounded time.
//
// Three rungs, each with its own deadline: stdin EOF (how a stdio MCP server
// is asked to exit), SIGTERM to the process group, then SIGKILL. It used to
// stop at SIGTERM and then block on cmd.Wait() forever, so a server that
// ignores the signal — a broken handler, a wedged uninterruptible read — hung
// whichever goroutine was tidying up. That is the CLI's shutdown path, so the
// user's terminal never came back.
//
// The final wait is bounded too. SIGKILL is unblockable, but the process stays
// unreaped while it is in uninterruptible sleep (a stuck NFS read, a hung FUSE
// mount), and cleanup must not inherit that. Giving up leaks a zombie until
// the CLI exits, which is strictly better than never returning; it is logged
// because a child that survives SIGKILL is worth knowing about.
func (c *mcpChild) close() {
	c.closeOnce.Do(func() {
		// Closing stdin without taking wmu is deliberate. write() holds wmu
		// across the pipe write, and a child that has stopped reading wedges
		// that write once the pipe buffer fills — waiting for the lock here
		// would hang shutdown on exactly the child this ladder exists to kill,
		// which is how Ctrl-C stopped returning. os.File tolerates a
		// concurrent Close: the blocked write is parked in the runtime poller
		// and returns ErrClosed, so closing is also what unwedges the writer.
		_ = c.stdin.Close()

		done := make(chan struct{})
		go func() {
			_ = c.cmd.Wait()
			close(done)
		}()
		c.terminate(done)
	})
}

// terminate runs the escalation ladder against a child whose exit is signalled
// by done. Split from close so a test can drive it with an exit that never
// arrives — the uninterruptible-sleep case, which no signal can produce.
func (c *mcpChild) terminate(done <-chan struct{}) {
	if waitExit(done, mcpStdioTermGrace) {
		return
	}
	if c.cmd.Process == nil {
		return
	}
	pid := c.cmd.Process.Pid
	// The child spawns its own children (npx -> node), so signal the group;
	// killing only the direct child orphans the server.
	termProcessGroup(pid)
	if waitExit(done, mcpStdioKillGrace) {
		return
	}
	log.Printf("mcp server pid=%v ignored SIGTERM, sending SIGKILL", pid)
	killProcessGroup(pid)
	if !waitExit(done, mcpStdioReapGrace) {
		log.Printf("mcp server pid=%v survived SIGKILL, abandoning it to avoid blocking shutdown", pid)
	}
}

// waitExit reports whether the child exited within d.
func waitExit(done <-chan struct{}, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
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

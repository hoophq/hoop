package controller

// Reverse (client-hosted) stdio backend for the protocol-aware MCP path.
//
// The default `stdio` transport runs the MCP server as a child of THIS agent.
// That is wrong whenever the server needs the developer's own machine: their
// filesystem, their SSH agent, their already-authenticated CLI tools. This
// transport keeps every inspection stage where it is — policy, guardrails,
// masking and audit all still run in the agent — and moves only the process
// to the machine that ran `hoop connect`.
//
//	MCP client -> hoop connect (local port) -> gateway -> agent
//	  -> mcpproxy gateway (policy, masking, audit)
//	  -> clientStdioBackend  ==tunnel==>  hoop connect -> child stdin/stdout
//
// The tunnel rides the same gRPC stream the client already owns, so the
// answer returns to the session that asked. Nothing here is MCP-specific
// beyond the payload: the backend moves whole JSON-RPC envelopes, and the
// newline framing a real stdio server needs is applied by the CLI at the pipe.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	mcpbackend "github.com/hoophq/mcpproxy/backend"
)

// mcpTransportClientStdio is the MCP_TRANSPORT value selecting this backend.
// It is deliberately not one of mcpproxy's own transport strings: the library
// never constructs it, hoop injects the factory directly.
const mcpTransportClientStdio = "client-stdio"

// mcpStdioRecvBuffer bounds the queue of server-initiated messages waiting for
// the gateway's pump goroutine. It matches the library's own stdio backend so
// a burst of notifications behaves identically on both transports.
//
// What happens at the bound does NOT match, and cannot. A local stdio child
// pushes back: its pump parks on the channel and the child's own stdout write
// blocks, so nothing is lost. Here the producer is the agent's shared recv
// loop, which must never park on one backend, so a full buffer drops (see
// deliver). 64 is therefore a real ceiling on how far this backend's server
// may run ahead of the gateway, not just a hint.
const mcpStdioRecvBuffer = 64

// clientStdioBackend implements mcpproxy's backend.Backend by tunnelling to a
// child process on the connecting user's machine.
//
// Lifetime is one MCP session, per the Backend contract. Several can exist
// concurrently under one hoop session when a user reconnects their MCP client,
// which is why the backend id — not the hoop session id — scopes the child.
type clientStdioBackend struct {
	agent     *Agent
	name      string
	sessionID string
	backendID string
	// spec is the packet spec identifying the client stream, captured from
	// the request that created this backend.
	spec map[string][]byte

	command []string
	env     map[string]string

	recv chan []byte
	done chan struct{}

	reqSeq atomic.Uint64

	// sendMu orders this backend's writes to the client stream against its
	// own teardown. A holder may write; Close takes it before flipping
	// closed, so no MCPStdioRequest can reach the wire after the
	// MCPStdioClose that reaps the child it would be delivered to.
	//
	// Held only across the write, never across the wait for the ack: the ack
	// arrives on the recv loop, and two concurrent Sends must not serialise
	// on each other's round trips.
	//
	// Lock order is sendMu then mu, never the reverse. mu stays out of the
	// write path so deliver — which runs on the agent's shared recv loop —
	// never waits on the network.
	sendMu sync.Mutex

	// mu guards the fields below. It does NOT serialise Send against Close;
	// sendMu does. backend.Backend does not promise serialised Send either:
	// the gateway's pump goroutine calls Send to deny a server-initiated
	// request while a client POST is in flight (mcpproxy gateway/conn.go,
	// inspectS2C).
	mu      sync.Mutex
	closed  bool
	started bool
	// waiters maps a request id to the channel its reply must land on.
	waiters map[string]chan *pb.Packet
	// dropped counts server messages deliver discarded because the pump had
	// not drained recv. Nonzero means the gateway consumer is falling behind
	// this backend's server, and each one is a message the MCP client will
	// never see.
	dropped uint64
	err     error
}

// clientStdioFactory returns the mcpproxy factory that builds a tunnelled
// backend. Registration happens in Start rather than here because the factory
// may be called for a session that is then abandoned before Start.
func (a *Agent) clientStdioFactory(name, sessionID string) mcpbackend.Factory {
	return func(ctx context.Context) (mcpbackend.Backend, error) {
		connParams := a.connectionParams(sessionID)
		if connParams == nil {
			return nil, fmt.Errorf("connection params not found for session")
		}
		connenv, err := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionType(connParams.ConnectionType))
		if err != nil {
			return nil, fmt.Errorf("failed reading connection settings: %v", err)
		}
		if len(connParams.CmdList) == 0 {
			// Without a command the CLI has nothing to spawn. Failing here
			// turns it into a clean "failed to start session" on initialize
			// instead of a request that hangs until the 30-minute timeout.
			return nil, fmt.Errorf("mcpproxy connection with MCP_TRANSPORT=%s has no command configured",
				mcpTransportClientStdio)
		}
		return &clientStdioBackend{
			agent:     a,
			name:      name,
			sessionID: sessionID,
			backendID: a.nextMCPStdioBackendID(sessionID),
			spec:      a.mcpStdioSpecFor(sessionID),
			command:   connParams.CmdList,
			env:       connenv.mcpEnv,
			recv:      make(chan []byte, mcpStdioRecvBuffer),
			done:      make(chan struct{}),
			waiters:   map[string]chan *pb.Packet{},
		}, nil
	}
}

func (b *clientStdioBackend) Name() string { return b.name }

// Start registers the backend so replies can find it. The child itself is
// spawned lazily by the CLI on the first request, which keeps Start from
// needing its own round trip and means a session that never issues a call
// never starts a process on the user's machine.
func (b *clientStdioBackend) Start(context.Context) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return mcpbackend.ErrClosed
	}
	b.started = true
	b.mu.Unlock()

	b.agent.mcpStdioBackends.Store(b.key(), b)
	return nil
}

func (b *clientStdioBackend) Recv() <-chan []byte   { return b.recv }
func (b *clientStdioBackend) Done() <-chan struct{} { return b.done }

func (b *clientStdioBackend) Err() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}

// Send delivers one JSON-RPC envelope to the user's machine.
//
// It returns once the client confirms the envelope reached the child's stdin,
// which is the same point the library's stdio backend returns at — its Write
// hits a pipe. It deliberately does NOT wait for the MCP response: responses
// arrive asynchronously on Recv, where the gateway correlates them by
// JSON-RPC id, and a server may emit notifications between a request and its
// answer. Waiting for the answer here would deadlock exactly that.
//
// The ack exists because the write happens on another machine. Without it a
// server that cannot spawn produces no error and no reply, and the gateway
// waits out its full request timeout.
//
// The write is ordered against Close by sendMu (see writeRequest), so a
// request can never land on the client after the MCPStdioClose that reaps the
// child it was bound for. The wait below is deliberately outside that lock.
func (b *clientStdioBackend) Send(ctx context.Context, msg []byte) error {
	reqID := strconv.FormatUint(b.reqSeq.Add(1), 10)
	ackC := make(chan *pb.Packet, 1)

	defer func() {
		b.mu.Lock()
		delete(b.waiters, reqID)
		b.mu.Unlock()
	}()

	if err := b.writeRequest(reqID, ackC, msg); err != nil {
		return err
	}

	select {
	case ack, ok := <-ackC:
		if !ok {
			return mcpbackend.ErrClosed
		}
		if errMsg := string(ack.Spec[pb.SpecMCPStdioErrorKey]); errMsg != "" {
			return fmt.Errorf("mcp stdio backend on client: %s", errMsg)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-b.done:
		return mcpbackend.ErrClosed
	}
}

// writeRequest registers the ack waiter and puts one MCPStdioRequest on the
// wire, holding sendMu across both.
//
// Holding it across both is the point. Close takes sendMu before it flips
// closed, so either this runs first — and the MCPStdioClose that follows reaps
// whatever child the request spawned — or Close wins and the closed check here
// rejects the request before it is written. Without that, Close could complete
// entirely inside the gap between the check and the write, inverting the wire
// order: the CLI reaps the child on MCPStdioClose, then the late
// MCPStdioRequest finds no entry in its child map and spawns a REPLACEMENT MCP
// server on the user's machine — one whose reaping packet has already been
// spent, so it survives until the whole hoop session ends.
//
// b.mu is taken inside sendMu, never the other way round, and is released
// before the write: deliver runs on the agent's shared recv loop and must
// never wait on a network write (see deliver).
func (b *clientStdioBackend) writeRequest(reqID string, ackC chan *pb.Packet, msg []byte) error {
	pkt := &pb.Packet{
		Type:    pbclient.MCPStdioRequest,
		Spec:    b.newSpec(),
		Payload: msg,
	}
	pkt.Spec[pb.SpecMCPStdioRequestKey] = []byte(reqID)
	// The command rides every request so a client that reconnects mid-session,
	// or one whose child died, can respawn without a separate handshake.
	cmd, err := pb.GobEncode(b.command)
	if err != nil {
		return fmt.Errorf("failed encoding mcp stdio command: %v", err)
	}
	pkt.Spec[pb.SpecMCPStdioCommandKey] = cmd
	if len(b.env) > 0 {
		env, err := pb.GobEncode(b.env)
		if err != nil {
			return fmt.Errorf("failed encoding mcp stdio env: %v", err)
		}
		pkt.Spec[pb.SpecMCPStdioEnvKey] = env
	}

	b.sendMu.Lock()
	defer b.sendMu.Unlock()

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return mcpbackend.ErrClosed
	}
	b.waiters[reqID] = ackC
	b.mu.Unlock()

	if err := b.agent.client.Send(pkt); err != nil {
		return fmt.Errorf("failed sending mcp stdio request: %v", err)
	}
	return nil
}

// errMCPStdioRecvFull reports a server message dropped because the gateway's
// pump goroutine has not drained Recv. It is a distinct error because the
// caller must tell chronic backpressure (operationally interesting) apart from
// a backend that simply closed (routine).
var errMCPStdioRecvFull = errors.New("mcp stdio recv buffer is full")

// deliver hands one server-initiated message to the gateway's pump goroutine.
// It never blocks.
//
// It used to block, and that could wedge the whole agent. deliver runs on the
// recv loop, which carries every session and every protocol on this agent, and
// the send it made was unbounded in practice: b.done is closed only under
// b.mu, so a deliver parked on `b.recv <- msg` while holding that mutex could
// never be woken by the `<-b.done` arm it selected on. Only the pump draining
// Recv could release it — and the pump can be blocked on that same mutex,
// because mcpproxy answers a denied server-initiated request by calling Send
// (gateway/conn.go, inspectS2C) and Send takes b.mu. Recv loop waits on pump,
// pump waits on mutex, mutex is held by the recv loop. Nothing breaks that.
//
// So an undrained buffer drops the message. Dropping is not free: if the lost
// message was a tool-call response, the caller's request hangs until the
// gateway's RequestTimeout instead of answering. That is one stalled request
// against a permanently dead agent, which is not a close call.
//
// The lock stays. It is what keeps this send off a channel Close may already
// have closed, which would panic the gateway rather than merely stall it.
func (b *clientStdioBackend) deliver(msg []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return mcpbackend.ErrClosed
	}
	select {
	case b.recv <- msg:
		return nil
	default:
		b.dropped++
		return fmt.Errorf("%w, %d dropped on this backend", errMCPStdioRecvFull, b.dropped)
	}
}

// Close terminates the backend and asks the client to reap its child.
// Idempotent, and safe before Start: the recv and done channels must close
// either way or the gateway's pump blocks forever.
//
// sendMu is taken first and held across the MCPStdioClose write. That is what
// makes the reap final: an in-flight Send either completes before this (its
// request precedes the close on the wire, so the child it spawns is reaped) or
// finds closed=true and never writes. Releasing sendMu earlier would let a
// request slip out behind the close and strand a fresh MCP server on the
// user's machine (see writeRequest).
func (b *clientStdioBackend) Close() error {
	b.sendMu.Lock()
	defer b.sendMu.Unlock()

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	waiters := b.waiters
	b.waiters = map[string]chan *pb.Packet{}
	started := b.started
	close(b.recv)
	close(b.done)
	b.mu.Unlock()

	// Safe to close outside b.mu only because the swap above happened under
	// it: every reader of b.waiters holds b.mu across both the lookup and the
	// send (see processMCPStdioReply), so a reader either got the channel
	// before the swap — in which case its send completed before this line
	// could run — or acquires b.mu afterwards and finds an empty map. Waiters
	// select on done as well, so this only releases them promptly.
	for _, ch := range waiters {
		close(ch)
	}

	if !started {
		return nil
	}
	b.agent.mcpStdioBackends.Delete(b.key())

	if err := b.agent.client.Send(&pb.Packet{
		Type: pbclient.MCPStdioClose,
		Spec: b.newSpec(),
	}); err != nil {
		log.With("sid", b.sessionID).Warnf("failed asking client to close mcp stdio child: %v", err)
	}
	return nil
}

func (b *clientStdioBackend) key() string { return b.sessionID + ":" + b.backendID }

// newSpec copies the originating request's spec and stamps this backend's
// identity. The copy matters: the packet spec is shared with the writer that
// produced it, and mutating it in place would corrupt in-flight packets.
func (b *clientStdioBackend) newSpec() map[string][]byte {
	spec := make(map[string][]byte, len(b.spec)+3)
	for k, v := range b.spec {
		spec[k] = v
	}
	spec[pb.SpecGatewaySessionID] = []byte(b.sessionID)
	spec[pb.SpecMCPStdioBackendKey] = []byte(b.backendID)
	return spec
}

// processMCPStdioReply handles both kinds of packet arriving from the user's
// machine: the ack that unblocks a Send, and the MCP server's own output.
//
// They are told apart by the request id, which only an ack carries: server
// output is produced by the child's stdout reader, which has no idea which
// request (if any) a given line answers. It runs on the agent's recv loop and
// must not block: both handoffs here are non-blocking, dropping rather than
// waiting when the destination buffer is full (see deliver).
//
// Locking rule for the ack: b.mu is held across BOTH the waiter lookup and the
// send on the channel it returns, never just the lookup. Close empties
// b.waiters under b.mu and closes those channels only afterwards, so this is
// what keeps the send off an already-closed channel — the same defense deliver
// documents, for the same reason. A panic here is not one dead session: this
// runs on the agent's shared, unrecovered recv loop, so it takes down every
// session on the agent.
func (a *Agent) processMCPStdioReply(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	backendID := string(pkt.Spec[pb.SpecMCPStdioBackendKey])
	requestID := string(pkt.Spec[pb.SpecMCPStdioRequestKey])
	log := log.With("sid", sessionID, "backend", backendID, "req", requestID)

	obj, ok := a.mcpStdioBackends.Load(sessionID + ":" + backendID)
	if !ok {
		log.Debugf("dropping mcp stdio packet for unknown backend")
		return
	}
	backend, _ := obj.(*clientStdioBackend)
	if backend == nil {
		return
	}

	// No request id: this is the MCP server talking — a response, or a
	// server-initiated notification or request. It goes onto Recv, where the
	// gateway's S2C pipeline inspects it and matches responses by JSON-RPC id.
	if requestID == "" {
		switch err := backend.deliver(pkt.Payload); {
		case err == nil:
		case errors.Is(err, errMCPStdioRecvFull):
			// Backpressure, not teardown: the gateway's pump is not draining
			// this backend. Warn — the MCP client silently loses this message,
			// and if it was a tool-call response that request hangs until the
			// gateway's request timeout.
			log.Warnf("dropping mcp server message: %v", err)
		default:
			log.Debugf("dropping mcp server message: %v", err)
		}
		return
	}

	// The lock spans the send, not just the lookup. Holding it costs the recv
	// loop nothing: ackC is a cap-1 buffered channel with a default arm, so
	// the send below never waits — it either fits or is dropped. What it buys
	// is that Close cannot close this channel between the lookup and the send.
	backend.mu.Lock()
	defer backend.mu.Unlock()
	ch := backend.waiters[requestID]
	if ch == nil {
		// The Send already returned (its context expired, or the backend
		// closed). Nothing to wake.
		log.Debugf("dropping mcp stdio ack with no waiter")
		return
	}
	select {
	case ch <- pkt:
	default:
		log.Warnf("mcp stdio waiter already satisfied, dropping duplicate ack")
	}
}

// closeClientStdioBackends tears down every tunnelled backend of a session.
//
// gw.Close already closes each backend through its MCP sessions, but a backend
// whose gateway construction failed part-way is not reachable that way, and a
// leaked one would keep a child alive on the user's machine.
func (a *Agent) closeClientStdioBackends(sessionID string) {
	prefix := sessionID + ":"
	a.mcpStdioBackends.Range(func(key, value any) bool {
		k, _ := key.(string)
		if len(k) < len(prefix) || k[:len(prefix)] != prefix {
			return true
		}
		if b, _ := value.(*clientStdioBackend); b != nil {
			_ = b.Close()
		}
		return true
	})
}

// nextMCPStdioBackendID mints an id unique within a session, so two MCP
// sessions under one hoop session get two children instead of sharing one.
func (a *Agent) nextMCPStdioBackendID(sessionID string) string {
	obj, _ := a.mcpStdioSeq.LoadOrStore(sessionID, &atomic.Uint64{})
	seq := obj.(*atomic.Uint64)
	return strconv.FormatUint(seq.Add(1), 10)
}

// mcpStdioSpecFor returns the packet spec addressing a session's client
// stream. Only the session id is required for routing (the gateway resolves
// the proxy stream from it), so an absent entry is not an error.
func (a *Agent) mcpStdioSpecFor(sessionID string) map[string][]byte {
	return map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)}
}

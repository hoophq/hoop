//go:build integration

package integration

import (
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/agent/integration/testutil"
)

// These tests cover the agent's async SSH dispatch: SSHConnectionWrite and
// SessionClose packets run on per-packet goroutines (guarded by the
// per-session RW lock, per-connection write mutex, and singleflight group),
// so a slow upstream on one session cannot stall packet processing for
// other sessions on the same agent. Async dispatch is unconditional — the
// ENG-395 work introduced it behind the experimental.agent_async_ssh flag
// and ENG-470 promoted it to a permanent, always-on behavior.

// stallChunkSize is the per-Data packet size used by the parallel-stall
// test. Smaller chunks send more packets through libhoop's writerQueueCh,
// which makes the buffer fill the dominant blocking point rather than the
// SSH window.
const stallChunkSize = 16 * 1024

// TestSSH_BaseSmoke verifies that opening an SSH session and asking
// libhoop to open a "session" channel against the upstream sshd produces
// an observable upstream connection.
func TestSSH_BaseSmoke(t *testing.T) {
	ssh := testutil.StartSSH(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)

	sessionID := testutil.OpenSSHSession(t, tr, ssh.Host, ssh.Port, ssh.User, ssh.Password)
	demux := testutil.StartRecvDemux(t, tr)
	const connID = "conn-smoke"

	// First packet: OpenChannel("session"). Triggers libhoop.NewSSHProxy
	// + proxy.Run() inside the agent's singleflight closure, then the
	// payload is forwarded to libhoop which dials the upstream sshd and
	// opens an SSH session channel.
	testutil.SendSSHWrite(t, tr, sessionID, connID, testutil.SSHOpenChannelPayload(1))

	// Upstream connection should appear after the async dial finishes.
	// 10s is generous against CI cold-start scheduling.
	ssh.WaitForEstablishedSSHConnections(t, 1, 10*time.Second)
	_ = demux // demux exists to drain any libhoop responses cleanly during shutdown
}

// TestSSH_ConcurrentFirstPackets_SameConnID is the singleflight
// regression contract. Fires 50 goroutines that each send the first SSH
// packet for the same (sessionID, connID), then asserts that exactly
// one upstream SSH connection was created — proving that
// singleflight.Group correctly dedups concurrent proxy construction.
//
// Without singleflight, this scenario would race: each goroutine sees
// connStore.Get return nil, each calls libhoop.NewSSHProxy + proxy.Run,
// each dials the upstream. The connStore.Set is last-write-wins so
// orphan proxies remain pumping bytes into the gRPC stream.
//
// Async dispatch is what puts multiple goroutines into the same code
// path concurrently, which is exactly what exercises the singleflight
// dedup.
func TestSSH_ConcurrentFirstPackets_SameConnID(t *testing.T) {
	ssh := testutil.StartSSH(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)

	sessionID := testutil.OpenSSHSession(t, tr, ssh.Host, ssh.Port, ssh.User, ssh.Password)
	demux := testutil.StartRecvDemux(t, tr)
	const connID = "conn-shared"
	const numGoroutines = 50

	payload := testutil.SSHOpenChannelPayload(1)
	var wg sync.WaitGroup
	wg.Add(numGoroutines)
	startGate := make(chan struct{})
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			<-startGate
			testutil.SendSSHWrite(t, tr, sessionID, connID, payload)
		}()
	}
	close(startGate)
	wg.Wait()

	// Wait until the singleflight winner's dial completes and registers
	// in the container. WaitForEstablishedSSHConnections fails if the
	// count is anything other than 1 within the deadline — both 0
	// (nothing happened) and >1 (race fired) are failures.
	ssh.WaitForEstablishedSSHConnections(t, 1, 15*time.Second)

	// Re-check once after a brief settle delay. If the race fired but
	// race losers' duplicate connections close quickly (e.g., libhoop
	// errors out on duplicate session attempts), a single point-in-time
	// check could miss the spike. The second observation catches any
	// late arrivals.
	time.Sleep(500 * time.Millisecond)
	got := ssh.EstablishedSSHConnections(t)
	if got != 1 {
		t.Errorf("expected exactly 1 upstream SSH connection after burst, got %d "+
			"(singleflight should have deduped 50 concurrent first-packets to 1 dial)", got)
	}
	_ = demux
}

// TestSSH_SessionCloseRace verifies the per-session RW lock introduced in
// ENG-395. Opens an SSH session, fires a burst of writes, then injects
// SessionClose while writes may still be in flight on their per-packet
// goroutines.
//
// The invariant: SessionClose's Lock() must drain all in-flight RLock
// holders (the per-packet goroutines) before tearing down the
// connStore entries. A torn-down proxy mid-Write would manifest as a
// libhoop error sent back to the client; we don't assert that exactly,
// but we assert clean teardown: no goroutine leak and an observable
// SessionClose response.
//
// With -race enabled (when libhoop's pre-existing races are resolved in
// ENG-396), this test would also catch any concurrent access to torn-
// down state. We don't depend on -race here so this test is informative
// even on the default integration runner.
func TestSSH_SessionCloseRace(t *testing.T) {
	ssh := testutil.StartSSH(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)

	leak := testutil.SnapshotGoroutines(t, 30)

	sessionID := testutil.OpenSSHSession(t, tr, ssh.Host, ssh.Port, ssh.User, ssh.Password)
	demux := testutil.StartRecvDemux(t, tr)
	const connID = "conn-1"

	// Open the channel first so subsequent writes hit the cache-hit
	// fast path. This isolates the race we're testing (SessionClose
	// vs cached-proxy writes) from the singleflight slow path.
	testutil.SendSSHWrite(t, tr, sessionID, connID, testutil.SSHOpenChannelPayload(1))
	ssh.WaitForEstablishedSSHConnections(t, 1, 10*time.Second)

	// Burst Data packets. Each runs in its own goroutine holding the
	// session RLock briefly. The goal is to have several handlers in
	// flight when SessionClose arrives.
	for i := 0; i < 20; i++ {
		testutil.SendSSHWrite(t, tr, sessionID, connID,
			testutil.SSHDataPayload(1, []byte("noise-data-for-libhoop\r\n")))
	}

	// Fire SessionClose immediately after the burst. SessionClose is
	// also goroutine-dispatched and must wait for the RLock holders to
	// drain before sessionCleanup runs. SessionClose from client→agent
	// is one-way; the agent does not emit a SessionClose back, so we
	// observe cleanup via the upstream connection drop instead.
	closePkt := testutil.BuildSessionClosePacket(sessionID, "0")
	tr.Inject(closePkt)

	// Upstream connection should drop once libhoop's proxy.Close()
	// runs (kicked off in a goroutine inside sessionCleanup). If the
	// session RW lock didn't drain in-flight handlers, sessionCleanup
	// would race against the handlers, possibly orphaning the proxy
	// and leaving the connection alive.
	ssh.WaitForEstablishedSSHConnections(t, 0, 10*time.Second)
	_ = demux

	// Give libhoop's proxy goroutines a moment to fully exit after
	// the underlying SSH client closes. SnapshotGoroutines's
	// tolerance absorbs noise but flags a real leak.
	time.Sleep(1 * time.Second)
	leak.Assert(t)
}

// TestSSH_ParallelSessions_OneStalled is the customer-symptom regression
// contract for the parallel-session hang. It sets up two SSH sessions on
// the same agent:
//
//   - Session A goes through a SlowTCPProxy that we pause mid-stream.
//     Once paused, agent → libhoop → proxy bytes pile up: kernel TCP
//     send buffer (~64KB) fills, SSH channel window (2MB default) fills,
//     libhoop's ch.Write blocks, dataChannel (unbuffered) blocks,
//     writerQueueCh (100-slot buffer) fills, and finally the agent's
//     serverWriter.Write blocks. This is the production hang triggered
//     by SCP transfers against a slow remote.
//
//   - Session B is a normal SSH connection to the same sshd directly
//     (no slow proxy). Because SSHConnectionWrite packets are dispatched
//     on per-packet goroutines, session A's blocking Write runs in its
//     own goroutine and the recv loop keeps dispatching session B's
//     OpenChannel packet promptly.
//
// Asserts: Session B's upstream connection appears within a few seconds.
// Before async dispatch existed, session A's stalled Write held the recv
// loop and session B never connected — this test would fail.
func TestSSH_ParallelSessions_OneStalled(t *testing.T) {
	ssh := testutil.StartSSH(t)
	agent, tr := startAgent(t)

	slowProxy := testutil.StartSlowTCPProxy(t, ssh.ConnString())
	slowHost, slowPort := slowProxy.Addr()

	sidA := testutil.OpenSSHSession(t, tr, slowHost, slowPort, ssh.User, ssh.Password)
	sidB := testutil.OpenSSHSession(t, tr, ssh.Host, ssh.Port, ssh.User, ssh.Password)
	demux := testutil.StartRecvDemux(t, tr)
	const connA = "conn-a"
	const connB = "conn-b"

	testutil.SendSSHWrite(t, tr, sidA, connA, testutil.SSHOpenChannelPayload(1))
	ssh.WaitForEstablishedSSHConnections(t, 1, 10*time.Second)

	slowProxy.Pause()

	fillCtx := newFillContext(t)
	go fillSessionA(fillCtx, tr, sidA, connA)

	time.Sleep(1 * time.Second)

	// Session B should open promptly because its packet is dispatched
	// on its own goroutine; session A's stalled handler can't block
	// the recv loop.
	start := time.Now()
	testutil.SendSSHWrite(t, tr, sidB, connB, testutil.SSHOpenChannelPayload(1))
	ssh.WaitForEstablishedSSHConnections(t, 2, 10*time.Second)
	elapsed := time.Since(start)
	t.Logf("session B established in %v", elapsed)

	if elapsed > 5*time.Second {
		t.Errorf("session B took %v to establish; expected <5s. "+
			"Session A's stall appears to still be blocking session B.", elapsed)
	}
	_ = demux

	// Drain order matters: stop the fill goroutine, resume the proxy
	// so libhoop can drain in-flight writes, send SessionClose for
	// both sessions so libhoop's proxies close their writerQueueCh
	// cleanly (their consumer-side loops exit on EOF rather than
	// racing with concurrent Write calls), then shut down the agent.
	fillCtx.cancel()
	slowProxy.Resume()
	teardownStalledSessions(t, tr, sidA, sidB)
	shutdownAgent(t, agent, tr)
}

// teardownStalledSessions sends SessionClose for both sessions and
// waits briefly so libhoop's SSH proxies can wind down cleanly. This
// avoids two pre-existing libhoop races (close-of-closed-channel and
// send-on-closed-channel in Proxy.Write) that fire when many writes
// are blocked in writerQueueCh at the moment of agent shutdown.
//
// The libhoop fix tracked in ENG-396 will make this helper redundant.
func teardownStalledSessions(t *testing.T, tr *testutil.MockTransport, sids ...string) {
	t.Helper()
	for _, sid := range sids {
		tr.Inject(testutil.BuildSessionClosePacket(sid, "0"))
	}
	// Give the SessionClose packets time to be processed and for
	// libhoop's Proxy.Close to drain its queue. 2 seconds is enough
	// for thousands of small in-flight packets on a local CI runner.
	time.Sleep(2 * time.Second)
}

// fillContext encapsulates the lifecycle of the background goroutine
// that pumps data into session A. The cancel function is invoked by
// the test at teardown so the goroutine exits promptly.
type fillContext struct {
	t      *testing.T
	done   chan struct{}
	cancel func()
}

func newFillContext(t *testing.T) *fillContext {
	done := make(chan struct{})
	return &fillContext{
		t:    t,
		done: done,
		cancel: sync.OnceFunc(func() {
			close(done)
		}),
	}
}

// fillSessionA pushes Data packets into session A's channel to
// saturate libhoop's writerQueueCh (100-slot buffer). It sends a
// bounded burst then stops; the goal is to put libhoop's Write into
// the blocking state, not to exhaust MockTransport's recvCh — the
// test needs recvCh to retain room for session B's OpenChannel
// packet.
//
// The burst size of writerQueueCap-ish packets fills the libhoop
// queue. The few packets after that spawn blocked goroutines. Either
// way, the test's next injection (session B) goes into recvCh with
// room to spare.
//
// Includes a small inter-packet delay so the burst is observable
// rather than instantaneous — easier to reason about timing in CI
// where scheduling jitter is higher.
func fillSessionA(fc *fillContext, tr *testutil.MockTransport, sid, connID string) {
	defer func() {
		// SendSSHWrite calls tr.Inject which panics after 10s of
		// blocking. Recover to keep the test process alive.
		_ = recover()
	}()

	payload := make([]byte, stallChunkSize)
	for i := range payload {
		payload[i] = byte(i & 0xff)
	}
	dataPkt := testutil.SSHDataPayload(1, payload)

	// libhoop's writerQueueCh capacity is 100. We send ~120 packets
	// so the queue saturates and the agent's Write is blocked on
	// packet 101+, but we leave recvCh (also 100 slots) mostly empty
	// so the test's session B injection has room.
	const burst = 120
	for sent := 0; sent < burst; sent++ {
		select {
		case <-fc.done:
			return
		default:
		}
		testutil.SendSSHWrite(fc.t, tr, sid, connID, dataPkt)
		time.Sleep(5 * time.Millisecond)
	}
}

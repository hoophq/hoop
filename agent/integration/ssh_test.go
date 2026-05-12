//go:build integration

package integration

import (
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/agent/integration/testutil"
)

// asyncSSHFlag is the experimental flag introduced by ENG-395 that gates
// per-packet goroutine dispatch for SSHConnectionWrite and SessionClose.
// When false, packets process synchronously on the recv loop; when true,
// each packet runs in its own goroutine guarded by the per-session RW
// lock and per-connection write mutex.
const asyncSSHFlag = "experimental.agent_async_ssh"

const sshTestTimeout = 30 * time.Second

// stallFillBytes is the payload size used by the parallel-stall test to
// saturate libhoop's writerQueueCh and the kernel TCP buffers between
// agent and the (paused) slow proxy. SSH's default channel window is
// 2 MiB; ~4 MiB of data ensures we push past it, fill the agent-side
// kernel send buffer, fill libhoop's per-channel send loop, and back
// pressure into the writerQueueCh buffer (capacity 100).
const stallFillBytes = 4 * 1024 * 1024

// stallChunkSize is the per-Data packet size. Smaller chunks send more
// packets through libhoop's writerQueueCh, which makes the buffer fill
// the dominant blocking point rather than the SSH window.
const stallChunkSize = 16 * 1024

// TestSSH_BaseSmoke_FlagOff verifies that with async-SSH dispatch off,
// opening an SSH session and asking libhoop to open a "session" channel
// against the upstream sshd produces an observable upstream connection.
// This is the regression contract: the flag-off path must continue to
// behave exactly as main does today.
func TestSSH_BaseSmoke_FlagOff(t *testing.T) {
	ssh := testutil.StartSSH(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)

	// Reset flag state explicitly. featureflagstate is process-global
	// and other tests in the suite may have left it on.
	testutil.SetAgentFeatureFlags(t, tr, map[string]bool{asyncSSHFlag: false})

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

// TestSSH_BaseSmoke_FlagOn is the flag-on twin of the smoke test.
// Asserts that the async dispatch path doesn't break the happy case.
func TestSSH_BaseSmoke_FlagOn(t *testing.T) {
	ssh := testutil.StartSSH(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)

	testutil.SetAgentFeatureFlags(t, tr, map[string]bool{asyncSSHFlag: true})

	sessionID := testutil.OpenSSHSession(t, tr, ssh.Host, ssh.Port, ssh.User, ssh.Password)
	demux := testutil.StartRecvDemux(t, tr)
	const connID = "conn-smoke-async"

	testutil.SendSSHWrite(t, tr, sessionID, connID, testutil.SSHOpenChannelPayload(1))
	ssh.WaitForEstablishedSSHConnections(t, 1, 10*time.Second)
	_ = demux
}

// TestSSH_ConcurrentFirstPackets_SameConnID_FlagOn is the singleflight
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
// This test only runs under flag-on because async dispatch is what
// puts multiple goroutines into the same code path concurrently. With
// sync dispatch the recv loop processes packets serially and the race
// can't fire — but that's a behavioral coincidence, not a fix.
func TestSSH_ConcurrentFirstPackets_SameConnID_FlagOn(t *testing.T) {
	ssh := testutil.StartSSH(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)

	testutil.SetAgentFeatureFlags(t, tr, map[string]bool{asyncSSHFlag: true})

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

// TestSSH_SessionCloseRace_FlagOn verifies the per-session RW lock
// introduced in ENG-395. Opens an SSH session, fires a burst of writes,
// then injects SessionClose while writes may still be in flight on
// their per-packet goroutines.
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
func TestSSH_SessionCloseRace_FlagOn(t *testing.T) {
	ssh := testutil.StartSSH(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)

	testutil.SetAgentFeatureFlags(t, tr, map[string]bool{asyncSSHFlag: true})

	leak := testutil.SnapshotGoroutines(t, 30)

	sessionID := testutil.OpenSSHSession(t, tr, ssh.Host, ssh.Port, ssh.User, ssh.Password)
	demux := testutil.StartRecvDemux(t, tr)
	const connID = "conn-1"

	// Open the channel first so subsequent writes hit the cache-hit
	// fast path. This isolates the race we're testing (SessionClose
	// vs cached-proxy writes) from the singleflight slow path.
	testutil.SendSSHWrite(t, tr, sessionID, connID, testutil.SSHOpenChannelPayload(1))
	ssh.WaitForEstablishedSSHConnections(t, 1, 10*time.Second)

	// Burst Data packets. With async dispatch, each runs in its own
	// goroutine holding the session RLock briefly. The goal is to
	// have several handlers in flight when SessionClose arrives.
	for i := 0; i < 20; i++ {
		testutil.SendSSHWrite(t, tr, sessionID, connID,
			testutil.SSHDataPayload(1, []byte("noise-data-for-libhoop\r\n")))
	}

	// Fire SessionClose immediately after the burst. With async
	// dispatch, SessionClose is also goroutine-dispatched and must
	// wait for the RLock holders to drain before sessionCleanup runs.
	// SessionClose from client→agent is one-way; the agent does not
	// emit a SessionClose back, so we observe cleanup via the upstream
	// connection drop instead.
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

// TestSSH_ParallelSessions_OneStalled_FlagOff is the customer-symptom
// reproduction in its "bug present" form. It sets up two SSH sessions
// on the same agent:
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
//     (no slow proxy). With sync packet dispatch (flag off), the recv
//     loop is wedged inside Session A's blocking Write, so Session B's
//     OpenChannel packet cannot be processed — Session B never sees
//     its upstream connection appear.
//
// Asserts: Session B's upstream connection appears within 5 seconds.
// With the flag off, this assertion fails because the agent's recv
// loop is stuck on Session A. Under flag-on (the next test), it
// succeeds because Session A's blocking Write runs in its own
// goroutine and the recv loop continues dispatching Session B.
//
// This test pairs with TestSSH_ParallelSessions_OneStalled_FlagOn:
// together they form the regression contract for the customer's bug.
func TestSSH_ParallelSessions_OneStalled_FlagOff(t *testing.T) {
	ssh := testutil.StartSSH(t)
	agent, tr := startAgent(t)

	testutil.SetAgentFeatureFlags(t, tr, map[string]bool{asyncSSHFlag: false})

	// The slow proxy points at the real sshd. SessionA dials the
	// proxy; SessionB dials the sshd directly so we can isolate the
	// stall to SessionA's path.
	slowProxy := testutil.StartSlowTCPProxy(t, ssh.ConnString())
	slowHost, slowPort := slowProxy.Addr()

	sidA := testutil.OpenSSHSession(t, tr, slowHost, slowPort, ssh.User, ssh.Password)
	sidB := testutil.OpenSSHSession(t, tr, ssh.Host, ssh.Port, ssh.User, ssh.Password)
	demux := testutil.StartRecvDemux(t, tr)
	const connA = "conn-a"
	const connB = "conn-b"

	// Open a channel on session A. This completes the SSH handshake
	// and gives us a channel we can pump data into. The proxy is not
	// yet paused, so the handshake finishes normally.
	testutil.SendSSHWrite(t, tr, sidA, connA, testutil.SSHOpenChannelPayload(1))
	ssh.WaitForEstablishedSSHConnections(t, 1, 10*time.Second)

	// Pause the proxy. Subsequent writes from the agent will reach
	// the proxy but not the sshd; the proxy holds them in its kernel
	// receive buffer until Resume.
	slowProxy.Pause()

	// Pump enough data through session A to saturate every buffer in
	// the chain (kernel send + SSH window + libhoop's queues). Once
	// any of these is full, the next agent Write blocks the recv
	// loop. We fire-and-forget — the test does not wait for these
	// goroutines.
	fillCtx := newFillContext(t)
	go fillSessionA(fillCtx, tr, sidA, connA)

	// Give the fill loop a moment to actually push bytes and trigger
	// the saturation. 1 second is a generous lower bound — typical
	// saturation occurs within tens of milliseconds with 16 KiB
	// chunks but CI scheduling can stretch it.
	time.Sleep(1 * time.Second)

	// Now try to open a channel on session B. With the recv loop
	// blocked on session A's stalled Write, this packet cannot be
	// processed — no second upstream SSH connection appears.
	testutil.SendSSHWrite(t, tr, sidB, connB, testutil.SSHOpenChannelPayload(1))

	// We expect the connection count to stay at 1 (only session A's
	// proxy connection). Wait up to 5 seconds; if session B somehow
	// got through (the bug is gone), this fails.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got := ssh.EstablishedSSHConnections(t)
		if got >= 2 {
			t.Errorf("flag-off: session B opened an SSH connection (count=%d) "+
				"despite session A stalling — the recv loop is no longer "+
				"synchronously blocked on Write. Regression contract violated.",
				got)
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("flag-off: confirmed session B is blocked behind session A's stalled Write")
	_ = demux

	// Drain order matters: stop the fill goroutine, resume the proxy
	// so libhoop can drain in-flight writes, send SessionClose for
	// both sessions so libhoop's proxies close their writerQueueCh
	// cleanly (their consumer-side loops exit on EOF rather than
	// racing with concurrent Write calls), then shut down the agent.
	//
	// This dance works around two pre-existing libhoop races where
	// concurrent Write calls during teardown can either send on a
	// closed channel or double-close it. See ENG-396 for the libhoop
	// fix needed to make this dance unnecessary.
	fillCtx.cancel()
	slowProxy.Resume()
	teardownStalledSessions(t, tr, sidA, sidB)
	shutdownAgent(t, agent, tr)
}

// TestSSH_ParallelSessions_OneStalled_FlagOn is the "fix verified" twin
// of the customer-symptom test. Same setup as the flag-off variant; the
// difference is that SSHConnectionWrite packets are now dispatched on
// per-packet goroutines, so session A's blocking Write doesn't hold up
// the recv loop. Session B's OpenChannel packet reaches its handler
// promptly and the upstream SSH connection is established within a few
// seconds.
func TestSSH_ParallelSessions_OneStalled_FlagOn(t *testing.T) {
	ssh := testutil.StartSSH(t)
	agent, tr := startAgent(t)

	testutil.SetAgentFeatureFlags(t, tr, map[string]bool{asyncSSHFlag: true})

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
	t.Logf("flag-on: session B established in %v", elapsed)

	if elapsed > 5*time.Second {
		t.Errorf("flag-on: session B took %v to establish; expected <5s. "+
			"Session A's stall appears to still be blocking session B.", elapsed)
	}
	_ = demux

	// Same drain dance as the flag-off twin — see comment there.
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
// queue. The few packets after that block the agent's Write (flag
// off) or spawn blocked goroutines (flag on). Either way, the test's
// next injection (session B) goes into recvCh with room to spare.
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

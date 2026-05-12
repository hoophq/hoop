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

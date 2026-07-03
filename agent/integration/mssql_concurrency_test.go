//go:build integration

package integration

import (
	"fmt"
	"testing"
	"time"

	"github.com/hoophq/hoop/agent/integration/testutil"
)

// TestMSSQL_MultipleSessions opens several independent MSSQL sessions on the
// same agent and exercises each one's work in turn. Each session has its own
// bridged connection, its own (sid, connID), and its own upstream SQL Server
// backend.
//
// The invariant: independent sessions coexisting on the same agent must not
// corrupt each other's state. With the blockingReader race fixed (ENG-396)
// this runs clean under -race; before the fix, concurrent Read/Write on each
// proxy's bytes.Buffer would trip the detector.
//
// Why the work is driven sequentially, not concurrently: the agent's recv
// loop dispatches packets synchronously (only SSH has async dispatch).
// Driving all sessions' drivers at once would
// have each session's handshake contend for the single recv loop, so under
// load one session can starve past the driver timeout — the same limitation
// TestPG_ParallelSessions_OneHangs and the MySQL equivalent document with
// t.Skip. The state-isolation invariant this test cares about does not
// require concurrent throughput: every session is opened and bridged up
// front (so all proxies coexist in the connStore at once), then each
// session's query runs in turn. The proxies are all live simultaneously,
// which is what -race needs to observe cross-session state corruption.
func TestMSSQL_MultipleSessions(t *testing.T) {
	mc := testutil.StartMSSQL(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)

	const numSessions = 4

	// Open all sessions *before* starting the demux. OpenMSSQLSession
	// reads SessionOpenOK directly off MockTransport.RecvCh(); once the
	// demux is draining that same channel it would steal the replies, and
	// two direct readers would steal from each other. So we serialize the
	// opens up front, then start the demux, then dial the bridges (which
	// need the demux running for their inbound pumps).
	sessionIDs := make([]string, numSessions)
	for i := 0; i < numSessions; i++ {
		sessionIDs[i] = testutil.OpenMSSQLSession(t, tr, mc)
	}

	demux := testutil.StartRecvDemux(t, tr)

	// Dial every bridge up front so all sessions' proxies are live in the
	// agent's connStore simultaneously — that coexistence is what lets
	// -race surface any cross-session state corruption.
	clients := make([]*testutil.PipedMSSQLClient, numSessions)
	for i := 0; i < numSessions; i++ {
		clients[i] = testutil.DialPipedMSSQL(t, tr, demux, mc, sessionIDs[i], fmt.Sprintf("conn-multi-%d", i))
	}

	// Drive each session's work in turn. Sequential driver traffic avoids
	// starving the synchronous recv loop while still exercising every
	// session's proxy independently.
	for i := 0; i < numSessions; i++ {
		client := clients[i]
		if err := client.PingWithTimeout(mssqlTestTimeout); err != nil {
			t.Errorf("session %d ping: %v", i, err)
			continue
		}
		var n int
		if err := client.DB.QueryRow("SELECT @p1 + @p2", i, 100).Scan(&n); err != nil {
			t.Errorf("session %d query: %v", i, err)
			continue
		}
		if n != i+100 {
			t.Errorf("session %d: expected %d, got %d", i, i+100, n)
		}
	}
}

// TestMSSQL_SessionCloseRace opens a connection, fires a slow query, then
// sends SessionClose while the query is in flight. Asserts teardown is
// clean: the upstream backend exits and no goroutines leak.
//
// The invariant: a SessionClose arriving while a packet handler is still
// running for that session must not leave the system with orphaned
// upstream connections or leaked goroutines.
func TestMSSQL_SessionCloseRace(t *testing.T) {
	mc := testutil.StartMSSQL(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)

	leak := testutil.SnapshotGoroutines(t, 25)

	sessionID := testutil.OpenMSSQLSession(t, tr, mc)
	demux := testutil.StartRecvDemux(t, tr)
	connID := "conn-close-race"
	client := testutil.DialPipedMSSQL(t, tr, demux, mc, sessionID, connID)

	// Establish the connection up front so the SessionClose races a
	// cached-proxy query path, not the initial handshake.
	if err := client.PingWithTimeout(mssqlTestTimeout); err != nil {
		t.Fatalf("initial ping: %v", err)
	}

	// Fire a ~2s query in the background. WAITFOR DELAY runs on the
	// upstream; the agent's recv loop returns after queuing the query
	// bytes into libhoop's blockingReader, so SessionClose can land while
	// the query is still executing server-side.
	queryDone := make(chan struct{})
	go func() {
		defer close(queryDone)
		var dummy int
		_ = client.DB.QueryRow("WAITFOR DELAY '00:00:02'; SELECT 1").Scan(&dummy)
	}()

	// Let the query reach the server, then close the session underneath it.
	time.Sleep(200 * time.Millisecond)
	tr.Inject(testutil.BuildSessionClosePacket(sessionID, "0"))

	// The query goroutine should unblock (with or without error) rather
	// than hang forever.
	select {
	case <-queryDone:
	case <-time.After(mssqlTestTimeout):
		t.Fatal("query did not return after SessionClose — possible wedge")
	}

	// Upstream backend should fully disconnect within a few seconds.
	mc.WaitForConnectionCount(t, 0, 15*time.Second)

	shutdownAgent(t, agent, tr)

	// No significant goroutine leak after teardown. Tolerance absorbs
	// docker-client / testcontainer background noise.
	leak.Assert(t)
}

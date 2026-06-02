//go:build integration

package integration

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/agent/integration/testutil"
)

// TestMySQL_ParallelSessions runs two independent MySQL sessions on the
// same agent concurrently and asserts both complete their work. Each
// session has its own bridged connection, its own (sid, connID), and its
// own upstream MariaDB backend.
//
// The invariant: independent sessions on the same agent must not corrupt
// each other's state. With the blockingReader race fixed (ENG-396) this
// runs clean under -race; before the fix, concurrent Read/Write on each
// proxy's bytes.Buffer would trip the detector.
func TestMySQL_ParallelSessions(t *testing.T) {
	mc := testutil.StartMySQL(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)

	const numSessions = 4

	// Open all sessions *before* starting the demux. OpenMySQLSession
	// reads SessionOpenOK directly off MockTransport.RecvCh(); once the
	// demux is draining that same channel it would steal the replies, and
	// two direct readers would steal from each other. So we serialize the
	// opens up front, then start the demux, then dial the bridges (which
	// need the demux running for their inbound pumps).
	sessionIDs := make([]string, numSessions)
	for i := 0; i < numSessions; i++ {
		sessionIDs[i] = testutil.OpenMySQLSession(t, tr, mc)
	}

	demux := testutil.StartRecvDemux(t, tr)

	clients := make([]*testutil.PipedMySQLClient, numSessions)
	for i := 0; i < numSessions; i++ {
		clients[i] = testutil.DialPipedMySQL(t, tr, demux, mc, sessionIDs[i], fmt.Sprintf("conn-parallel-%d", i))
	}

	var wg sync.WaitGroup
	errCh := make(chan error, numSessions)
	for i := 0; i < numSessions; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			client := clients[i]
			if err := client.PingWithTimeout(mysqlTestTimeout); err != nil {
				errCh <- fmt.Errorf("session %d ping: %w", i, err)
				return
			}
			var n int
			if err := client.DB.QueryRow("SELECT ? + ?", i, 100).Scan(&n); err != nil {
				errCh <- fmt.Errorf("session %d query: %w", i, err)
				return
			}
			if n != i+100 {
				errCh <- fmt.Errorf("session %d: expected %d, got %d", i, i+100, n)
				return
			}
		}(i)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Error(err)
		}
	}
}

// TestMySQL_SessionCloseRace opens a connection, fires a slow query, then
// sends SessionClose while the query is in flight. Asserts teardown is
// clean: the upstream backend exits and no goroutines leak.
//
// The invariant: a SessionClose arriving while a packet handler is still
// running for that session must not leave the system with orphaned
// upstream connections or leaked goroutines.
func TestMySQL_SessionCloseRace(t *testing.T) {
	mc := testutil.StartMySQL(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)

	leak := testutil.SnapshotGoroutines(t, 25)

	sessionID := testutil.OpenMySQLSession(t, tr, mc)
	demux := testutil.StartRecvDemux(t, tr)
	connID := "conn-close-race"
	client := testutil.DialPipedMySQL(t, tr, demux, mc, sessionID, connID)

	// Establish the connection up front so the SessionClose races a
	// cached-proxy query path, not the initial handshake.
	if err := client.PingWithTimeout(mysqlTestTimeout); err != nil {
		t.Fatalf("initial ping: %v", err)
	}

	// Fire a ~2s query in the background. SLEEP runs on the upstream;
	// the agent's recv loop returns after queuing the query bytes into
	// libhoop's blockingReader, so SessionClose can land while the
	// query is still executing server-side.
	queryDone := make(chan struct{})
	go func() {
		defer close(queryDone)
		var slept int
		_ = client.DB.QueryRow("SELECT SLEEP(2)").Scan(&slept)
	}()

	// Let the query reach the server, then close the session underneath it.
	time.Sleep(200 * time.Millisecond)
	tr.Inject(testutil.BuildSessionClosePacket(sessionID, "0"))

	// The query goroutine should unblock (with or without error) rather
	// than hang forever.
	select {
	case <-queryDone:
	case <-time.After(mysqlTestTimeout):
		t.Fatal("query did not return after SessionClose — possible wedge")
	}

	// Upstream backend should fully disconnect within a few seconds.
	mc.WaitForProcessCount(t, 0, 10*time.Second)

	shutdownAgent(t, agent, tr)

	// No significant goroutine leak after teardown. Tolerance absorbs
	// docker-client / testcontainer background noise.
	leak.Assert(t)
}

// TestMySQL_ParallelSessions_OneHangs is the MySQL analogue of the SSH
// customer-symptom test: two sessions, one pointed at a non-routable
// address so its first upstream dial blocks, the other at a real MariaDB.
// It asserts the fast session is unaffected by the slow one.
//
// Skipped: the agent's async-dispatch flag (experimental.agent_async_ssh)
// only covers SSH packets — MySQL packets are still processed
// synchronously, so the slow session's blocking dial holds up the recv
// loop and the fast session queues behind it. Unskip when async dispatch
// is extended to MySQL.
func TestMySQL_ParallelSessions_OneHangs(t *testing.T) {
	t.Skip("MySQL packets are dispatched synchronously (async flag is SSH-only); " +
		"a slow upstream dial blocks the recv loop. Unskip when async dispatch covers MySQL.")
}

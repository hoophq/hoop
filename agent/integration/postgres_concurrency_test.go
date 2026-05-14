//go:build integration

package integration

import (
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/agent/integration/testutil"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
)

// TestPG_ConcurrentFirstPackets_SameConnID fires 50 goroutines that each
// send the first PG packet (SSLRequest + StartupMessage) for the same
// (sessionID, connID) tuple, then asserts exactly one Postgres backend
// connection is dialed.
//
// The invariant being protected: concurrent first-packets for the same
// connection must deduplicate at the proxy-creation step. A naive
// implementation that does connStore.Get → dial → connStore.Set without
// a synchronization primitive will race — multiple goroutines miss the
// cache, all dial the upstream, last-write-wins leaves orphan proxies
// still pumping bytes into the gRPC stream.
func TestPG_ConcurrentFirstPackets_SameConnID(t *testing.T) {
	pg := testutil.StartPostgres(t)
	agent, tr := startAgent(t)

	// OpenPGSession reads SessionOpenOK directly from the transport.
	// Start the demux only after the session is open so it doesn't
	// intercept the handshake reply.
	sessionID := testutil.OpenPGSession(t, tr, pg.Host, pg.Port, pg.User, pg.Password, pg.Database)
	demux := testutil.StartRecvDemux(t, tr)
	connID := "conn-shared"

	const numGoroutines = 50
	handshake := append(testutil.PGSSLRequest(), testutil.PGStartupMessage(pg.User, pg.Database)...)

	var wg sync.WaitGroup
	wg.Add(numGoroutines)
	startGate := make(chan struct{})
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			<-startGate
			pkt := &pb.Packet{
				Type: pbagent.PGConnectionWrite,
				Spec: map[string][]byte{
					pb.SpecGatewaySessionID:   []byte(sessionID),
					pb.SpecClientConnectionID: []byte(connID),
				},
				Payload: handshake,
			}
			tr.Inject(pkt)
		}()
	}
	close(startGate)
	wg.Wait()

	// Drain until ReadyForQuery on the shared conn. We don't assert on
	// response content; only the upstream-connection count matters here.
	testutil.WaitForReadyOnConn(t, demux, connID, pgTestTimeout)

	// Settle: even after the agent has buffered Z, the PG backend may
	// still be in handshake. Give it a moment to land in pg_stat_activity.
	time.Sleep(500 * time.Millisecond)

	got := testutil.PGBackendCount(t, pg)
	if got != 1 {
		t.Errorf("expected exactly 1 upstream PG backend, got %d (duplicate-proxy race?)", got)
	}

	shutdownAgent(t, agent, tr)
}

// TestPG_SessionCloseRace opens a connection, fires a query, then sends
// SessionClose while the query is in flight. Asserts that teardown is
// clean: upstream backend exits, no goroutine leak.
//
// The invariant: a SessionClose arriving while a packet handler is still
// running for that session must wait for the handler to finish, or at
// minimum must not leave the system in a partially-torn-down state with
// orphaned upstream connections or leaked goroutines.
func TestPG_SessionCloseRace(t *testing.T) {
	pg := testutil.StartPostgres(t)
	agent, tr := startAgent(t)

	leak := testutil.SnapshotGoroutines(t, 20)

	sessionID := testutil.OpenPGSession(t, tr, pg.Host, pg.Port, pg.User, pg.Password, pg.Database)
	demux := testutil.StartRecvDemux(t, tr)
	connID := "conn-1"

	// Establish the connection first so subsequent writes hit the cached
	// proxy path. Isolates the SessionClose race from any duplicate-proxy
	// interactions on the create path.
	testutil.SendPGConnectHandshake(t, tr, sessionID, connID, pg.User, pg.Database)
	testutil.WaitForReadyOnConn(t, demux, connID, pgTestTimeout)

	// Fire a query that takes ~1s on the upstream so SessionClose has a
	// chance to arrive while it's in flight. pg_sleep runs in libhoop's
	// goroutine, not the recv loop — the recv loop returns after queuing
	// the query bytes into libhoop's blockingReader.
	testutil.SendPGWrite(t, tr, sessionID, connID, testutil.PGSimpleQuery("SELECT pg_sleep(1)"))

	// SessionClose fires 100ms in, during the query's pg_sleep.
	time.Sleep(100 * time.Millisecond)
	closePkt := testutil.BuildSessionClosePacket(sessionID, "0")
	tr.Inject(closePkt)

	// Drain whatever the agent emits afterward: query response, error,
	// session-close acks. We don't assert on content; the cleanup
	// invariants are what matter.
	deadline := time.After(pgTestTimeout)
drain:
	for {
		select {
		case <-demux.Channel(connID):
		case <-demux.SessionChannel():
		case <-deadline:
			break drain
		case <-time.After(500 * time.Millisecond):
			// idle long enough — done draining
			break drain
		}
	}

	// Upstream backend should fully disconnect within a few seconds.
	testutil.WaitForPGBackendCount(t, pg, 0, 10*time.Second)

	shutdownAgent(t, agent, tr)

	// No significant goroutine leak after teardown. Tolerance is generous
	// to absorb testcontainer / docker-client noise.
	leak.Assert(t)
}

// TestPG_ParallelSessions_OneHangs opens two independent sessions on the
// same agent. One points at 192.0.2.1 (TEST-NET-1, RFC 5737, guaranteed
// non-routable) so libhoop's 10-second net.DialTimeout blocks on its
// first PGConnectionWrite. The other points at a real Postgres container.
//
// Asserts the fast session's handshake completes in under 2 seconds —
// independent of the slow session's stalled dial.
//
// The invariant: a slow or stalled operation on one session must not
// block packet processing for other sessions on the same agent.
//
// This test currently fails because the agent's recv loop processes
// packets synchronously, so the slow session's dial blocks the fast
// session's packet behind it. The test is skipped until the dispatch
// model changes; unskip it once the agent dispatches concurrent
// sessions independently.
func TestPG_ParallelSessions_OneHangs(t *testing.T) {
	t.Skip("known failure: agent recv loop is synchronous; fast session blocks behind slow session's dial. " +
		"Unskip when the dispatch model supports independent per-session processing.")

	pg := testutil.StartPostgres(t)
	agent, tr := startAgent(t)

	// Two independent sessions. The slow one points at a non-routable
	// address so libhoop's dial blocks until its 10s timeout fires.
	// SessionOpen itself doesn't dial — only PGConnectionWrite does —
	// so both OpenPGSession calls return quickly.
	sidSlow := testutil.OpenPGSession(t, tr, "192.0.2.1", "5432", pg.User, pg.Password, pg.Database)
	sidFast := testutil.OpenPGSession(t, tr, pg.Host, pg.Port, pg.User, pg.Password, pg.Database)
	demux := testutil.StartRecvDemux(t, tr)

	connSlow := "conn-slow"
	connFast := "conn-fast"

	// Fire the slow handshake. The agent's recv loop picks it up, calls
	// processPGProtocol → libhoop.Postgres() → net.DialTimeout("192.0.2.1:5432",
	// 10s). The recv loop is now blocked here.
	go func() {
		testutil.SendPGConnectHandshake(t, tr, sidSlow, connSlow, pg.User, pg.Database)
	}()

	// Brief delay so the slow handshake reaches the dial state before
	// we fire the fast one. 200ms is generous — the recv loop picks up
	// the slow packet within a few microseconds.
	time.Sleep(200 * time.Millisecond)

	// Fire the fast handshake. Under sync dispatch, this packet sits in
	// recvCh until the slow dial times out. Under async dispatch, it
	// gets its own goroutine and proceeds immediately.
	start := time.Now()
	testutil.SendPGConnectHandshake(t, tr, sidFast, connFast, pg.User, pg.Database)

	fastReady := make(chan struct{})
	go func() {
		testutil.WaitForReadyOnConn(t, demux, connFast, 15*time.Second)
		close(fastReady)
	}()

	select {
	case <-fastReady:
		elapsed := time.Since(start)
		t.Logf("fast session ready in %v", elapsed)
		// 2 seconds is generous for async — typical PG handshake completes
		// in ~50-200ms locally. The slow session's 10s dial timeout sets
		// the upper bound for the sync path.
		if elapsed > 2*time.Second {
			t.Errorf("fast session ready in %v; expected <2s — slow session is blocking the fast one", elapsed)
		}
	case <-time.After(15 * time.Second):
		t.Errorf("fast session never became ready — agent recv loop appears wedged")
	}

	// Drain so shutdown is clean. The slow session is still stuck in
	// libhoop's dial; closing the agent below will tear it down.
	drainDeadline := time.After(3 * time.Second)
drain:
	for {
		select {
		case <-demux.Channel(connSlow):
		case <-demux.Channel(connFast):
		case <-demux.SessionChannel():
		case <-drainDeadline:
			break drain
		case <-time.After(200 * time.Millisecond):
			break drain
		}
	}

	shutdownAgent(t, agent, tr)
}

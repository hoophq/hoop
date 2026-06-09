//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"github.com/hoophq/hoop/agent/integration/testutil"
)

// TestMongoDB_MultipleSessions opens several independent MongoDB sessions on
// the same agent and exercises each one's work in turn. Each session has its
// own bridged client (with its own pool of connections, each a distinct
// connID) and its own upstream MongoDB backend connections.
//
// The invariant: independent sessions coexisting on the same agent must not
// corrupt each other's state. With the blockingReader race fixed (ENG-396)
// this runs clean under -race; before the fix, concurrent Read/Write on each
// proxy's bytes.Buffer would trip the detector.
//
// Why the work is driven sequentially, not concurrently: the agent's recv
// loop dispatches packets synchronously (only SSH has async dispatch, behind
// experimental.agent_async_ssh). Driving all sessions' drivers at once would
// have each session's SCRAM handshake and multi-connection pool contend for
// the single recv loop, so under load one session can starve past the driver
// timeout — the same limitation TestPG_ParallelSessions_OneHangs and the
// MySQL equivalent document with t.Skip. The state-isolation invariant this
// test cares about does not require concurrent throughput: every session is
// opened and bridged up front (so all proxies coexist in the connStore at
// once), then each session's queries run in turn. The proxies are all live
// simultaneously, which is what -race needs to observe cross-session state
// corruption.
func TestMongoDB_MultipleSessions(t *testing.T) {
	mc := testutil.StartMongoDB(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)

	const numSessions = 4

	// Open all sessions *before* starting the demux. OpenMongoSession
	// reads SessionOpenOK directly off MockTransport.RecvCh(); once the
	// demux is draining that same channel it would steal the replies, and
	// two direct readers would steal from each other. So we serialize the
	// opens up front, then start the demux, then dial the bridges (which
	// need the demux running for their inbound pumps).
	sessionIDs := make([]string, numSessions)
	for i := 0; i < numSessions; i++ {
		sessionIDs[i] = testutil.OpenMongoSession(t, tr, mc)
	}

	demux := testutil.StartRecvDemux(t, tr)

	// Build every session's bridged client up front so all sessions coexist
	// on the agent. Each client's topology monitor establishes at least one
	// bridged connection (and thus an agent-side proxy) eagerly; the
	// operation pool grows lazily as queries run. By the time the loop
	// below drives session i, sessions 0..i-1 still hold live proxies in the
	// agent's connStore, so -race can observe cross-session state
	// corruption.
	clients := make([]*testutil.PipedMongoClient, numSessions)
	for i := 0; i < numSessions; i++ {
		clients[i] = testutil.DialPipedMongo(t, tr, demux, mc, sessionIDs[i], fmt.Sprintf("conn-multi-%d", i))
	}

	// Drive each session's work in turn. Sequential driver traffic avoids
	// starving the synchronous recv loop while still exercising every
	// session's proxy independently.
	for i := 0; i < numSessions; i++ {
		client := clients[i]
		if err := client.PingWithTimeout(mongoTestTimeout); err != nil {
			t.Errorf("session %d ping: %v", i, err)
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), mongoTestTimeout)
		coll := client.Client.Database(mc.Database).Collection(fmt.Sprintf("multi_%d_%d", i, time.Now().UnixNano()))
		if _, err := coll.InsertOne(ctx, bson.M{"session": i}); err != nil {
			cancel()
			t.Errorf("session %d insert: %v", i, err)
			continue
		}
		var doc bson.M
		if err := coll.FindOne(ctx, bson.M{"session": i}).Decode(&doc); err != nil {
			cancel()
			t.Errorf("session %d find: %v", i, err)
			continue
		}
		cancel()
		if got, _ := doc["session"].(int32); int(got) != i {
			t.Errorf("session %d: expected session=%d, got %v", i, i, doc["session"])
		}
	}
}

// TestMongoDB_SessionCloseRace opens a connection, fires a slow operation,
// then sends SessionClose while it is in flight. Asserts teardown is clean:
// no goroutines leak.
//
// The invariant: a SessionClose arriving while a packet handler is still
// running for that session must not leave the system with leaked
// goroutines or a wedged client.
func TestMongoDB_SessionCloseRace(t *testing.T) {
	mc := testutil.StartMongoDB(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)

	leak := testutil.SnapshotGoroutines(t, 30)

	sessionID := testutil.OpenMongoSession(t, tr, mc)
	demux := testutil.StartRecvDemux(t, tr)
	client := testutil.DialPipedMongo(t, tr, demux, mc, sessionID, "conn-close-race")

	// Establish the connection up front so the SessionClose races a
	// cached-proxy operation path, not the initial handshake.
	if err := client.PingWithTimeout(mongoTestTimeout); err != nil {
		t.Fatalf("initial ping: %v", err)
	}

	coll := client.Client.Database(mc.Database).Collection(fmt.Sprintf("sleep_%d", time.Now().UnixNano()))

	// Fire a ~2s server-side operation in the background (the $where sleep
	// keeps the server busy). The agent's recv loop returns after queuing
	// the query into libhoop's blockingReader, so SessionClose can land
	// while the operation is still executing server-side.
	queryDone := make(chan struct{})
	go func() {
		defer close(queryDone)
		ctx, cancel := context.WithTimeout(context.Background(), mongoTestTimeout)
		defer cancel()
		// $where with a sleep blocks the operation server-side.
		_, _ = coll.CountDocuments(ctx, bson.M{
			"$where": "function() { var s = new Date().getTime(); while (new Date().getTime() < s + 2000) {} return true; }",
		})
	}()

	// Let the operation reach the server, then close the session underneath it.
	time.Sleep(300 * time.Millisecond)
	tr.Inject(testutil.BuildSessionClosePacket(sessionID, "0"))

	// The operation goroutine should unblock (with or without error)
	// rather than hang forever.
	select {
	case <-queryDone:
	case <-time.After(mongoTestTimeout):
		t.Fatal("operation did not return after SessionClose — possible wedge")
	}

	shutdownAgent(t, agent, tr)

	// No significant goroutine leak after teardown. Tolerance absorbs
	// docker-client / testcontainer / mongo-driver background noise.
	leak.Assert(t)
}

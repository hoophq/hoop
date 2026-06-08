//go:build integration

package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"github.com/hoophq/hoop/agent/integration/testutil"
)

// TestMongoDB_ParallelSessions runs several independent MongoDB sessions on
// the same agent concurrently and asserts each completes its work. Each
// session has its own bridged client (with its own pool of connections,
// each a distinct connID) and its own upstream MongoDB backend connections.
//
// The invariant: independent sessions on the same agent must not corrupt
// each other's state. With the blockingReader race fixed (ENG-396) this
// runs clean under -race; before the fix, concurrent Read/Write on each
// proxy's bytes.Buffer would trip the detector.
func TestMongoDB_ParallelSessions(t *testing.T) {
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

	clients := make([]*testutil.PipedMongoClient, numSessions)
	for i := 0; i < numSessions; i++ {
		clients[i] = testutil.DialPipedMongo(t, tr, demux, mc, sessionIDs[i], fmt.Sprintf("conn-parallel-%d", i))
	}

	var wg sync.WaitGroup
	errCh := make(chan error, numSessions)
	for i := 0; i < numSessions; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), mongoTestTimeout)
			defer cancel()

			client := clients[i]
			if err := client.PingWithTimeout(mongoTestTimeout); err != nil {
				errCh <- fmt.Errorf("session %d ping: %w", i, err)
				return
			}
			coll := client.Client.Database(mc.Database).Collection(fmt.Sprintf("parallel_%d_%d", i, time.Now().UnixNano()))
			if _, err := coll.InsertOne(ctx, bson.M{"session": i}); err != nil {
				errCh <- fmt.Errorf("session %d insert: %w", i, err)
				return
			}
			var doc bson.M
			if err := coll.FindOne(ctx, bson.M{"session": i}).Decode(&doc); err != nil {
				errCh <- fmt.Errorf("session %d find: %w", i, err)
				return
			}
			if got, _ := doc["session"].(int32); int(got) != i {
				errCh <- fmt.Errorf("session %d: expected session=%d, got %v", i, i, doc["session"])
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

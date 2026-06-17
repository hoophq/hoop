//go:build integration

package integration

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"github.com/hoophq/hoop/agent/integration/testutil"
)

const mongoTestTimeout = 30 * time.Second

// dialMongo wires up the common per-test scaffolding: start the agent, open
// a MongoDB session, start the demux, and build the bridged client. Returns
// the client plus a teardown that shuts the agent down. The ordering
// (OpenMongoSession before StartRecvDemux before DialPipedMongo) matters —
// see the helper docs.
func dialMongo(t *testing.T, mc *testutil.MongoContainer) (*testutil.PipedMongoClient, func()) {
	t.Helper()
	agent, tr := startAgent(t)
	sessionID := testutil.OpenMongoSession(t, tr, mc)
	demux := testutil.StartRecvDemux(t, tr)
	client := testutil.DialPipedMongo(t, tr, demux, mc, sessionID, "conn")
	return client, func() { shutdownAgent(t, agent, tr) }
}

// TestMongoDB_Ping is the end-to-end smoke test: a successful ping forces
// the full bridged SCRAM handshake (legacy OP_QUERY hello with speculative
// auth → libhoop-driven upstream auth → fake SCRAM server validates the
// noop client → OK) through processMongoDBProtocol and libhoop's MongoDB
// proxy.
func TestMongoDB_Ping(t *testing.T) {
	mc := testutil.StartMongoDB(t)
	client, teardown := dialMongo(t, mc)
	defer teardown()

	if err := client.PingWithTimeout(mongoTestTimeout); err != nil {
		t.Fatalf("mongodb ping through agent failed: %v", err)
	}

	// The driver opens a topology monitor plus an operation pool, each as
	// its own DialContext → its own connID → its own agent-side proxy.
	// Assert the per-connection allocation actually fanned out; a
	// regression that collapsed everything onto one connID would still
	// ping successfully but break the multi-connection routing contract.
	if n := client.ConnCount(); n < 2 {
		t.Errorf("expected the driver to open multiple bridged connections, got %d", n)
	}
}

// TestMongoDB_InsertFindUpdateDelete exercises the full document lifecycle
// over the bridged connection.
func TestMongoDB_InsertFindUpdateDelete(t *testing.T) {
	mc := testutil.StartMongoDB(t)
	client, teardown := dialMongo(t, mc)
	defer teardown()

	ctx, cancel := context.WithTimeout(context.Background(), mongoTestTimeout)
	defer cancel()

	coll := client.Client.Database(mc.Database).Collection(fmt.Sprintf("crud_%d", time.Now().UnixNano()))

	t.Run("InsertOne", func(t *testing.T) {
		res, err := coll.InsertOne(ctx, bson.M{"name": "alice", "age": 30})
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
		if res.InsertedID == nil {
			t.Error("expected an inserted id")
		}
	})

	t.Run("FindOne", func(t *testing.T) {
		var doc bson.M
		if err := coll.FindOne(ctx, bson.M{"name": "alice"}).Decode(&doc); err != nil {
			t.Fatalf("find: %v", err)
		}
		if doc["name"] != "alice" {
			t.Errorf("expected name=alice, got %v", doc["name"])
		}
	})

	t.Run("UpdateOne", func(t *testing.T) {
		res, err := coll.UpdateOne(ctx, bson.M{"name": "alice"}, bson.M{"$set": bson.M{"name": "bob"}})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if res.ModifiedCount != 1 {
			t.Errorf("expected 1 modified, got %d", res.ModifiedCount)
		}
	})

	t.Run("FindAfterUpdate", func(t *testing.T) {
		var doc bson.M
		if err := coll.FindOne(ctx, bson.M{"name": "bob"}).Decode(&doc); err != nil {
			t.Fatalf("find after update: %v", err)
		}
		if doc["name"] != "bob" {
			t.Errorf("expected name=bob, got %v", doc["name"])
		}
	})

	t.Run("DeleteOne", func(t *testing.T) {
		res, err := coll.DeleteOne(ctx, bson.M{"name": "bob"})
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		if res.DeletedCount != 1 {
			t.Errorf("expected 1 deleted, got %d", res.DeletedCount)
		}
	})

	t.Run("CountAfterDelete", func(t *testing.T) {
		n, err := coll.CountDocuments(ctx, bson.M{})
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != 0 {
			t.Errorf("expected 0 docs, got %d", n)
		}
	})
}

// TestMongoDB_MultiDocumentCursor verifies a multi-document result set
// streams correctly end-to-end (the find reply plus any getMore batches all
// flow through the proxy).
func TestMongoDB_MultiDocumentCursor(t *testing.T) {
	mc := testutil.StartMongoDB(t)
	client, teardown := dialMongo(t, mc)
	defer teardown()

	ctx, cancel := context.WithTimeout(context.Background(), mongoTestTimeout)
	defer cancel()

	coll := client.Client.Database(mc.Database).Collection(fmt.Sprintf("multi_%d", time.Now().UnixNano()))

	docs := make([]any, 0, 50)
	for i := 0; i < 50; i++ {
		docs = append(docs, bson.M{"i": i, "val": fmt.Sprintf("v%d", i)})
	}
	if _, err := coll.InsertMany(ctx, docs); err != nil {
		t.Fatalf("insert many: %v", err)
	}

	cur, err := coll.Find(ctx, bson.M{}, options.Find().SetBatchSize(10))
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	defer cur.Close(ctx)

	count := 0
	for cur.Next(ctx) {
		count++
	}
	if err := cur.Err(); err != nil {
		t.Fatalf("cursor err: %v", err)
	}
	if count != 50 {
		t.Errorf("expected 50 docs, got %d", count)
	}
}

// TestMongoDB_RunCommand verifies an admin command round-trips, confirming
// non-CRUD OP_MSG commands flow through the proxy.
func TestMongoDB_RunCommand(t *testing.T) {
	mc := testutil.StartMongoDB(t)
	client, teardown := dialMongo(t, mc)
	defer teardown()

	ctx, cancel := context.WithTimeout(context.Background(), mongoTestTimeout)
	defer cancel()

	var result bson.M
	if err := client.Client.Database(mc.Database).RunCommand(ctx, bson.D{{Key: "ping", Value: 1}}).Decode(&result); err != nil {
		t.Fatalf("runCommand ping: %v", err)
	}
	if ok, _ := result["ok"].(float64); ok != 1 {
		t.Errorf("expected ok=1, got %v", result["ok"])
	}
}

// TestMongoDB_ErrorBadCommand verifies a server-side error surfaces to the
// client (proving the error reply round-trips) and that the connection
// survives for a follow-up command.
func TestMongoDB_ErrorBadCommand(t *testing.T) {
	mc := testutil.StartMongoDB(t)
	client, teardown := dialMongo(t, mc)
	defer teardown()

	ctx, cancel := context.WithTimeout(context.Background(), mongoTestTimeout)
	defer cancel()

	err := client.Client.Database(mc.Database).RunCommand(ctx, bson.D{{Key: "thisIsNotACommand", Value: 1}}).Err()
	if err == nil {
		t.Fatal("expected error for unknown command, got nil")
	}
	var cmdErr mongo.CommandError
	if !asMongoCommandError(err, &cmdErr) {
		t.Fatalf("expected mongo.CommandError, got %T: %v", err, err)
	}
	// 59 = CommandNotFound. Assert the specific server semantics survived
	// the proxy round-trip rather than just "some error happened" — a weak
	// assertion would pass on unrelated auth/framing regressions.
	if cmdErr.Code != 59 {
		t.Errorf("expected CommandNotFound (59), got code=%d (%s)", cmdErr.Code, cmdErr.Message)
	}
	if !strings.Contains(strings.ToLower(cmdErr.Message), "command") {
		t.Errorf("expected an unknown-command style message, got: %q", cmdErr.Message)
	}

	// Connection must survive the error.
	if err := client.Client.Ping(ctx, nil); err != nil {
		t.Fatalf("connection did not survive command error: %v", err)
	}
}

// TestMongoDB_BadCredentials verifies that a session whose upstream
// password is wrong fails to establish — libhoop authenticates against the
// upstream itself, so the bad password manifests as a failed ping.
func TestMongoDB_BadCredentials(t *testing.T) {
	mc := testutil.StartMongoDB(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)

	// Connection string with the wrong upstream password. libhoop fails
	// the upstream SCRAM handshake; the client never reaches a usable
	// state.
	badConnString := fmt.Sprintf("mongodb://%s:%s@%s:%s/%s?authSource=admin",
		mc.User, "wrongpassword", mc.Host, mc.Port, mc.Database)
	sessionID := testutil.OpenMongoSessionWithConnString(t, tr, badConnString)
	demux := testutil.StartRecvDemux(t, tr)
	client := testutil.DialPipedMongo(t, tr, demux, mc, sessionID, "conn-bad")

	if err := client.PingWithTimeout(15 * time.Second); err == nil {
		t.Fatal("expected ping to fail with bad upstream credentials, got nil")
	}
}

// asMongoCommandError is errors.As specialized for mongo.CommandError.
func asMongoCommandError(err error, target *mongo.CommandError) bool {
	for err != nil {
		if ce, ok := err.(mongo.CommandError); ok {
			*target = ce
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

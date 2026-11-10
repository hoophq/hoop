//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/agent/integration/testutil"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	mongoOpCompressed uint32 = 2012
	mongoOpMsg        uint32 = 2013
)

// assertAuthenticatedAs checks that the server authenticated the connection
// user of the hoop connection, not the noop user of the client.
func assertAuthenticatedAs(t *testing.T, client *testutil.PipedMongoClient, user string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), mongoTestTimeout)
	defer cancel()
	var status struct {
		AuthInfo struct {
			AuthenticatedUsers []struct {
				User string `bson:"user"`
			} `bson:"authenticatedUsers"`
		} `bson:"authInfo"`
	}
	if err := client.Client.Database("admin").RunCommand(ctx, bson.D{{Key: "connectionStatus", Value: 1}}).Decode(&status); err != nil {
		t.Fatalf("connectionStatus failed: %v", err)
	}
	users := status.AuthInfo.AuthenticatedUsers
	if len(users) != 1 || users[0].User != user {
		t.Fatalf("expected the server to authenticate %q, got %+v", user, users)
	}
}

// TestMongoDB_StableAPI covers a driver whose first hello is OP_MSG: every
// driver with Stable API, and pymongo >= 4.18 without it (ENG-801). The proxy
// must replace the noop login with the connection credentials.
func TestMongoDB_StableAPI(t *testing.T) {
	mc := testutil.StartMongoDB(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)
	sessionID := testutil.OpenMongoSession(t, tr, mc)
	demux := testutil.StartRecvDemux(t, tr)
	client := testutil.DialPipedMongoWith(t, tr, demux, sessionID, "conn-api", testutil.MongoDialOptions{
		Configure: func(o *options.ClientOptions) {
			o.SetServerAPIOptions(options.ServerAPI(options.ServerAPIVersion1))
		},
	})

	if err := client.PingWithTimeout(mongoTestTimeout); err != nil {
		t.Fatalf("mongodb ping with Stable API through agent failed: %v", err)
	}
	assertAuthenticatedAs(t, client, mc.User)

	// the test is only useful while the driver sends its hello as OP_MSG
	for _, op := range client.FirstOpCodes() {
		if op != mongoOpMsg {
			t.Fatalf("expected an OP_MSG hello on every connection, got opcodes %v", client.FirstOpCodes())
		}
	}
}

// TestMongoDB_CompressorsNotNegotiated covers a client that asks for
// compression. The gateway audit and the agent guardrails and data masking
// read every packet and do not decode OP_COMPRESSED, so the proxy must not
// agree on a compressor.
func TestMongoDB_CompressorsNotNegotiated(t *testing.T) {
	mc := testutil.StartMongoDB(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)
	sessionID := testutil.OpenMongoSession(t, tr, mc)
	demux := testutil.StartRecvDemux(t, tr)
	client := testutil.DialPipedMongoWith(t, tr, demux, sessionID, "conn-zlib", testutil.MongoDialOptions{
		Configure: func(o *options.ClientOptions) { o.SetCompressors([]string{"zlib", "zstd", "snappy"}) },
	})

	ctx, cancel := context.WithTimeout(context.Background(), mongoTestTimeout)
	defer cancel()
	coll := client.Client.Database(mc.Database).Collection("compressors")
	if _, err := coll.InsertOne(ctx, bson.D{{Key: "name", Value: strings.Repeat("a", 4096)}}); err != nil {
		t.Fatalf("insert through agent failed: %v", err)
	}
	if n, err := coll.CountDocuments(ctx, bson.D{}); err != nil || n != 1 {
		t.Fatalf("count through agent: n=%d, err=%v", n, err)
	}
	if n := client.SentOpCodes()[mongoOpCompressed]; n != 0 {
		t.Fatalf("expected no OP_COMPRESSED message, the driver sent %d", n)
	}

	// a client without credentials (a health probe) still runs commands
	probe := testutil.DialPipedMongoWith(t, tr, demux, sessionID, "conn-probe", testutil.MongoDialOptions{
		NoAuth:    true,
		Configure: func(o *options.ClientOptions) { o.SetCompressors([]string{"zlib"}) },
	})
	for i := 0; i < 3; i++ {
		if err := probe.PingWithTimeout(mongoTestTimeout); err != nil {
			t.Fatalf("ping without credentials through agent failed: %v", err)
		}
	}
	if n := probe.SentOpCodes()[mongoOpCompressed]; n != 0 {
		t.Fatalf("expected no OP_COMPRESSED message without credentials, the driver sent %d", n)
	}
}

// TestMongoDB_ClientBadPasswordKeepsSession covers a client that does not
// log in with noop:noop. It must get the error a mongo server returns, and the
// other clients of the same session must keep working.
func TestMongoDB_ClientBadPasswordKeepsSession(t *testing.T) {
	mc := testutil.StartMongoDB(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)
	sessionID := testutil.OpenMongoSession(t, tr, mc)
	demux := testutil.StartRecvDemux(t, tr)

	good := testutil.DialPipedMongo(t, tr, demux, mc, sessionID, "conn-good")
	if err := good.PingWithTimeout(mongoTestTimeout); err != nil {
		t.Fatalf("mongodb ping through agent failed: %v", err)
	}

	bad := testutil.DialPipedMongoWith(t, tr, demux, sessionID, "conn-bad-client", testutil.MongoDialOptions{
		User: "noop", Password: "wrong",
	})
	err := bad.PingWithTimeout(15 * time.Second)
	if err == nil || !strings.Contains(err.Error(), "AuthenticationFailed") {
		t.Fatalf("expected an AuthenticationFailed error for a wrong client password, got %v", err)
	}

	select {
	case <-demux.SessionCloseChan(sessionID):
		t.Fatal("a failed client login must not end the session")
	case <-time.After(time.Second):
	}
	// the connections of the other client and its new connections still work
	if err := good.PingWithTimeout(mongoTestTimeout); err != nil {
		t.Fatalf("mongodb ping after a failed client login: %v", err)
	}
	again := testutil.DialPipedMongo(t, tr, demux, mc, sessionID, "conn-good-again")
	if err := again.PingWithTimeout(mongoTestTimeout); err != nil {
		t.Fatalf("mongodb ping after a failed client login: %v", err)
	}
}

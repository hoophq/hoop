//go:build integration

package testutil

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// MongoContainer wraps a MongoDB container for integration tests.
// Credentials are fixed so test code can reference them directly. These
// are the *real upstream* credentials libhoop's MongoDB proxy uses to
// authenticate against the server; the client-facing credentials the proxy
// presents are the hardcoded noop/noop pair (see DialPipedMongo).
type MongoContainer struct {
	Host      string
	Port      string
	User      string
	Password  string
	Database  string
	Container testcontainers.Container
}

const (
	mongoRootUser = "root"
	mongoRootPass = "testpass"
)

// StartMongoDB returns a handle to the shared MongoDB 7 server with a
// private database name for this test. The server boots once per package
// (see shared_container.go); Mongo creates databases lazily on first
// write, so no setup statement is needed.
func StartMongoDB(t T) *MongoContainer {
	t.Helper()
	base, err := bootMongoDB()
	if err != nil {
		t.Fatalf("failed to start mongodb container: %v", err)
	}
	return base.forkDatabase()
}

// StartMongoDBWithTestCommands returns the same shared server as
// StartMongoDB. The shared mongod always runs with
// --setParameter enableTestCommands=1, which unlocks server test commands
// such as `sleep` — a deterministic stand-in for a long-running operation
// because, unlike a cursor read, it is never aborted by a client
// disconnect and only killOp stops it.
//
// Enabling the parameter for every test rather than for a second container
// costs a ~9.5s boot; it only adds commands, so tests that do not use them
// are unaffected.
func StartMongoDBWithTestCommands(t T) *MongoContainer {
	t.Helper()
	return StartMongoDB(t)
}

// bootMongoDB boots the shared server on first call. sync.OnceValues also
// caches a failure, so a broken Docker daemon costs one startup timeout
// instead of one per test.
var bootMongoDB = sync.OnceValues(bootMongoDBContainer)

// bootMongoDBContainer boots MongoDB with a fixed root user and waits until
// it accepts authenticated connections. The wait strategy combines the
// readiness log line with a real authenticated ping because mongod logs
// "waiting for connections" slightly before the root user (created from
// the MONGO_INITDB env vars) is usable.
func bootMongoDBContainer() (*MongoContainer, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "mongo:7",
			ExposedPorts: []string{"27017/tcp"},
			// wiredTigerCacheSizeGB is set explicitly because mongod's
			// default sizes the cache from *total host RAM*, not from what
			// this container may use. That was harmless when the container
			// lived for one test; now it stays resident beside SQL Server,
			// MariaDB, Postgres and a -race test binary, so an unbounded
			// cache starves the rest of the suite (ENG-511).
			Cmd: []string{
				"--setParameter", "enableTestCommands=1",
				"--wiredTigerCacheSizeGB", "0.5",
			},
			Env: map[string]string{
				"MONGO_INITDB_ROOT_USERNAME": mongoRootUser,
				"MONGO_INITDB_ROOT_PASSWORD": mongoRootPass,
			},
			WaitingFor: wait.ForAll(
				wait.ForLog("Waiting for connections").
					WithOccurrence(1),
				wait.ForListeningPort("27017/tcp"),
			).WithDeadline(90 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		return nil, err
	}
	terminateAtPackageEnd(container)

	mappedPort, err := container.MappedPort(ctx, "27017/tcp")
	if err != nil {
		return nil, fmt.Errorf("failed to get mapped mongodb port: %w", err)
	}

	host, err := ContainerHost(ctx, container)
	if err != nil {
		return nil, fmt.Errorf("failed to get mongodb container host: %w", err)
	}

	c := &MongoContainer{
		Host:      host,
		Port:      mappedPort.Port(),
		User:      mongoRootUser,
		Password:  mongoRootPass,
		Database:  "testdb",
		Container: container,
	}

	if err := c.waitForReady(); err != nil {
		return nil, err
	}
	return c, nil
}

// forkDatabase returns a handle scoped to a private database name. Mongo
// creates a database on first write, so there is nothing to execute here.
func (c *MongoContainer) forkDatabase() *MongoContainer {
	forked := *c
	forked.Database = nextDatabaseName()
	return &forked
}

// UpstreamConnString returns the direct mongodb:// URI to the container
// using the real root credentials. This is what libhoop's proxy receives
// as its CONNECTION_STRING env var to authenticate upstream. authSource is
// admin because that's where the root user lives.
func (c *MongoContainer) UpstreamConnString() string {
	return fmt.Sprintf("mongodb://%s:%s@%s:%s/%s?authSource=admin",
		c.User, c.Password, c.Host, c.Port, c.Database)
}

func (c *MongoContainer) waitForReady() error {
	// 120s matches the container-start context above. mongod's first boot
	// (storage engine init + root user creation from MONGO_INITDB) can
	// exceed a minute on a loaded CI runner even after the wait strategy's
	// log line and listening port have been observed (DEP-57).
	const readyDeadline = 120 * time.Second
	deadline := time.Now().Add(readyDeadline)
	var lastErr error
	for time.Now().Before(deadline) {
		if lastErr = c.ping(); lastErr == nil {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("mongodb container never became ready within %v: %w", readyDeadline, lastErr)
}

// directClient opens a short-lived direct connection to the container
// using the real root credentials, bypassing the agent. Used by
// waitForReady and by concurrency tests to inspect server state.
func (c *MongoContainer) directClient(ctx context.Context) (*mongo.Client, error) {
	return mongo.Connect(ctx, options.Client().ApplyURI(c.UpstreamConnString()))
}

func (c *MongoContainer) ping() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	client, err := c.directClient(ctx)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	return client.Ping(ctx, nil)
}

// ConnectionCount opens a sidecar admin connection and returns the number
// of current connections the server reports via serverStatus. This counts
// all connections, so callers compare deltas rather than absolute values.
func (c *MongoContainer) ConnectionCount(t T) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := c.directClient(ctx)
	if err != nil {
		t.Fatalf("mongostat: failed to open admin connection: %v", err)
	}
	defer client.Disconnect(context.Background())

	var result struct {
		Connections struct {
			Current int32 `bson:"current"`
		} `bson:"connections"`
	}
	cmd := client.Database("admin").RunCommand(ctx, map[string]any{"serverStatus": 1})
	if err := cmd.Decode(&result); err != nil {
		t.Fatalf("mongostat: failed running serverStatus: %v", err)
	}
	return int(result.Connections.Current)
}

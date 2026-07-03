//go:build integration

package testutil

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
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
	mongoDatabase = "testdb"
)

// StartMongoDB boots a MongoDB 7 container with a fixed root user, waits
// until it accepts authenticated connections, and returns a handle. The
// wait strategy combines the readiness log line with a real authenticated
// ping because mongod logs "waiting for connections" slightly before the
// root user (created from the MONGO_INITDB env vars) is usable.
func StartMongoDB(t T) *MongoContainer {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "mongo:7",
			ExposedPorts: []string{"27017/tcp"},
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
		t.Fatalf("failed to start mongodb container: %v", err)
	}

	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	mappedPort, err := container.MappedPort(ctx, "27017/tcp")
	if err != nil {
		t.Fatalf("failed to get mapped mongodb port: %v", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get mongodb container host: %v", err)
	}

	c := &MongoContainer{
		Host:      host,
		Port:      mappedPort.Port(),
		User:      mongoRootUser,
		Password:  mongoRootPass,
		Database:  mongoDatabase,
		Container: container,
	}

	c.waitForReady(t)
	return c
}

// UpstreamConnString returns the direct mongodb:// URI to the container
// using the real root credentials. This is what libhoop's proxy receives
// as its CONNECTION_STRING env var to authenticate upstream. authSource is
// admin because that's where the root user lives.
func (c *MongoContainer) UpstreamConnString() string {
	return fmt.Sprintf("mongodb://%s:%s@%s:%s/%s?authSource=admin",
		c.User, c.Password, c.Host, c.Port, c.Database)
}

func (c *MongoContainer) waitForReady(t T) {
	deadline := time.Now().Add(60 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if lastErr = c.ping(); lastErr == nil {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("mongodb container never became ready within 60s: %v", lastErr)
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

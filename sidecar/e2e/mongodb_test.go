//go:build integration

package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const mongoImage = "mongo:7"

const mongodbMaskingConfig = `
log_level: info

audit:
  file: "-"
  memory_buffer: 64

listeners:
  - name: appdb
    protocol: mongodb
    listen: {{listen}}
    upstream: {{upstream}}
    guardrails:
      mode: enforce
      rules:
        - name: no-destructive-mongodb
          type: operation
          operations: [drop, delete]
          message: destructive commands are not permitted on appdb
    mask:
      rules:
        - {name: email-column, columns: [email], strategy: redact}
`

func startMongoDB(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        mongoImage,
		ExposedPorts: []string{"27017/tcp"},
		Env: map[string]string{
			"MONGO_INITDB_ROOT_USERNAME": dbUser,
			"MONGO_INITDB_ROOT_PASSWORD": dbPass,
		},
		WaitingFor: wait.ForAll(
			wait.ForLog("Waiting for connections"),
			wait.ForListeningPort("27017/tcp"),
		).WithDeadline(4 * time.Minute),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start mongodb: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Errorf("leaked mongodb container: %v", err)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("mongodb container host: %v", err)
	}
	port, err := container.MappedPort(ctx, "27017/tcp")
	if err != nil {
		t.Fatalf("mongodb container port: %v", err)
	}
	addr := net.JoinHostPort(host, port.Port())
	seedMongoDB(t, addr)
	return addr
}

func seedMongoDB(t *testing.T, addr string) {
	t.Helper()
	client := openMongoDB(t, addr)
	ctx, cancel := context.WithTimeout(context.Background(), stmtTimeout)
	defer cancel()
	_, err := client.Database(dbName).Collection("customers").InsertMany(ctx, []any{
		bson.D{
			{Key: "_id", Value: int32(1)},
			{Key: "name", Value: "Ada Lovelace"},
			{Key: "email", Value: "ada@example.com"},
			{Key: "profile", Value: bson.D{{Key: "email", Value: "private@example.com"}}},
		},
		bson.D{
			{Key: "_id", Value: int32(2)},
			{Key: "name", Value: "Bob Stone"},
			{Key: "email", Value: "bob@example.com"},
			{Key: "profile", Value: bson.D{{Key: "email", Value: "bob.private@example.com"}}},
		},
		bson.D{
			{Key: "_id", Value: int32(3)},
			{Key: "name", Value: "No Email"},
			{Key: "email", Value: nil},
		},
	})
	if err != nil {
		t.Fatalf("seed mongodb: %v", err)
	}
}

func openMongoDB(t *testing.T, addr string) *mongo.Client {
	t.Helper()
	uri := fmt.Sprintf(
		"mongodb://%s:%s@%s/%s?authSource=admin&directConnection=true&connectTimeoutMS=20000&socketTimeoutMS=20000&serverSelectionTimeoutMS=20000",
		dbUser, dbPass, addr, dbName)
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("connect mongodb: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Disconnect(ctx); err != nil {
			t.Errorf("disconnect mongodb: %v", err)
		}
	})

	deadline := time.Now().Add(90 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), stmtTimeout)
		err = client.Ping(ctx, nil)
		cancel()
		if err == nil {
			return client
		}
		if time.Now().After(deadline) {
			t.Fatalf("ping mongodb: %v", err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func (s *sidecar) dialMongoDB(t *testing.T) *mongo.Client {
	t.Helper()
	return openMongoDB(t, s.addr)
}

func TestMongoDBMasksFirstAndNextBatch(t *testing.T) {
	upstream := startMongoDB(t)
	sidecar := startSidecar(t, upstream, mongodbMaskingConfig)
	client := sidecar.dialMongoDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), stmtTimeout)
	defer cancel()

	cursor, err := client.Database(dbName).Collection("customers").Find(
		ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetBatchSize(1))
	if err != nil {
		t.Fatalf("find customers: %v", err)
	}
	defer cursor.Close(ctx)

	var customers []struct {
		ID      int32   `bson:"_id"`
		Email   *string `bson:"email"`
		Profile struct {
			Email string `bson:"email"`
		} `bson:"profile"`
	}
	if err := cursor.All(ctx, &customers); err != nil {
		t.Fatalf("read customers: %v", err)
	}
	if len(customers) != 3 {
		t.Fatalf("customers = %d, want 3", len(customers))
	}
	for _, customer := range customers[:2] {
		if customer.Email == nil || !strings.HasPrefix(*customer.Email, "[REDACTED:") {
			t.Errorf("customer %d email was not masked: %v", customer.ID, customer.Email)
		}
		if !strings.HasPrefix(customer.Profile.Email, "[REDACTED:") {
			t.Errorf("customer %d nested email was not masked: %q", customer.ID, customer.Profile.Email)
		}
	}
	if customers[2].Email != nil {
		t.Errorf("BSON null became %q", *customers[2].Email)
	}

	sidecar.waitForAudit(t, "a masked MongoDB cursor batch", func(event auditEvent) bool {
		return event.Kind == "masked" && event.Count > 0
	})
}

func TestMongoDBGuardrailReturnsCorrelatedCommandError(t *testing.T) {
	upstream := startMongoDB(t)
	sidecar := startSidecar(t, upstream, mongodbMaskingConfig)
	client := sidecar.dialMongoDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), stmtTimeout)
	defer cancel()

	_, err := client.Database(dbName).Collection("customers").DeleteOne(
		ctx, bson.D{{Key: "_id", Value: int32(1)}})
	if err == nil {
		t.Fatal("MongoDB delete was allowed")
	}
	var commandError mongo.CommandError
	if !errors.As(err, &commandError) {
		t.Fatalf("delete error = %T %v, want mongo.CommandError", err, err)
	}
	if commandError.Code != 13 || !strings.Contains(commandError.Message, "destructive commands are not permitted") {
		t.Fatalf("command error = code %d message %q", commandError.Code, commandError.Message)
	}

	direct := openMongoDB(t, upstream)
	count, err := direct.Database(dbName).Collection("customers").CountDocuments(ctx, bson.D{})
	if err != nil {
		t.Fatalf("count upstream documents: %v", err)
	}
	if count != 3 {
		t.Fatalf("document count = %d, want 3; denied delete reached MongoDB", count)
	}

	event := sidecar.waitForAudit(t, "the denied MongoDB delete", func(event auditEvent) bool {
		return event.Kind == "violation" && event.Operation == "delete"
	})
	if event.Allowed || event.Rule != "no-destructive-mongodb" || event.Metadata["mongodb.command"] != "delete" {
		t.Errorf("violation event = %+v", event)
	}

	// The denial closes one relayed connection. A fresh pooled client must be
	// able to use the listener immediately afterwards.
	recovered := sidecar.dialMongoDB(t)
	if _, err := recovered.Database(dbName).Collection("customers").UpdateOne(
		ctx,
		bson.D{{Key: "_id", Value: int32(1)}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "name", Value: "Ada L."}}}},
	); err != nil {
		t.Fatalf("allowed update after denial: %v", err)
	}

	attacker := sidecar.dialMongoDB(t)
	err = attacker.Database("admin").RunCommand(ctx, bson.D{
		{Key: "applyOps", Value: bson.A{bson.D{
			{Key: "op", Value: "d"},
			{Key: "ns", Value: dbName + ".customers"},
			{Key: "o", Value: bson.D{{Key: "_id", Value: int32(2)}}},
		}}},
	}).Err()
	if err == nil {
		t.Fatal("MongoDB applyOps delete was allowed")
	}
	commandError = mongo.CommandError{}
	if !errors.As(err, &commandError) || commandError.Code != 13 {
		t.Fatalf("applyOps error = %T %v, want MongoDB Unauthorized", err, err)
	}
	count, err = direct.Database(dbName).Collection("customers").CountDocuments(ctx, bson.D{})
	if err != nil {
		t.Fatalf("count after applyOps denial: %v", err)
	}
	if count != 3 {
		t.Fatalf("document count = %d, want 3; applyOps delete reached MongoDB", count)
	}
	event = sidecar.waitForAudit(t, "the denied MongoDB applyOps", func(event auditEvent) bool {
		return event.Kind == "violation" && event.Operation == "delete" &&
			event.Metadata["mongodb.command"] == "applyOps"
	})
	if event.Allowed || event.Rule != "no-destructive-mongodb" {
		t.Errorf("applyOps violation event = %+v", event)
	}
}

func TestMongoDBDocumentSequenceIsAudited(t *testing.T) {
	upstream := startMongoDB(t)
	sidecar := startSidecar(t, upstream, mongodbMaskingConfig)
	client := sidecar.dialMongoDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), stmtTimeout)
	defer cancel()

	_, err := client.Database(dbName).Collection("customers").InsertMany(ctx, []any{
		bson.D{{Key: "_id", Value: int32(4)}, {Key: "marker", Value: "first-sequence-marker"}},
		bson.D{{Key: "_id", Value: int32(5)}, {Key: "marker", Value: "second-sequence-marker"}},
	})
	if err != nil {
		t.Fatalf("insert many: %v", err)
	}

	event := sidecar.waitForAudit(t, "the MongoDB document sequence", func(event auditEvent) bool {
		return event.Kind == "statement" && event.Operation == "insert" &&
			strings.Contains(event.Statement, "first-sequence-marker")
	})
	if !strings.Contains(event.Statement, "second-sequence-marker") {
		t.Errorf("audit statement omitted the second sequence document: %s", event.Statement)
	}
	if event.Metadata["mongodb.command"] != "insert" {
		t.Errorf("mongodb.command = %q, want insert", event.Metadata["mongodb.command"])
	}
}

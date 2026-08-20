//go:build integration

package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "github.com/lib/pq"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// ContainerHost returns the host address for reaching a container's
// published ports, normalizing "localhost" to "127.0.0.1". Docker's port
// proxy on CI runners is not always bound on IPv6, and "localhost" can
// resolve to "::1" first — clients then see "connection refused" until
// their readiness deadline expires even though the container is healthy
// on the IPv4 loopback (observed with the MongoDB driver on GitHub
// Actions, DEP-57). Pinning the IPv4 literal removes the ambiguity.
func ContainerHost(ctx context.Context, container testcontainers.Container) (string, error) {
	host, err := container.Host(ctx)
	if err != nil {
		return "", err
	}
	if host == "localhost" {
		return "127.0.0.1", nil
	}
	return host, nil
}

type PGContainer struct {
	Host      string
	Port      string
	User      string
	Password  string
	Database  string
	Container testcontainers.Container
}

// StartPostgres returns a handle to the shared Postgres server with a
// private database created for this test. The server boots once per
// package; see shared_container.go for why and for how isolation is kept.
func StartPostgres(t T) *PGContainer {
	t.Helper()
	base, err := bootPostgres()
	if err != nil {
		t.Fatalf("failed to start postgres container: %v", err)
	}
	return base.forkDatabase(t)
}

// bootPostgres boots the shared server on first call. sync.OnceValues also
// caches a failure, so a broken Docker daemon costs one startup timeout
// instead of one per test.
var bootPostgres = sync.OnceValues(bootPostgresContainer)

func bootPostgresContainer() (*PGContainer, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const user = "testuser"
	const password = "testpass"
	const database = "testdb"

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "postgres:16-alpine",
			ExposedPorts: []string{"5432/tcp"},
			Env: map[string]string{
				"POSTGRES_USER":     user,
				"POSTGRES_PASSWORD": password,
				"POSTGRES_DB":       database,
			},
			WaitingFor: wait.ForAll(
				wait.ForLog("database system is ready to accept connections"),
				wait.ForListeningPort("5432/tcp"),
			).WithDeadline(30 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		return nil, err
	}
	terminateAtPackageEnd(container)

	mappedPort, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return nil, fmt.Errorf("failed to get mapped port: %w", err)
	}

	host, err := ContainerHost(ctx, container)
	if err != nil {
		return nil, fmt.Errorf("failed to get container host: %w", err)
	}

	return &PGContainer{
		Host:      host,
		Port:      mappedPort.Port(),
		User:      user,
		Password:  password,
		Database:  database,
		Container: container,
	}, nil
}

// forkDatabase creates a private database on the shared server and returns
// a handle scoped to it. The bootstrap database stays untouched so it can
// always serve as the connection target for the next CREATE DATABASE.
func (pg *PGContainer) forkDatabase(t T) *PGContainer {
	t.Helper()

	db, err := sql.Open("postgres", pg.ConnString())
	if err != nil {
		t.Fatalf("postgres: failed to open admin connection: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	forked := *pg
	forked.Database = nextDatabaseName()
	if _, err := db.ExecContext(ctx, "CREATE DATABASE "+forked.Database); err != nil {
		t.Fatalf("postgres: failed creating database %s: %v", forked.Database, err)
	}
	return &forked
}

func (pg *PGContainer) ConnString() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		pg.User, pg.Password, pg.Host, pg.Port, pg.Database)
}

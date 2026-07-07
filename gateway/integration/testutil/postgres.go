//go:build integration

package testutil

import (
	"context"
	"fmt"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// PostgresContainer wraps a throwaway PostgreSQL container used as the
// gateway's state store for the smoke test suite. Credentials are fixed so
// the connection URI is deterministic.
type PostgresContainer struct {
	Host      string
	Port      string
	User      string
	Password  string
	Database  string
	Container testcontainers.Container
}

const (
	pgUser     = "hoopgw"
	pgPassword = "hoopgwpass"
	pgDatabase = "hoopgwtest"
)

// StartPostgres boots a postgres:17.6 container (matching the version used in
// deploy/docker-compose) and blocks until it accepts connections. It returns
// an error rather than taking a *testing.T because it runs from TestMain,
// where no test handle exists yet.
func StartPostgres(ctx context.Context) (*PostgresContainer, error) {
	startCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	container, err := testcontainers.GenericContainer(startCtx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "postgres:17.6",
			ExposedPorts: []string{"5432/tcp"},
			Env: map[string]string{
				"POSTGRES_USER":     pgUser,
				"POSTGRES_PASSWORD": pgPassword,
				"POSTGRES_DB":       pgDatabase,
			},
			WaitingFor: wait.ForAll(
				// The init scripts restart Postgres once, so the readiness log
				// can appear twice; require the second occurrence to avoid
				// connecting during the transient first startup.
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2),
				wait.ForListeningPort("5432/tcp"),
			).WithDeadline(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed starting postgres container: %w", err)
	}

	mappedPort, err := container.MappedPort(startCtx, "5432/tcp")
	if err != nil {
		_ = container.Terminate(context.Background())
		return nil, fmt.Errorf("failed getting mapped postgres port: %w", err)
	}

	host, err := container.Host(startCtx)
	if err != nil {
		_ = container.Terminate(context.Background())
		return nil, fmt.Errorf("failed getting postgres container host: %w", err)
	}
	// Docker's port proxy on CI runners is not always bound on IPv6, and
	// "localhost" can resolve to "::1" first, yielding spurious connection
	// refusals against a healthy container. Pin the IPv4 loopback.
	if host == "localhost" {
		host = "127.0.0.1"
	}

	return &PostgresContainer{
		Host:      host,
		Port:      mappedPort.Port(),
		User:      pgUser,
		Password:  pgPassword,
		Database:  pgDatabase,
		Container: container,
	}, nil
}

// URI returns a libpq/pgx-compatible connection string for the container.
func (c *PostgresContainer) URI() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		c.User, c.Password, c.Host, c.Port, c.Database)
}

// Terminate stops and removes the container. Safe to call once during teardown.
func (c *PostgresContainer) Terminate() error {
	if c.Container == nil {
		return nil
	}
	return c.Container.Terminate(context.Background())
}

//go:build integration

package testutil

import (
	"context"
	"fmt"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

type PGContainer struct {
	Host      string
	Port      string
	User      string
	Password  string
	Database  string
	Container testcontainers.Container
}

func StartPostgres(t T) *PGContainer {
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
		t.Fatalf("failed to start postgres container: %v", err)
	}

	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	mappedPort, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatalf("failed to get mapped port: %v", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get container host: %v", err)
	}

	return &PGContainer{
		Host:      host,
		Port:      mappedPort.Port(),
		User:      user,
		Password:  password,
		Database:  database,
		Container: container,
	}
}

func (pg *PGContainer) ConnString() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		pg.User, pg.Password, pg.Host, pg.Port, pg.Database)
}

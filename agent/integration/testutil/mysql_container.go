//go:build integration

package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// MySQLContainer wraps a MariaDB container for integration tests.
// MariaDB is used instead of Oracle MySQL because it boots faster, ships
// a smaller image, and speaks the same wire protocol libhoop's MySQL
// proxy targets. Credentials are fixed so test code can reference them
// directly.
type MySQLContainer struct {
	Host      string
	Port      string
	User      string
	Password  string
	Database  string
	Container testcontainers.Container
}

// StartMySQL boots a MariaDB container with a fixed root password and a
// pre-created test database. The wait strategy blocks until MariaDB logs
// that it is ready for connections *and* the port is accepting them —
// MariaDB's entrypoint starts a throwaway server for initialization
// before the real one, so matching the second "ready" log line plus the
// listening port avoids connecting to the init server.
func StartMySQL(t T) *MySQLContainer {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	const user = "root"
	const password = "testpass"
	const database = "testdb"

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "mariadb:11",
			ExposedPorts: []string{"3306/tcp"},
			Env: map[string]string{
				"MARIADB_ROOT_PASSWORD": password,
				"MARIADB_DATABASE":      database,
			},
			WaitingFor: wait.ForAll(
				// The init phase logs this line once for the temporary
				// server and once for the real one; the "port: 3306"
				// qualifier only appears for the real server.
				wait.ForLog("mariadbd: ready for connections").
					WithOccurrence(1),
				wait.ForListeningPort("3306/tcp"),
			).WithDeadline(90 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("failed to start mariadb container: %v", err)
	}

	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	mappedPort, err := container.MappedPort(ctx, "3306/tcp")
	if err != nil {
		t.Fatalf("failed to get mapped mysql port: %v", err)
	}

	host, err := ContainerHost(ctx, container)
	if err != nil {
		t.Fatalf("failed to get mysql container host: %v", err)
	}

	c := &MySQLContainer{
		Host:      host,
		Port:      mappedPort.Port(),
		User:      user,
		Password:  password,
		Database:  database,
		Container: container,
	}

	// MariaDB accepts TCP a moment before it can actually authenticate;
	// poll a real handshake to be sure before returning.
	c.waitForReady(t)

	return c
}

func (c *MySQLContainer) waitForReady(t T) {
	deadline := time.Now().Add(60 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if lastErr = c.ping(); lastErr == nil {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("mariadb container never became ready within 60s: %v", lastErr)
}

// ConnString returns a go-sql-driver/mysql DSN that connects directly to
// the container (bypassing the agent). Used by the sidecar admin
// connection in concurrency tests to count active backends.
func (c *MySQLContainer) ConnString() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s",
		c.User, c.Password, c.Host, c.Port, c.Database)
}

// ping opens a short-lived direct connection to the container and runs a
// trivial query, returning any error. Used by waitForReady to confirm
// the real (non-init) MariaDB server is accepting authenticated
// connections.
func (c *MySQLContainer) ping() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	db, err := sql.Open("mysql", c.ConnString())
	if err != nil {
		return err
	}
	defer db.Close()
	return db.PingContext(ctx)
}

// ProcessCount opens a sidecar admin connection and returns the number of
// connections to the test database currently visible in
// information_schema.PROCESSLIST, excluding the sidecar's own
// connection. Used by concurrency tests to assert how many upstream
// connections the agent established.
func (c *MySQLContainer) ProcessCount(t T) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err := sql.Open("mysql", c.ConnString())
	if err != nil {
		t.Fatalf("mysqlstat: failed to open admin connection: %v", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("mysqlstat: admin ping failed: %v", err)
	}

	// CONNECTION_ID() is this admin session's own id; exclude it so the
	// count reflects only agent-driven connections. Filtering on DB
	// keeps the MariaDB system/event-scheduler threads out of the count.
	var adminConnID int64
	if err := db.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&adminConnID); err != nil {
		t.Fatalf("mysqlstat: failed to read CONNECTION_ID: %v", err)
	}

	var count int
	row := db.QueryRowContext(ctx, `
		SELECT count(*) FROM information_schema.PROCESSLIST
		WHERE DB = ? AND ID <> ?`, c.Database, adminConnID)
	if err := row.Scan(&count); err != nil {
		t.Fatalf("mysqlstat: failed to count processes: %v", err)
	}
	return count
}

// WaitForProcessCount polls ProcessCount until it equals want or the
// timeout elapses. MariaDB reaps connections on close but not instantly,
// so teardown assertions need a poll rather than a single snapshot.
func (c *MySQLContainer) WaitForProcessCount(t T, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last int
	for time.Now().Before(deadline) {
		last = c.ProcessCount(t)
		if last == want {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("mysqlstat: expected %d processes after %v, last observed=%d", want, timeout, last)
}

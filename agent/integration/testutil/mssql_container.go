//go:build integration

package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/microsoft/go-mssqldb"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// MSSQLContainer wraps a Microsoft SQL Server container for integration
// tests. SQL Server runs natively on Linux from the official
// mcr.microsoft.com image, so no Windows host is needed. Credentials are
// fixed so test code can reference them directly.
//
// SQL Server has no concept of a "create this database on boot" env var
// (unlike Postgres/MySQL), so StartMSSQL creates the test database itself
// after the server is ready.
type MSSQLContainer struct {
	Host      string
	Port      string
	User      string
	Password  string
	Database  string
	Container testcontainers.Container
}

// mssqlSAPassword satisfies SQL Server's password policy (>=8 chars, mixed
// case, digits, symbols). Used for the built-in sa account.
const (
	mssqlSAUser     = "sa"
	mssqlSAPassword = "hoopTest!2024"
	mssqlDatabase   = "testdb"
)

// StartMSSQL boots a SQL Server 2022 container, waits until it accepts
// authenticated connections, then creates the test database. The wait
// strategy combines the readiness log line with a real authenticated ping
// because SQL Server logs "ready for client connections" slightly before
// the sa login is actually usable.
func StartMSSQL(t T) *MSSQLContainer {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "mcr.microsoft.com/mssql/server:2022-latest",
			ExposedPorts: []string{"1433/tcp"},
			Env: map[string]string{
				"ACCEPT_EULA":       "Y",
				"MSSQL_SA_PASSWORD": mssqlSAPassword,
				// Express avoids the eval-edition nag and boots quickly;
				// it speaks the identical TDS wire protocol libhoop's
				// MSSQL proxy targets.
				"MSSQL_PID": "Express",
			},
			WaitingFor: wait.ForAll(
				wait.ForLog("SQL Server is now ready for client connections").
					WithOccurrence(1),
				wait.ForListeningPort("1433/tcp"),
			).WithDeadline(150 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("failed to start mssql container: %v", err)
	}

	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	mappedPort, err := container.MappedPort(ctx, "1433/tcp")
	if err != nil {
		t.Fatalf("failed to get mapped mssql port: %v", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get mssql container host: %v", err)
	}

	c := &MSSQLContainer{
		Host:      host,
		Port:      mappedPort.Port(),
		User:      mssqlSAUser,
		Password:  mssqlSAPassword,
		Database:  "master",
		Container: container,
	}

	// Block until sa can actually authenticate, then create the test DB.
	c.waitForReady(t)
	c.createDatabase(t)
	c.Database = mssqlDatabase

	return c
}

// adminConnString returns a go-mssqldb DSN that connects directly to the
// container against the given database, bypassing the agent. TLS is
// disabled because the bridged-proxy path the tests exercise also runs
// without client-side encryption.
func (c *MSSQLContainer) adminConnString(database string) string {
	return fmt.Sprintf("sqlserver://%s:%s@%s:%s?database=%s&encrypt=disable",
		c.User, c.Password, c.Host, c.Port, database)
}

// ConnString returns a direct DSN to the test database. Used by sidecar
// admin connections in concurrency tests.
func (c *MSSQLContainer) ConnString() string {
	return c.adminConnString(c.Database)
}

func (c *MSSQLContainer) waitForReady(t T) {
	deadline := time.Now().Add(120 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if lastErr = c.ping("master"); lastErr == nil {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("mssql container never became ready within 120s: %v", lastErr)
}

// ping opens a short-lived direct connection to the container and runs a
// trivial query against the given database.
func (c *MSSQLContainer) ping(database string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	db, err := sql.Open("sqlserver", c.adminConnString(database))
	if err != nil {
		return err
	}
	defer db.Close()
	return db.PingContext(ctx)
}

// createDatabase creates the test database on the freshly booted server.
// CREATE DATABASE cannot run inside a transaction, so it goes through a
// plain Exec on the master database.
func (c *MSSQLContainer) createDatabase(t T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := sql.Open("sqlserver", c.adminConnString("master"))
	if err != nil {
		t.Fatalf("mssql: failed opening admin connection to create database: %v", err)
	}
	defer db.Close()

	stmt := fmt.Sprintf(
		"IF DB_ID('%s') IS NULL CREATE DATABASE [%s]", mssqlDatabase, mssqlDatabase)
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		t.Fatalf("mssql: failed creating test database: %v", err)
	}
}

// countSessionsOn returns the number of sessions on the test database
// visible to the given admin connection, excluding that connection's own
// session (@@SPID). Filtering on DB_ID keeps system sessions for other
// databases out of the count.
//
// The admin connection must be a single, pinned *sql.DB (MaxOpenConns=1):
// SQL Server does not reap a session the instant its TCP connection closes,
// so a fresh admin connection per poll would race its own
// just-disconnected predecessor and intermittently count it. Reusing one
// pinned connection keeps @@SPID stable and self-exclusion exact.
func (c *MSSQLContainer) countSessionsOn(ctx context.Context, db *sql.DB) (int, error) {
	var count int
	row := db.QueryRowContext(ctx, `
		SELECT count(*) FROM sys.dm_exec_sessions
		WHERE database_id = DB_ID(@p1) AND session_id <> @@SPID`, c.Database)
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// ConnectionCount opens a single pinned sidecar admin connection and returns
// the number of sessions connected to the test database, excluding the
// sidecar's own session. Used by concurrency tests to assert how many
// upstream connections the agent established.
func (c *MSSQLContainer) ConnectionCount(t T) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err := c.openPinnedAdmin()
	if err != nil {
		t.Fatalf("mssqlstat: failed to open admin connection: %v", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("mssqlstat: admin ping failed: %v", err)
	}
	count, err := c.countSessionsOn(ctx, db)
	if err != nil {
		t.Fatalf("mssqlstat: failed to count sessions: %v", err)
	}
	return count
}

// openPinnedAdmin returns an admin *sql.DB pinned to exactly one underlying
// connection so its @@SPID stays stable across queries.
func (c *MSSQLContainer) openPinnedAdmin() (*sql.DB, error) {
	db, err := sql.Open("sqlserver", c.adminConnString(c.Database))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	return db, nil
}

// WaitForConnectionCount polls until the session count on the test database
// equals want or the timeout elapses. It holds a single pinned admin
// connection for the entire poll: opening a new admin connection per
// iteration would race SQL Server's lazy session reaping and count the
// previous iteration's just-closed admin session, so the poll could never
// observe 0. SQL Server also reaps the agent's session lazily after its TCP
// connection drops mid-query, which is why a poll (not a single snapshot) is
// needed.
func (c *MSSQLContainer) WaitForConnectionCount(t T, want int, timeout time.Duration) {
	t.Helper()

	db, err := c.openPinnedAdmin()
	if err != nil {
		t.Fatalf("mssqlstat: failed to open admin connection: %v", err)
	}
	defer db.Close()

	deadline := time.Now().Add(timeout)
	var last int
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		count, qErr := c.countSessionsOn(ctx, db)
		cancel()
		if qErr != nil {
			t.Fatalf("mssqlstat: failed to count sessions: %v", qErr)
		}
		last = count
		if last == want {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("mssqlstat: expected %d sessions after %v, last observed=%d", want, timeout, last)
}

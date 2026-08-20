//go:build integration

package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/microsoft/go-mssqldb"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// MSSQLContainer is a handle to one database on a SQL Server container.
// Credentials are fixed so test code can reference them directly.
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
)

// StartMSSQL returns a handle to the shared SQL Server with a private database
// for this test. One container per test cost ~224s of the suite's 571s (ENG-511).
func StartMSSQL(t T) *MSSQLContainer {
	t.Helper()
	base, err := bootMSSQL()
	if err != nil {
		t.Fatalf("failed to start mssql container: %v", err)
	}
	return base.forkDatabase(t)
}

// bootMSSQL boots the shared server once. OnceValues caches the failure too, so
// a broken Docker daemon costs one startup timeout instead of one per test.
var bootMSSQL = sync.OnceValues(bootMSSQLContainer)

// databaseSeq is the only producer of forked database names, which is what makes
// them safe to interpolate straight into a CREATE DATABASE.
var databaseSeq atomic.Uint64

func nextDatabaseName() string {
	return fmt.Sprintf("testdb_%d", databaseSeq.Add(1))
}

var (
	sharedMu     sync.Mutex
	sharedServer testcontainers.Container
)

// ShutdownSharedContainers terminates the shared SQL Server. Bounded because it
// runs inside TestMain, where a stuck Terminate would burn the go test timeout.
func ShutdownSharedContainers() {
	sharedMu.Lock()
	c := sharedServer
	sharedServer = nil
	sharedMu.Unlock()

	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := c.Terminate(ctx); err != nil {
		// No *testing.T here: m.Run has already returned. A leaked container
		// is worth reporting, and stderr is what go test surfaces.
		fmt.Fprintf(os.Stderr, "mssql: failed terminating shared container: %v\n", err)
	}
}

// bootMSSQLContainer boots SQL Server 2022. The readiness log line fires
// slightly before the sa login works, so waitForReady also pings.
func bootMSSQLContainer() (*MSSQLContainer, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "mcr.microsoft.com/mssql/server:2022-latest",
			ExposedPorts: []string{"1433/tcp"},
			Env: map[string]string{
				"ACCEPT_EULA":       "Y",
				"MSSQL_SA_PASSWORD": mssqlSAPassword,
				// Express boots fast and speaks the same TDS wire
				// protocol libhoop's MSSQL proxy targets.
				"MSSQL_PID": "Express",
				// Resident for the whole package now, so it overlaps every
				// other container. Express caps its pool at 1410MB anyway.
				"MSSQL_MEMORY_LIMIT_MB": "1024",
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
		return nil, err
	}
	sharedMu.Lock()
	sharedServer = container
	sharedMu.Unlock()

	mappedPort, err := container.MappedPort(ctx, "1433/tcp")
	if err != nil {
		return nil, fmt.Errorf("failed to get mapped mssql port: %w", err)
	}

	host, err := ContainerHost(ctx, container)
	if err != nil {
		return nil, fmt.Errorf("failed to get mssql container host: %w", err)
	}

	c := &MSSQLContainer{
		Host:      host,
		Port:      mappedPort.Port(),
		User:      mssqlSAUser,
		Password:  mssqlSAPassword,
		Database:  "master",
		Container: container,
	}

	// Block until sa can actually authenticate.
	if err := c.waitForReady(); err != nil {
		return nil, err
	}

	return c, nil
}

// forkDatabase gives the test a private database instead of a private server.
// countSessionsOn filters on DB_ID, so that is the boundary its assertions need.
func (c *MSSQLContainer) forkDatabase(t T) *MSSQLContainer {
	t.Helper()

	forked := *c
	forked.Database = nextDatabaseName()
	forked.createDatabase(t)
	return &forked
}

// adminConnString returns a DSN straight to the container, bypassing the agent.
// TLS is off because the bridged-proxy path under test also runs unencrypted.
func (c *MSSQLContainer) adminConnString(database string) string {
	return fmt.Sprintf("sqlserver://%s:%s@%s:%s?database=%s&encrypt=disable",
		c.User, c.Password, c.Host, c.Port, database)
}

// ConnString returns a direct DSN to the test database. Used by sidecar
// admin connections in concurrency tests.
func (c *MSSQLContainer) ConnString() string {
	return c.adminConnString(c.Database)
}

func (c *MSSQLContainer) waitForReady() error {
	deadline := time.Now().Add(120 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if lastErr = c.ping("master"); lastErr == nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("mssql container never became ready within 120s: %w", lastErr)
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

// createDatabase creates c.Database. CREATE DATABASE cannot run inside a
// transaction, so it goes through a plain Exec on master.
func (c *MSSQLContainer) createDatabase(t T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := sql.Open("sqlserver", c.adminConnString("master"))
	if err != nil {
		t.Fatalf("mssql: failed opening admin connection to create database: %v", err)
	}
	defer db.Close()

	stmt := fmt.Sprintf(
		"IF DB_ID('%s') IS NULL CREATE DATABASE [%s]", c.Database, c.Database)
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		t.Fatalf("mssql: failed creating database %s: %v", c.Database, err)
	}
}

// countSessionsOn counts sessions on c.Database, excluding db's own (@@SPID).
// db must come from openPinnedAdmin or the count races its own predecessor.
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

// ConnectionCount returns the session count on the test database, excluding the
// sidecar's own. Concurrency tests assert the agent's upstream connections.
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

// WaitForConnectionCount polls until the session count equals want. SQL Server
// reaps sessions lazily, so a single snapshot after SessionClose still sees 1.
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

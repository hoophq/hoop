//go:build integration

package e2e_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	go_ora "github.com/sijms/go-ora/v2"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	oracleUser     = "app"
	oraclePassword = "app"
)

// The default is Oracle Free 23. SIDECAR_E2E_ORACLE_IMAGE runs the same tests
// against another gvenzl image, which honours the same ORACLE_PASSWORD and
// APP_USER variables, e.g. gvenzl/oracle-xe:21-slim-faststart with
// SIDECAR_E2E_ORACLE_SERVICE=XEPDB1. SIDECAR_E2E_ORACLE_PLATFORM picks the
// image platform: linux/amd64 on Apple silicon runs what CI runs, and OCI
// sends platform-dependent fields.
var (
	oracleImage    = envOr("SIDECAR_E2E_ORACLE_IMAGE", "gvenzl/oracle-free:23-slim-faststart")
	oracleService  = envOr("SIDECAR_E2E_ORACLE_SERVICE", "FREEPDB1")
	oraclePlatform = os.Getenv("SIDECAR_E2E_ORACLE_PLATFORM")
)

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

const oracleMaskingConfig = `
log_level: info

audit:
  file: "-"
  memory_buffer: 64

listeners:
  - name: appdb
    protocol: oracle
    listen: {{listen}}
    upstream: {{upstream}}
    guardrails:
      mode: enforce
      rules:
        - name: no-destructive-oracle
          type: operation
          operations: [delete]
          message: destructive statements are not permitted on appdb
    mask:
      rules:
        - {name: email-column, columns: [email], strategy: redact}
`

type oracleDB struct {
	addr      string
	container testcontainers.Container
	// client runs SQL*Plus against the relay when
	// SIDECAR_E2E_ORACLE_CLIENT_IMAGE names another image, e.g. SQL*Plus 23
	// from gvenzl/oracle-free:23-slim-faststart against a 21c database.
	client testcontainers.Container
}

// startOracle creates an isolated database for each test. The database log
// means its PDB and app user are ready; a listening port alone does not.
func startOracle(t *testing.T) *oracleDB {
	t.Helper()
	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:         oracleImage,
			ImagePlatform: oraclePlatform,
			ExposedPorts:  []string{"1521/tcp"},
			Env: map[string]string{
				"ORACLE_PASSWORD":   oraclePassword,
				"APP_USER":          oracleUser,
				"APP_USER_PASSWORD": oraclePassword,
			},
			// An SQLPlus client running inside the database container reaches
			// the host's relay through this bridge name on Linux and Docker Desktop.
			ExtraHosts: []string{"host.docker.internal:host-gateway"},
			WaitingFor: wait.ForAll(
				wait.ForLog("DATABASE IS READY TO USE!").WithStartupTimeout(8*time.Minute),
				wait.ForListeningPort("1521/tcp").WithStartupTimeout(8*time.Minute),
			).WithDeadline(8 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start oracle: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Errorf("leaked oracle container: %v", err)
		}
	})
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("oracle container host: %v", err)
	}
	port, err := container.MappedPort(ctx, "1521/tcp")
	if err != nil {
		t.Fatalf("oracle container port: %v", err)
	}
	db := &oracleDB{addr: net.JoinHostPort(host, port.Port()), container: container}
	if image := os.Getenv("SIDECAR_E2E_ORACLE_CLIENT_IMAGE"); image != "" {
		db.client = startOracleClient(t, image)
	}
	seedOracle(t, db.addr)
	return db
}

// startOracleClient runs an Oracle image as a SQL*Plus client only.
func startOracleClient(t *testing.T, image string) testcontainers.Container {
	t.Helper()
	client, err := testcontainers.GenericContainer(context.Background(), testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:         image,
			ImagePlatform: oraclePlatform,
			Entrypoint:    []string{"sleep", "infinity"},
			ExtraHosts:    []string{"host.docker.internal:host-gateway"},
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start oracle client: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(client); err != nil {
			t.Errorf("leaked oracle client container: %v", err)
		}
	})
	return client
}

func oracleDSN(t *testing.T, addr string, prefetchRows int) string {
	t.Helper()
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("invalid oracle address %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("invalid oracle port %q: %v", portText, err)
	}
	options := map[string]string{"CONNECTION TIMEOUT": "20", "PREFETCH_ROWS": strconv.Itoa(prefetchRows)}
	return go_ora.BuildUrl(host, port, oracleService, oracleUser, oraclePassword, options)
}

func openOracle(t *testing.T, addr string, prefetchRows int) *sql.DB {
	t.Helper()
	db, err := sql.Open("oracle", oracleDSN(t, addr, prefetchRows))
	if err != nil {
		t.Fatalf("open oracle: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close oracle: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for {
		pingCtx, pingCancel := context.WithTimeout(ctx, stmtTimeout)
		err = db.PingContext(pingCtx)
		pingCancel()
		if err == nil {
			return db
		}
		if ctx.Err() != nil {
			t.Fatalf("ping oracle: %v", err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// closeDenied closes a pool whose session the relay ended after a denial.
// go-ora then writes LOGOFF to that dead pooled connection and fails with a
// broken pipe or EOF: that is the denial, not a test failure. The Cleanup
// that openOracle registered closes the pool again, and DB.Close is
// idempotent, so it still reports errors from every other pool.
func closeDenied(db *sql.DB) { _ = db.Close() }

// Seed directly against Oracle, not through the relay under test.
func seedOracle(t *testing.T, addr string) {
	t.Helper()
	db := openOracle(t, addr, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, `CREATE TABLE people (
		id NUMBER(10) PRIMARY KEY,
		name VARCHAR2(64),
		email VARCHAR2(128),
		code VARCHAR2(32),
		nick VARCHAR2(64),
		note VARCHAR2(128)
	)`); err != nil {
		t.Fatalf("create oracle fixture: %v", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin oracle fixture: %v", err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO people (id, name, email, code, nick, note) VALUES (:1, :2, :3, :4, :5, :6)`)
	if err != nil {
		t.Fatalf("prepare oracle fixture: %v", err)
	}
	for id := 1; id <= 40; id++ {
		name := fmt.Sprintf("Person %02d", id)
		var email any = fmt.Sprintf("user%02d@example.com", id)
		code := fmt.Sprintf("P-%03d", id)
		nick := fmt.Sprintf("p%02d", id)
		var note any = fmt.Sprintf("note %02d", id)
		if id == 1 {
			name, email, code, nick, note = "Ada Lovelace", "ada@example.com", "AL-001", "Ada", "first row"
		}
		if id == 3 {
			email, note = nil, nil
		}
		if _, err := stmt.ExecContext(ctx, id, name, email, code, nick, note); err != nil {
			t.Fatalf("insert oracle fixture row %d: %v", id, err)
		}
	}
	if err := stmt.Close(); err != nil {
		t.Fatalf("close oracle fixture statement: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit oracle fixture: %v", err)
	}
}

// sqlplus uses the official OCI client bundled with the image. Direct checks
// use loopback inside Oracle's container; relay checks use the host gateway,
// from the client container when one is configured.
// Return SQLPlus output on SQL errors so callers can assert the native ORA code.
func (db *oracleDB) sqlplus(t *testing.T, endpoint, script string) (string, error) {
	t.Helper()
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		t.Fatalf("invalid SQLPlus endpoint %q: %v", endpoint, err)
	}
	runner := db.container
	if endpoint == db.addr {
		host, port = "127.0.0.1", "1521"
	} else {
		host = "host.docker.internal"
		if db.client != nil {
			runner = db.client
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	connect := fmt.Sprintf("%s/%s@//%s:%s/%s", oracleUser, oraclePassword, host, port, oracleService)
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", runner.GetContainerID(), "sqlplus", "-S", "-L", connect)
	cmd.Stdin = strings.NewReader("SET PAGESIZE 0\nSET FEEDBACK OFF\nSET HEADING OFF\nSET LINESIZE 32767\nSET TRIMSPOOL ON\nWHENEVER SQLERROR EXIT SQL.SQLCODE\n" + script + "\nEXIT\n")
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err = cmd.Run()
	return output.String(), err
}

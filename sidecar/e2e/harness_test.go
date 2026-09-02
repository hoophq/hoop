//go:build integration

package e2e_test

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// The scaffolding every test in this package shares: a MySQL container, the
// real hoop-inspect binary in front of it, and a driver connection through
// the relay.

const (
	dbUser = "root"
	dbPass = "e2e-secret"
	dbName = "appdb"
)

// mysqlImage is pinned to MySQL 8 rather than MariaDB.
//
// The agent integration suite uses MariaDB because it boots faster, and for
// the wire protocol that is a fair trade. It is not a fair trade here: MariaDB
// authenticates with mysql_native_password, and the rewriter bug this suite
// was built to catch only appears under caching_sha2_password, MySQL 8's
// default. Testing the faster image would pass while the shipped path hung.
const mysqlImage = "mysql:8"

// startMySQL boots the upstream and returns its host:port.
//
// The wait strategy watches for the SECOND "ready for connections" line:
// MySQL's entrypoint runs a throwaway server to initialize the data
// directory and that one logs the same message, so matching the first
// connects to a server that is about to be shut down.
func startMySQL(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        mysqlImage,
		ExposedPorts: []string{"3306/tcp"},
		Env: map[string]string{
			"MYSQL_ROOT_PASSWORD": dbPass,
			"MYSQL_DATABASE":      dbName,
		},
		WaitingFor: wait.ForAll(
			wait.ForLog("ready for connections").WithOccurrence(2),
			wait.ForListeningPort("3306/tcp"),
		).WithDeadline(4 * time.Minute),
	}

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start mysql: %v", err)
	}
	t.Cleanup(func() {
		// Reported, not discarded: a container this suite failed to remove
		// stays on the runner holding a port, and the next job to want that
		// port fails somewhere unrelated. t.Errorf rather than Fatalf —
		// the test itself already finished, and its verdict should stand.
		if err := testcontainers.TerminateContainer(c); err != nil {
			t.Errorf("leaked mysql container: %v", err)
		}
	})

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	port, err := c.MappedPort(ctx, "3306/tcp")
	if err != nil {
		t.Fatalf("container port: %v", err)
	}
	addr := net.JoinHostPort(host, port.Port())

	seed(t, addr)
	return addr
}

// seed creates the fixture table directly on the upstream, bypassing the
// relay. The relay is what is under test; building its fixtures through it
// would make a masking bug look like a setup failure.
func seed(t *testing.T, addr string) {
	t.Helper()
	db := open(t, addr)
	defer db.Close()

	for _, stmt := range []string{
		`CREATE TABLE customers (
			id    INT PRIMARY KEY,
			name  VARCHAR(64),
			email VARCHAR(128),
			card  VARCHAR(32)
		)`,
		`INSERT INTO customers VALUES
			(1, 'Ada Lovelace', 'ada@example.com', '4111111111111111'),
			(2, 'Bob Stone',    'bob@example.com', '5500005555555559'),
			(3, 'No Email',     NULL,              NULL)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

// buildSidecar compiles the real hoop-inspect binary once per run.
//
// sidecar/cmd is what ships, and it is the only composition that links the
// detection plugin masking needs. A test calling daemon.Run in-process would
// have to pass a nil Plugin, which disables masking entirely — it would
// exercise the unmasked path and pass.
var buildSidecar = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "hoop-inspect-e2e")
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "hoop-inspect")

	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "../cmd"

	// GOWORK is not inherited from this module's own build. This module runs
	// under GOWORK=off — its testcontainers tree has no business in the
	// workspace — but sidecar/cmd is a different module, and forcing that
	// setting onto it breaks its libhoop imports.
	//
	// SIDECAR_E2E_GOWORK overrides what cmd is built against. Unset, cmd
	// resolves through the repository's own go.work and therefore the
	// PUBLISHED libhoop pin, which is what CI must test. Point it at a
	// workspace with a local `use` directive to test an unmerged codec
	// against a real server before it lands.
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	cmd.Env = slices.DeleteFunc(cmd.Env, func(kv string) bool {
		return strings.HasPrefix(kv, "GOWORK=")
	})
	if w := os.Getenv("SIDECAR_E2E_GOWORK"); w != "" {
		cmd.Env = append(cmd.Env, "GOWORK="+w)
	}

	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build hoop-inspect: %v\n%s", err, out)
	}
	return bin, nil
})

// sidecar is a running relay and the audit trail it produced.
type sidecar struct {
	addr   string
	mu     sync.Mutex
	events []auditEvent
}

// auditEvent is the subset of the relay's JSONL audit output these tests
// assert on. The trail is a product surface — an operator reads it to answer
// "did this statement run" — so a test that only checked the client's view
// would miss a denial that never got recorded.
type auditEvent struct {
	Kind      string            `json:"kind"`
	Operation string            `json:"operation"`
	Statement string            `json:"statement"`
	Allowed   bool              `json:"allowed"`
	Rule      string            `json:"rule"`
	Direction string            `json:"direction"`
	Masked    []string          `json:"masked_entities"`
	Count     int               `json:"masked_count"`
	Metadata  map[string]string `json:"metadata"`
}

// startSidecar launches the relay in front of upstream with the given config
// body and blocks until its listener accepts.
//
// config is the YAML with two placeholders substituted: {{listen}} and
// {{upstream}}. Ports are allocated by the OS rather than hardcoded so
// parallel tests and a busy CI runner cannot collide.
func startSidecar(t *testing.T, upstream, config string) *sidecar {
	t.Helper()

	bin, err := buildSidecar()
	if err != nil {
		t.Fatalf("%v", err)
	}

	listen := freePort(t)
	body := strings.NewReplacer(
		"{{listen}}", listen,
		"{{upstream}}", upstream,
	).Replace(config)

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "-config", path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatalf("stdout pipe: %v", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start sidecar: %v", err)
	}

	s := &sidecar{addr: listen}

	// The audit trail goes to stdout as JSONL (audit.file: "-"). Draining it
	// on a goroutine doubles as backpressure relief: a full pipe buffer would
	// block the relay's audit writes and stall the data path.
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for sc.Scan() {
			var ev auditEvent
			if err := json.Unmarshal(sc.Bytes(), &ev); err != nil || ev.Kind == "" {
				continue // a slog line, not an audit event
			}
			s.mu.Lock()
			s.events = append(s.events, ev)
			s.mu.Unlock()
		}
	}()

	t.Cleanup(func() {
		cancel()
		err := cmd.Wait()
		<-done

		// cancel() kills the process, so a "signal: killed" style error is
		// the expected shutdown and says nothing. Anything else means the
		// relay exited on its own before the test finished with it —
		// a panic or a config refusal — which would otherwise surface only
		// as a confusing connection error in whichever test ran next.
		if err != nil && ctx.Err() == nil {
			t.Errorf("sidecar exited before teardown: %v", err)
		}
	})

	waitForListener(t, listen)
	return s
}

// dial opens a pooled connection THROUGH the relay.
//
// Every statement carries a read deadline, and that is the single most
// important property of this harness. The failure mode it exists to catch is
// a relay that stops answering: the client then blocks in a socket read
// forever, and without a deadline the whole PACKAGE sits there until Go's
// test timeout fires — one anonymous "test timed out after 20m" naming no
// test, twenty minutes of CI, and a stack in the driver rather than at the
// query that hung. Verified: reverting the rewriter fix and running without
// this produced exactly that.
//
// readTimeout makes the same hang a named failure in seconds, at the
// statement responsible.
func (s *sidecar) dial(t *testing.T, params string) *sql.DB {
	t.Helper()
	if params != "" {
		params += "&"
	}
	params += "readTimeout=" + stmtTimeout.String() + "&writeTimeout=" + stmtTimeout.String()
	return openDSN(t, dsn(s.addr, params))
}

// stmtTimeout bounds one statement.
//
// Generous next to the workload — every query here is against three rows on
// a loopback socket — and short next to a hang, which is unbounded. A CI
// runner stalling on container I/O has to stay comfortably inside it.
const stmtTimeout = 20 * time.Second

// auditEvents returns a snapshot of the trail so far.
func (s *sidecar) auditEvents() []auditEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]auditEvent(nil), s.events...)
}

// waitForAudit polls until match finds an event or the deadline passes.
//
// The audit sink is asynchronous — it buffers and flushes on its own clock —
// so a test that read the trail immediately after a query would race the
// flush and fail intermittently, which is worse than no assertion.
func (s *sidecar) waitForAudit(t *testing.T, what string, match func(auditEvent) bool) auditEvent {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, ev := range s.auditEvents() {
			if match(ev) {
				return ev
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no audit event for %s within 10s; trail had %d events",
				what, len(s.auditEvents()))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func dsn(addr, params string) string {
	d := fmt.Sprintf("%s:%s@tcp(%s)/%s?tls=false", dbUser, dbPass, addr, dbName)
	if params != "" {
		d += "&" + params
	}
	return d
}

func open(t *testing.T, addr string) *sql.DB { return openDSN(t, dsn(addr, "")) }

func openDSN(t *testing.T, d string) *sql.DB {
	t.Helper()
	db, err := sql.Open("mysql", d)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// The upstream accepts connections before it finishes its first-boot
	// grants, so the first dial can legitimately fail.
	deadline := time.Now().Add(90 * time.Second)
	for {
		if err = db.Ping(); err == nil {
			return db
		}
		if time.Now().After(deadline) {
			t.Fatalf("ping: %v", err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// freePort asks the OS for an unused port and returns it as host:port.
//
// There is an unavoidable window between closing this listener and the relay
// binding it. Hardcoding ports instead would trade a rare race for a certain
// collision whenever two of these run on one runner.
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("release port: %v", err)
	}
	return addr
}

func waitForListener(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			c.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("sidecar never listened on %s: %v", addr, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

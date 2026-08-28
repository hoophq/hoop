package migrations

import (
	"io"
	"log/slog"
	"net/url"
	"strings"
	"testing"
)

func discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// The gateway migrates the same database with golang-migrate's default table
// name; sharing it makes each product misread the other's version and
// silently apply nothing.
func TestWithMigrationsTableIsolatesUsFromTheGateway(t *testing.T) {
	got, err := withMigrationsTable("postgres://hoop:hoop@localhost:5432/hoop?sslmode=disable")
	if err != nil {
		t.Fatalf("withMigrationsTable returned an error: %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("result did not parse: %v", err)
	}
	if v := u.Query().Get("x-migrations-table"); v != Table {
		t.Errorf("x-migrations-table = %q, want %q", v, Table)
	}
	if v := u.Query().Get("sslmode"); v != "disable" {
		t.Errorf("sslmode = %q, want the caller's value to survive", v)
	}
}

func TestWithMigrationsTableKeepsAnOperatorOverride(t *testing.T) {
	got, err := withMigrationsTable("postgres://localhost/hoop?x-migrations-table=mine")
	if err != nil {
		t.Fatalf("withMigrationsTable returned an error: %v", err)
	}
	u, _ := url.Parse(got)
	if v := u.Query().Get("x-migrations-table"); v != "mine" {
		t.Errorf("x-migrations-table = %q, want the operator's value", v)
	}
}

func TestWithMigrationsTableRejectsANonPostgresScheme(t *testing.T) {
	for _, uri := range []string{"mysql://localhost/hoop", "localhost:5432/hoop", "postgresfoo://x/y"} {
		if _, err := withMigrationsTable(uri); err == nil {
			t.Errorf("withMigrationsTable accepted %q", uri)
		}
	}
}

func TestWithMigrationsTableDoesNotLeakThePassword(t *testing.T) {
	_, err := withMigrationsTable("postgres://hoop:p%ssw0rd@localhost:5432/hoop")
	if err == nil {
		t.Fatal("withMigrationsTable accepted a URI with an invalid escape")
	}
	if strings.Contains(err.Error(), "ssw0rd") {
		t.Errorf("the credential reached the error message: %v", err)
	}
}

// serve compares Latest against the applied version before accepting traffic,
// so it must match what is embedded.
func TestLatestMatchesTheEmbeddedFiles(t *testing.T) {
	entries, err := FS.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the embedded FS: %v", err)
	}
	upFiles := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			upFiles++
		}
	}
	if upFiles == 0 {
		t.Fatal("no .up.sql embedded; the go:embed pattern is not matching")
	}

	latest, err := NewRunner(discard(), "postgres://localhost/x", "").Latest()
	if err != nil {
		t.Fatalf("Latest returned an error: %v", err)
	}
	if int(latest) != upFiles {
		t.Errorf("Latest = %d but %d .up.sql files are embedded; the sequence has a gap, so Verify would compare against the wrong number", latest, upFiles)
	}
}

// A migration whose down was never written cannot be rolled back.
func TestEveryMigrationHasBothDirections(t *testing.T) {
	entries, err := FS.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the embedded FS: %v", err)
	}
	seen := map[string]map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		for _, dir := range []string{"up", "down"} {
			suffix := "." + dir + ".sql"
			if strings.HasSuffix(name, suffix) {
				base := strings.TrimSuffix(name, suffix)
				if seen[base] == nil {
					seen[base] = map[string]bool{}
				}
				seen[base][dir] = true
			}
		}
	}
	for base, dirs := range seen {
		if !dirs["up"] || !dirs["down"] {
			t.Errorf("migration %s has up=%t down=%t, both are required", base, dirs["up"], dirs["down"])
		}
	}
}

func TestDownRefusesANonPositiveStepCount(t *testing.T) {
	r := NewRunner(discard(), "postgres://localhost/x", "")
	for _, steps := range []int{0, -1} {
		if err := r.Down(steps); err == nil {
			t.Errorf("Down(%d) was accepted; unbounded rollback is how a schema gets dropped by a typo", steps)
		}
	}
}

package aianalyzer

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
)

func TestBuildMetadataScriptSchemaScoping(t *testing.T) {
	// No schema -> postgres searches every user schema instead of assuming public.
	script, errMsg := buildMetadataScript("postgres", runMetadataQueryArgs{Operation: "table_size", Table: "sessions"})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if strings.Contains(script, "'public'") {
		t.Errorf("no-schema table_size must not hardcode public:\n%s", script)
	}
	if !strings.Contains(script, "NOT IN ('pg_catalog', 'information_schema')") {
		t.Errorf("no-schema table_size must search all user schemas:\n%s", script)
	}

	// Explicit schema -> exact filter.
	script, _ = buildMetadataScript("postgres", runMetadataQueryArgs{Operation: "table_size", Table: "sessions", Schema: "private"})
	if !strings.Contains(script, "n.nspname = 'private'") {
		t.Errorf("explicit schema not applied:\n%s", script)
	}

	// mysql explicit schema filters information_schema lookups.
	script, _ = buildMetadataScript("mysql", runMetadataQueryArgs{Operation: "table_indexes", Table: "orders", Schema: "shop"})
	if !strings.Contains(script, "table_schema = 'shop'") {
		t.Errorf("mysql schema filter missing:\n%s", script)
	}
}

func TestEmptyResultMessage(t *testing.T) {
	sizeArgs := runMetadataQueryArgs{Operation: "table_size", Table: "sessions", Schema: "private"}

	// postgres: "(0 rows)" footer -> explicit not-found error.
	msg := emptyResultMessage("postgres", sizeArgs, "schema relname n_live_tup total_size\n(0 rows)\n")
	if !strings.Contains(msg, `table "sessions" not found`) || !strings.Contains(msg, `schema "private"`) {
		t.Errorf("empty postgres table_size message = %q", msg)
	}

	// postgres: a matched row passes through untouched.
	if msg := emptyResultMessage("postgres", sizeArgs, "schema relname n_live_tup total_size\nprivate sessions 443 408 kB\n(1 row)\n"); msg != "" {
		t.Errorf("non-empty result flagged as empty: %q", msg)
	}

	// postgres table_indexes: only when BOTH statements are empty.
	idxArgs := runMetadataQueryArgs{Operation: "table_indexes", Table: "sessions"}
	if msg := emptyResultMessage("postgres", idxArgs, "a b c\n(0 rows)\nd e f\nrow1\n(1 row)\n"); msg != "" {
		t.Errorf("partial index result flagged as empty: %q", msg)
	}
	if msg := emptyResultMessage("postgres", idxArgs, "a b c\n(0 rows)\nd e f\n(0 rows)\n"); !strings.Contains(msg, "no indexes found") {
		t.Errorf("fully-empty index result not flagged: %q", msg)
	}

	// mysql: empty output -> not found; explain is never rewritten.
	if msg := emptyResultMessage("mysql", sizeArgs, "  \n"); !strings.Contains(msg, "not found") {
		t.Errorf("empty mysql output not flagged: %q", msg)
	}
	if msg := emptyResultMessage("postgres", runMetadataQueryArgs{Operation: "explain"}, ""); msg != "" {
		t.Errorf("explain output must pass through: %q", msg)
	}
}

func TestSessionDatabaseFromScript(t *testing.T) {
	// Webapp postgres prefix: \set QUIET on\n\c hoop\n\set QUIET off\n<query>
	got := SessionDatabaseFromScript("postgres", "\\set QUIET on\n\\c hoop\n\\set QUIET off\nSELECT * FROM private.sessions;")
	if got != "hoop" {
		t.Errorf("postgres extraction = %q, want hoop", got)
	}
	// Webapp mysql prefix: use shop;\n<query>
	if got := SessionDatabaseFromScript("mysql", "use shop;\nSELECT 1;"); got != "shop" {
		t.Errorf("mysql extraction = %q, want shop", got)
	}
	// No directive -> connection default applies.
	if got := SessionDatabaseFromScript("postgres", "SELECT 1;"); got != "" {
		t.Errorf("no-directive extraction = %q, want empty", got)
	}
	// Subtype without a database directive -> empty.
	if got := SessionDatabaseFromScript("dynamodb", "use shop;\nscan"); got != "" {
		t.Errorf("dynamodb extraction = %q, want empty", got)
	}
}

func TestPrependDatabaseDirective(t *testing.T) {
	out := prependDatabaseDirective("postgres", "hoop", "EXPLAIN SELECT 1;")
	if !strings.HasPrefix(out, "\\set QUIET on\n\\c hoop\n\\set QUIET off\n") {
		t.Errorf("postgres directive missing:\n%s", out)
	}
	if out := prependDatabaseDirective("mysql", "shop", "EXPLAIN SELECT 1;"); !strings.HasPrefix(out, "use shop;\n") {
		t.Errorf("mysql directive missing:\n%s", out)
	}
	// Empty or unsafe database names leave the script untouched.
	if out := prependDatabaseDirective("postgres", "", "SELECT 1;"); out != "SELECT 1;" {
		t.Errorf("empty db must be no-op, got:\n%s", out)
	}
	if out := prependDatabaseDirective("postgres", "bad;db", "SELECT 1;"); out != "SELECT 1;" {
		t.Errorf("unsafe db must be no-op, got:\n%s", out)
	}
}

func TestBuildMetadataScriptNewDialects(t *testing.T) {
	// mssql explain: SHOWPLAN batches, never executes the statement.
	script, errMsg := buildMetadataScript("mssql", runMetadataQueryArgs{Operation: "explain", Query: "DELETE FROM orders"})
	if errMsg != "" || !strings.HasPrefix(script, "SET SHOWPLAN_ALL ON\nGO\n") || !strings.Contains(script, "SET SHOWPLAN_ALL OFF") {
		t.Errorf("mssql explain script wrong (err=%q):\n%s", errMsg, script)
	}
	// mssql table_size: schema-qualified object + explicit not-found guard.
	script, _ = buildMetadataScript("mssql", runMetadataQueryArgs{Operation: "table_size", Table: "orders", Schema: "sales"})
	if !strings.Contains(script, "OBJECT_ID(N'sales.orders')") || !strings.Contains(script, "sp_spaceused") {
		t.Errorf("mssql table_size script wrong:\n%s", script)
	}
	// mongodb: explain rejected, stats/index scripts use getCollection.
	if _, errMsg = buildMetadataScript("mongodb", runMetadataQueryArgs{Operation: "explain", Query: "db.a.find()"}); !strings.Contains(errMsg, "not supported for mongodb") {
		t.Errorf("mongodb explain not rejected: %q", errMsg)
	}
	script, _ = buildMetadataScript("mongodb", runMetadataQueryArgs{Operation: "table_size", Table: "users"})
	if !strings.Contains(script, "db.getCollection('users').stats()") {
		t.Errorf("mongodb stats script wrong:\n%s", script)
	}
	script, _ = buildMetadataScript("mongodb", runMetadataQueryArgs{Operation: "table_indexes", Table: "users"})
	if !strings.Contains(script, "getIndexes()") {
		t.Errorf("mongodb indexes script wrong:\n%s", script)
	}
	// oracledb: explain uses dbms_xplan; size honors owner.
	script, _ = buildMetadataScript("oracledb", runMetadataQueryArgs{Operation: "explain", Query: "SELECT * FROM t;"})
	if !strings.HasPrefix(script, "EXPLAIN PLAN FOR SELECT * FROM t;") || !strings.Contains(script, "dbms_xplan.display()") {
		t.Errorf("oracle explain script wrong:\n%s", script)
	}
	script, _ = buildMetadataScript("oracledb", runMetadataQueryArgs{Operation: "table_size", Table: "t", Schema: "hr"})
	if !strings.Contains(script, "dba_segments") || !strings.Contains(script, "UPPER('hr')") {
		t.Errorf("oracle table_size script wrong:\n%s", script)
	}
}

func TestPrependDatabaseDirectiveNewDialects(t *testing.T) {
	if out := prependDatabaseDirective("mssql", "sales", "SELECT 1;"); !strings.HasPrefix(out, "USE [sales];\n") {
		t.Errorf("mssql directive missing:\n%s", out)
	}
	if out := prependDatabaseDirective("mongodb", "shop", "db.a.stats()"); !strings.HasPrefix(out, "db = db.getSiblingDB('shop');\n") {
		t.Errorf("mongodb directive missing:\n%s", out)
	}
	// oracledb has no database directive.
	if out := prependDatabaseDirective("oracledb", "orcl", "SELECT 1;"); out != "SELECT 1;" {
		t.Errorf("oracledb must be no-op:\n%s", out)
	}
}

func TestSessionDatabaseFromScriptNewDialects(t *testing.T) {
	if got := SessionDatabaseFromScript("mssql", "SET NOCOUNT ON;\nUSE [sales];\nSELECT 1;"); got != "sales" {
		t.Errorf("mssql extraction = %q, want sales", got)
	}
	if got := SessionDatabaseFromScript("mongodb", "use shop;\ndb.users.find()"); got != "shop" {
		t.Errorf("mongodb extraction = %q, want shop", got)
	}
}

func TestEmptyResultMessageNewDialects(t *testing.T) {
	sizeArgs := runMetadataQueryArgs{Operation: "table_size", Table: "t"}
	if msg := emptyResultMessage("oracledb", sizeArgs, "no rows selected\n"); !strings.Contains(msg, "not found") {
		t.Errorf("oracle empty not flagged: %q", msg)
	}
	// mssql/mongodb outputs pass through (they carry their own signals).
	if msg := emptyResultMessage("mssql", sizeArgs, "table not found in the connection database\n"); msg != "" {
		t.Errorf("mssql output must pass through: %q", msg)
	}
	if msg := emptyResultMessage("mongodb", sizeArgs, ""); msg != "" {
		t.Errorf("mongodb output must pass through: %q", msg)
	}
}

// The explain builders only wrap the FIRST statement, and every client splits
// the script on the separator — so a chained statement would be executed, not
// planned. This is the guard that keeps "explain" read-only.
func TestValidateSingleStatementRejectsChainedStatements(t *testing.T) {
	chained := []struct{ subtype, query string }{
		{"postgres", "SELECT 1; DELETE FROM users"},
		{"mysql", "SELECT 1; DROP TABLE users"},
		{"oracledb", "SELECT 1 FROM dual; DELETE FROM users"},
		{"mssql", "SELECT 1\nGO\nDELETE FROM users"},
	}
	for _, c := range chained {
		if msg := validateSingleStatement(c.subtype, c.query); msg == "" {
			t.Errorf("%s: chained statement %q accepted; it would execute", c.subtype, c.query)
		}
	}

	// Single statements, with and without a trailing semicolon, must pass.
	for _, q := range []string{"SELECT * FROM t WHERE id = 1", "DELETE FROM t;", "  UPDATE t SET a = 1 ;  "} {
		if msg := validateSingleStatement("postgres", q); msg != "" {
			t.Errorf("single statement %q rejected: %s", q, msg)
		}
	}
	// A bare GO is only a batch separator for mssql.
	if msg := validateSingleStatement("postgres", "SELECT 1\nGO\n"); msg != "" {
		t.Errorf("postgres query containing GO rejected: %s", msg)
	}
}

// runMetadataQuery must apply the guard before building any script.
func TestRunMetadataQueryRejectsChainedExplain(t *testing.T) {
	e := &gatewayToolExecutor{
		orgID: "org",
		conn: &models.Connection{
			Name:    "pg",
			Type:    "database",
			SubType: sql.NullString{String: "postgres", Valid: true},
		},
	}
	out, isErr := e.runMetadataQuery(context.Background(), `{"operation":"explain","query":"SELECT 1; DELETE FROM users"}`)
	if !isErr {
		t.Fatalf("chained explain accepted (output=%q); the DELETE would run", out)
	}
	if !strings.Contains(out, "single statement") {
		t.Errorf("unexpected rejection message: %q", out)
	}
}

// The tool must never surface connection secrets to the model.
func TestConnectionContextExcludesSecrets(t *testing.T) {
	e := &gatewayToolExecutor{
		conn: &models.Connection{
			Name:    "pg",
			Type:    "database",
			SubType: sql.NullString{String: "postgres", Valid: true},
			Envs:    map[string]string{"envvar:PASS": "cGFzc3dvcmQ="},
		},
	}
	out, isErr := e.connectionContext()
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	for _, leaked := range []string{"cGFzc3dvcmQ=", "envvar:PASS", "PASS"} {
		if strings.Contains(out, leaked) {
			t.Errorf("connection context leaked secret material %q: %s", leaked, out)
		}
	}
}

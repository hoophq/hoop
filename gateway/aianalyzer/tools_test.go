package aianalyzer

import (
	"strings"
	"testing"
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
	// Unsupported subtype -> empty.
	if got := SessionDatabaseFromScript("mongodb", "use shop;\ndb.find()"); got != "" {
		t.Errorf("mongodb extraction = %q, want empty", got)
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

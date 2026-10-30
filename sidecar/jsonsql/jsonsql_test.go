package jsonsql_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/jsonsql"
	"github.com/hoophq/hoop/sidecar/lexer"
)

func reader(refuse ...jsonsql.RefuseValue) *jsonsql.Reader {
	return jsonsql.New(jsonsql.Options{
		Dialect:       lexer.Spark,
		Tools:         []string{"execute_sql"},
		Refuse:        refuse,
		RefuseMessage: "ask for disposition INLINE",
	})
}

// The Reader knows JSON and JSON-RPC, not the protocol that carried them: a
// plain object, a tools/call, a batch, and the places SQL should be but is
// not, each read the same way whatever the transport. JSON-RPC members
// decide nothing where the SQL is not a tool argument: Databricks' Statement
// Execution API runs a body that also holds them.
func TestReadFindsTheSQL(t *testing.T) {
	call := func(q string) string {
		return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"execute_sql","arguments":{"query":"` + q + `"}}}`
	}
	for _, tc := range []struct {
		name, doc, field string
		want             []string // an operation, or "?reason" for Unreadable
	}{
		{"object", `{"statement":"DROP TABLE t"}`, "statement", []string{"drop"}},
		{"jsonrpc member on a plain field", `{"jsonrpc":1,"statement":"DROP TABLE t"}`, "statement", []string{"drop"}},
		{"another tool on a plain field", `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"list_tables"},"statement":"DROP TABLE t"}`,
			"statement", []string{"drop"}},
		{"tools/call", call("DELETE FROM t"), "params.arguments.query", []string{"delete"}},
		{"another tool", `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"list_tables"}}`, "params.arguments.query", nil},
		{"batch", "[" + call("SELECT 1") + `,{"jsonrpc":"2.0","method":"notifications/initialized"},` + call("DROP TABLE t") + "]",
			"params.arguments.query", []string{"select", "drop"}},
		{"no SQL string", `{"statement":42}`, "statement", []string{`?no SQL string at "statement"`}},
		{"not an object", `[1]`, "statement", []string{"?not an object"}},
		{"not JSON", `DROP TABLE t`, "statement", []string{"?not JSON"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := reader().Read(tc.doc, tc.field)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d SQL, want %d: %+v", len(got), len(tc.want), got)
			}
			for i, w := range tc.want {
				if reason, unreadable := strings.CutPrefix(w, "?"); unreadable {
					if !strings.Contains(got[i].Unreadable, reason) {
						t.Errorf("[%d] Unreadable = %q, want it to name %q", i, got[i].Unreadable, reason)
					}
					continue
				}
				if string(got[i].Analysis.Operation) != w || got[i].Unreadable != "" {
					t.Errorf("[%d] = %+v, want operation %s", i, got[i], w)
				}
			}
		})
	}
}

// A Refuse value, or a value at a Refuse field that is not a string, is
// refused with the operator's message; a document that cannot be read at
// all is refused too, since its value cannot be checked. Without Refuse it
// is Unreadable SQL for the lane's rules.
func TestRefuse(t *testing.T) {
	refuse := jsonsql.RefuseValue{Field: "disposition", Value: "EXTERNAL_LINKS"}
	for _, doc := range []string{
		`{"statement":"SELECT 1","disposition":"external_links"}`,
		`{"statement":"SELECT 1","disposition":2}`,
	} {
		_, err := reader(refuse).Read(doc, "statement")
		if !errors.Is(err, inspect.ErrStreamUnsafe) || !strings.Contains(err.Error(), "ask for disposition INLINE") {
			t.Errorf("%s: err = %v, want the operator's refusal", doc, err)
		}
	}
	if got, err := reader(refuse).Read(`{"statement":"SELECT 1","disposition":null}`, "statement"); err != nil || len(got) != 1 {
		t.Errorf("a null disposition: %+v, %v", got, err)
	}
	// A message read as carrying no SQL is still checked: the refused value
	// is about where the results go, not about the SQL.
	for doc, field := range map[string]string{
		`{"jsonrpc":1,"statement":"SELECT 1","disposition":"EXTERNAL_LINKS"}`:                  "statement",
		`{"jsonrpc":"2.0","method":"initialize","disposition":"EXTERNAL_LINKS"}`:               "params.arguments.query",
		`[{"jsonrpc":"2.0","method":"ping"},{"jsonrpc":"2.0","disposition":"external_links"}]`: "params.arguments.query",
	} {
		if _, err := reader(refuse).Read(doc, field); !errors.Is(err, inspect.ErrStreamUnsafe) {
			t.Errorf("%s: err = %v, want the operator's refusal", doc, err)
		}
	}

	if _, err := reader(refuse).Unparseable("{", "cut short"); !errors.Is(err, inspect.ErrStreamUnsafe) {
		t.Errorf("Unparseable with Refuse: err = %v", err)
	}
	if got, err := reader().Unparseable("{", "cut short"); err != nil || len(got) != 1 || got[0].Unreadable != "cut short" {
		t.Errorf("Unparseable without Refuse: %+v, %v", got, err)
	}
}

func TestToolArgument(t *testing.T) {
	for f, want := range map[string]bool{
		"params.arguments.query": true, "params.arguments.sql.text": true,
		"statement": false, "params.query": false, "params.arguments": false, "arguments.query": false,
	} {
		if got := jsonsql.ToolArgument(f); got != want {
			t.Errorf("ToolArgument(%q) = %v, want %v", f, got, want)
		}
	}
}

func TestValidPath(t *testing.T) {
	for p, want := range map[string]bool{
		"statement": true, "params.arguments.query": true,
		"": false, "params..query": false, ".statement": false, "a. .b": false,
	} {
		if got := jsonsql.ValidPath(p); got != want {
			t.Errorf("ValidPath(%q) = %v, want %v", p, got, want)
		}
	}
}

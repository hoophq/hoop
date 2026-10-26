package analyzer

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// An http lane with an http.sql block emits the request and, after it, the
// SQL read out of its body. The model must read the SQL as SQL, with the
// classifier's reading of it, not as a JSON envelope, and only once: the
// request that carried it declines. A request that carried no SQL keeps the
// HTTP rendering.
func TestHTTPBuilderRendersBodySQLAsSQL(t *testing.T) {
	builder, ok := BuilderFor(inspect.HTTP)
	if !ok {
		t.Fatal("http content builder is not registered")
	}

	sql := inspect.Statement{
		Protocol:  inspect.HTTP,
		Direction: inspect.FromClient,
		Text:      "DELETE FROM customers WHERE id = 7",
		Operation: inspect.OpDelete,
		Tables:    []string{"customers"},
		Metadata:  map[string]string{"http.sql_index": "1", "http.sql_field": "statement"},
	}
	content, ok := builder.Build(sql, 1024)
	if !ok {
		t.Fatal("body SQL produced no model content")
	}
	for _, want := range []string{"Protocol: http", "Operation: delete", "Tables: customers", "DELETE FROM customers"} {
		if !strings.Contains(content.Text, want) {
			t.Errorf("content %q does not contain %q", content.Text, want)
		}
	}
	variant := sql
	variant.Text = "DELETE FROM customers WHERE id = 8"
	if v, _ := builder.Build(variant, 1024); v.CacheKey != content.CacheKey {
		t.Error("literal-only SQL variants do not share a cache key")
	}

	req := inspect.Statement{
		Protocol:  inspect.HTTP,
		Direction: inspect.FromClient,
		Operation: inspect.OpPost,
		HTTP: &inspect.HTTPDetail{
			Method:   "POST",
			Path:     "/api/2.0/sql/statements",
			Resource: "/api/2.0/sql/statements",
			Body:     `{"statement":"DELETE FROM customers WHERE id = 7"}`,
		},
		Metadata: map[string]string{"http.sql_statements": "1"},
	}
	if c, ok := builder.Build(req, 1024); ok {
		t.Errorf("the request that carried SQL was sent to the model too: %q", c.Text)
	}

	other := req
	other.Metadata = nil
	other.HTTP = &inspect.HTTPDetail{Method: "POST", Path: "/api/2.0/jobs/run-now", Resource: "/api/2.0/jobs/run-now"}
	if c, ok := builder.Build(other, 1024); !ok || !strings.HasPrefix(c.Text, "POST /api/2.0/jobs/run-now") {
		t.Errorf("a request without SQL lost its HTTP rendering: %q, %v", c.Text, ok)
	}
}

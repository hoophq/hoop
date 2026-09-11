package analyzer

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// A spanner lane emits two statement shapes on one protocol, and the
// builder must route each to the rendering its rules were written against:
// extracted SQL reads like a wire-database statement, everything else like
// a captured gRPC message. Routing SQL through the gRPC rendering would
// hand the model a protojson wrapper instead of the query; routing a
// non-SQL message through the SQL rendering would classify the method path
// as if it were a statement.
func TestSpannerBuilderRoutesSQLAndGenericShapes(t *testing.T) {
	builder, ok := BuilderFor(inspect.Spanner)
	if !ok {
		t.Fatal("spanner content builder is not registered")
	}

	sql := inspect.Statement{
		Protocol:  inspect.Spanner,
		Direction: inspect.FromClient,
		Text:      "DELETE FROM accounts WHERE id = 7",
		Operation: inspect.OpDelete,
		Tables:    []string{"accounts"},
		HTTP: &inspect.HTTPDetail{
			Resource: "/google.spanner.v1.Spanner/ExecuteSql",
			Body:     `{"sql":"DELETE FROM accounts WHERE id = 7"}`,
		},
		Metadata: map[string]string{"spanner.sql_index": "1"},
	}
	content, ok := builder.Build(sql, 1024)
	if !ok {
		t.Fatal("extracted SQL statement produced no model content")
	}
	for _, want := range []string{
		"Protocol: spanner",
		"Operation: delete",
		"Tables: accounts",
		"DELETE FROM accounts",
	} {
		if !strings.Contains(content.Text, want) {
			t.Errorf("content %q does not contain %q", content.Text, want)
		}
	}
	if strings.Contains(content.Text, "gRPC ") {
		t.Errorf("SQL statement took the gRPC rendering: %q", content.Text)
	}

	// Parameter-only variants must fold onto one cache key, the property
	// that keeps a hot query from billing one model call per literal.
	variant := sql
	variant.Text = "DELETE FROM accounts WHERE id = 8"
	variantContent, _ := builder.Build(variant, 1024)
	if content.CacheKey != variantContent.CacheKey {
		t.Fatal("literal-only SQL variants do not share a cache key")
	}

	generic := inspect.Statement{
		Protocol:  inspect.Spanner,
		Direction: inspect.FromClient,
		Operation: inspect.OpCall,
		HTTP: &inspect.HTTPDetail{
			Resource: "/google.spanner.v1.Spanner/Commit",
			Body:     `{"transaction_id":"dHg="}`,
		},
	}
	genericContent, ok := builder.Build(generic, 1024)
	if !ok {
		t.Fatal("captured non-SQL message produced no model content")
	}
	if !strings.Contains(genericContent.Text, "gRPC /google.spanner.v1.Spanner/Commit") {
		t.Errorf("non-SQL message did not take the gRPC rendering: %q", genericContent.Text)
	}

	// Header/trailer statements carry no body and, like on a grpc lane,
	// are not worth a model call.
	header := inspect.Statement{
		Protocol:  inspect.Spanner,
		Operation: inspect.OpCall,
		HTTP:      &inspect.HTTPDetail{Resource: "/google.spanner.v1.Spanner/ExecuteSql"},
	}
	if _, ok := builder.Build(header, 1024); ok {
		t.Fatal("header-only statement produced model content")
	}
}

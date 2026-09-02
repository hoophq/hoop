package analyzer_test

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
)

func TestMongoDBBuilderIncludesCommandFacts(t *testing.T) {
	builder := analyzer.MongoDBBuilder{}
	content, ok := builder.Build(inspect.Statement{
		Protocol:  inspect.MongoDB,
		Direction: inspect.FromClient,
		Text:      `{"delete":"customers","deletes":[{"q":{"expired":true}}],"$db":"app"}`,
		Operation: inspect.OpDelete,
		Tables:    []string{"app.customers"},
		Database:  "app",
		Metadata:  map[string]string{"mongodb.command": "delete"},
	}, 4096)
	if !ok {
		t.Fatal("MongoDB command produced no analyzer content")
	}
	for _, want := range []string{
		"Protocol: mongodb", "Command: delete", "Operation: delete",
		"Collections: app.customers", "Database: app", `"expired":true`,
	} {
		if !strings.Contains(content.Text, want) {
			t.Errorf("content omitted %q:\n%s", want, content.Text)
		}
	}
	if content.CacheKey == "" {
		t.Error("MongoDB content has no cache key")
	}
}

func TestMongoDBCacheUsesCommandShape(t *testing.T) {
	builder := analyzer.MongoDBBuilder{}
	build := func(text string) string {
		t.Helper()
		content, ok := builder.Build(inspect.Statement{
			Protocol:  inspect.MongoDB,
			Direction: inspect.FromClient,
			Text:      text,
			Operation: inspect.OpSelect,
			Tables:    []string{"app.customers"},
			Database:  "app",
			Metadata:  map[string]string{"mongodb.command": "find"},
		}, 4096)
		if !ok {
			t.Fatal("Build returned false")
		}
		return content.CacheKey
	}

	first := build(`{"find":"customers","filter":{"id":1,"email":"a@example.com"},"$db":"app"}`)
	second := build(`{"find":"customers","filter":{"id":99,"email":"b@example.com"},"$db":"app"}`)
	if first != second {
		t.Errorf("literal-only variants have different keys: %s != %s", first, second)
	}
	third := build(`{"find":"customers","filter":{"account_id":99,"email":"b@example.com"},"$db":"app"}`)
	if first == third {
		t.Error("commands with different filter fields share a cache key")
	}
}

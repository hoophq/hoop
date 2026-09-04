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

// An aggregation pipeline is an ordered plan: reordering stages changes what
// the command exposes. Sharing one cache key across two orders would let the
// first pipeline's model verdict stand in for the second, which is a policy
// decision made about a command nobody classified.
//
// A write batch is the opposite: order and length say nothing a rule reads,
// so folding it is what keeps one verdict serving every batch size.
func TestMongoDBCacheKeepsPipelineOrder(t *testing.T) {
	builder := analyzer.MongoDBBuilder{}
	build := func(command, text string) string {
		t.Helper()
		content, ok := builder.Build(inspect.Statement{
			Protocol:  inspect.MongoDB,
			Direction: inspect.FromClient,
			Text:      text,
			Operation: inspect.OpSelect,
			Tables:    []string{"app.customers"},
			Database:  "app",
			Metadata:  map[string]string{"mongodb.command": command},
		}, 4096)
		if !ok {
			t.Fatal("Build returned false")
		}
		return content.CacheKey
	}

	matchFirst := build("aggregate", `{"aggregate":"customers","pipeline":[{"$match":{"vip":true}},{"$lookup":{"from":"orders"}}],"$db":"app"}`)
	lookupFirst := build("aggregate", `{"aggregate":"customers","pipeline":[{"$lookup":{"from":"orders"}},{"$match":{"vip":true}}],"$db":"app"}`)
	if matchFirst == lookupFirst {
		t.Error("reordered pipeline stages share a cache key")
	}
	repeated := build("aggregate", `{"aggregate":"customers","pipeline":[{"$match":{"vip":true}},{"$match":{"vip":true}}],"$db":"app"}`)
	single := build("aggregate", `{"aggregate":"customers","pipeline":[{"$match":{"vip":true}}],"$db":"app"}`)
	if repeated == single {
		t.Error("a repeated pipeline stage shares the single-stage cache key")
	}

	two := build("insert", `{"insert":"customers","documents":[{"email":"a@example.com"},{"email":"b@example.com"}],"$db":"app"}`)
	three := build("insert", `{"insert":"customers","documents":[{"email":"a@example.com"},{"email":"b@example.com"},{"email":"c@example.com"}],"$db":"app"}`)
	if two != three {
		t.Errorf("insert batches of different sizes have different keys: %s != %s", two, three)
	}
	other := build("insert", `{"insert":"customers","documents":[{"email":"a@example.com"},{"ssn":"1"}],"$db":"app"}`)
	if two == other {
		t.Error("a batch carrying an extra field shares the cache key")
	}
}

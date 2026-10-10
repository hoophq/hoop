package analyzer_test

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
)

func TestGenericBuilderRendersTheVerbAndTables(t *testing.T) {
	b := analyzer.GenericBuilder{Protocol_: "x-acmewire"}
	content, ok := b.Build(inspect.Statement{
		Protocol:  "x-acmewire",
		Operation: inspect.OpDelete,
		Tables:    []string{"orders"},
		Text:      "PURGE orders",
		Metadata:  map[string]string{"x-acmewire.verb": "PURGE"},
	}, 4096)
	if !ok {
		t.Fatal("a statement with text was not classified")
	}
	want := "Protocol: x-acmewire\nOperation: delete\nVerb: PURGE\nTables: orders\n\nPURGE orders"
	if content.Text != want {
		t.Errorf("rendering:\n got %q\nwant %q", content.Text, want)
	}
	if content.CacheKey == "" {
		t.Error("no cache key; every statement would be a fresh model call")
	}
	if _, ok := b.Build(inspect.Statement{Protocol: "x-acmewire", Text: "  "}, 4096); ok {
		t.Error("an empty statement was classified")
	}
}

// Two statements differing only in a literal are one shape, so one verdict
// serves both: the SQL key rule, applied to a plug-in protocol's text.
func TestGenericBuilderKeyStripsLiterals(t *testing.T) {
	b := analyzer.GenericBuilder{Protocol_: "x-acmewire"}
	key := func(text string) string {
		c, _ := b.Build(inspect.Statement{Protocol: "x-acmewire", Operation: inspect.OpSelect, Text: text}, 4096)
		return c.CacheKey
	}
	if key("SELECT * FROM t WHERE id = 1") != key("select * from t where id = 2") {
		t.Error("two statements of one shape got two keys")
	}
	if key("SELECT * FROM t") == key("DELETE FROM t") {
		t.Error("two shapes got one key")
	}
}

type fakeRenderer struct{ calls int }

func (f *fakeRenderer) Content(stmt inspect.Statement, maxBytes int) (string, string, bool) {
	f.calls++
	if stmt.Text == "" {
		return "", "", false
	}
	return "rendered " + stmt.Text, "k:" + stmt.Text, true
}

func TestPluginBuilderDelegatesToTheCodec(t *testing.T) {
	r := &fakeRenderer{}
	b := analyzer.PluginBuilder{Protocol_: "x-acmewire", Renderer: r}
	content, ok := b.Build(inspect.Statement{Text: "AUTH"}, 10)
	if !ok || content.Text != "rendered AUTH" || content.CacheKey != "k:AUTH" {
		t.Errorf("Build = %+v, %v", content, ok)
	}
	if _, ok := b.Build(inspect.Statement{}, 10); ok {
		t.Error("the codec declined and the builder classified anyway")
	}
	if r.calls != 2 {
		t.Errorf("renderer called %d times, want 2", r.calls)
	}
}

// A plug-in loads once per process in production, but -validate and Run in
// one test process, or an embedder loading twice, must not crash on a
// second registration — while a shipped protocol stays unreplaceable.
func TestSetPluginBuilderReplacesAndRefusesShippedNames(t *testing.T) {
	p := inspect.Protocol("x-replace-me")
	if err := analyzer.SetPluginBuilder(p, analyzer.GenericBuilder{Protocol_: p}); err != nil {
		t.Fatal(err)
	}
	r := &fakeRenderer{}
	if err := analyzer.SetPluginBuilder(p, analyzer.PluginBuilder{Protocol_: p, Renderer: r}); err != nil {
		t.Fatalf("second registration refused: %v", err)
	}
	b, ok := analyzer.BuilderFor(p)
	if !ok {
		t.Fatal("no builder after SetPluginBuilder")
	}
	if _, isPlugin := b.(analyzer.PluginBuilder); !isPlugin {
		t.Errorf("the second registration did not replace the first: %T", b)
	}
	if err := analyzer.SetPluginBuilder(inspect.Postgres, analyzer.GenericBuilder{Protocol_: inspect.Postgres}); err == nil {
		t.Error("a shipped protocol was replaced by a plug-in builder")
	}
	if err := analyzer.SetPluginBuilder(p, analyzer.GenericBuilder{Protocol_: "x-other"}); err == nil ||
		!strings.Contains(err.Error(), "answers for") {
		t.Errorf("a builder for another protocol was installed under %s: %v", p, err)
	}
}

package yaml_test

import (
	"testing"

	_ "github.com/hoophq/hoop/sidecar/analyzer/gemini"
	"github.com/hoophq/hoop/sidecar/config/yaml"
)

// YAML keeps an unquoted scalar's type, so `thinking_budget: 0` reaches the
// decoder as a number. Every analyzer extra is a string to its provider, and
// a correct value must not fail the whole file over its spelling.
func TestUnquotedAnalyzerExtrasLoadAsText(t *testing.T) {
	cfg, err := yaml.LoadYAMLBytes([]byte(`
analyzer:
  provider: gemini
  model: gemini-2.5-flash
  extra:
    thinking_budget: -1
    zero: 0
    flag: true
    api: developer
listeners:
  - name: appdb
    protocol: postgres
    listen: 127.0.0.1:15432
    upstream: db:5432
`))
	if err != nil {
		t.Fatalf("unquoted extras were refused: %v", err)
	}
	want := map[string]string{"thinking_budget": "-1", "zero": "0", "flag": "true", "api": "developer"}
	got := cfg.Analyzer.Extra
	for k, v := range want {
		if got[k] != v {
			t.Errorf("extra.%s = %q, want %q", k, got[k], v)
		}
	}
}

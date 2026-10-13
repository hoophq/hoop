package yaml_test

import (
	"strings"
	"testing"

	configyaml "github.com/hoophq/hoop/sidecar/config/yaml"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// Render writes the struct, so what it writes must load back to the same
// listeners and rules, with comments and extension keys around them.
func TestRenderRoundTripsWithCommentsAndExtensions(t *testing.T) {
	cfg := &daemon.Config{
		Listeners: []daemon.ListenerConfig{{
			Name: "api", Protocol: "http", Listen: "127.0.0.1:18080", Upstream: "127.0.0.1:18081",
		}},
		Guardrails: &daemon.GuardrailsConfig{Rules: []policy.Rule{{
			Name: "no-delete", Type: policy.MatchOperation, Operations: []inspect.Operation{inspect.OpDelete},
			Message: `say "no": it's: tricky`,
		}}},
	}
	b, err := configyaml.Render(cfg, configyaml.RenderOptions{
		Header:     []string{"header line", "", "after a gap"},
		Comments:   map[string][]string{"guardrails": {"about guardrails"}},
		Extensions: []configyaml.Extension{{Key: configyaml.DemoAPIKey, Value: "127.0.0.1:18081"}},
		Footer:     []string{"analyzer:", "  provider: anthropic"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"# header line\n#\n# after a gap", "# about guardrails\nguardrails:", "# analyzer:\n#   provider: anthropic"} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered config lacks %q:\n%s", want, s)
		}
	}
	got, err := configyaml.LoadYAMLBytes(b)
	if err != nil {
		t.Fatalf("rendered config does not load: %v\n%s", err, s)
	}
	if len(got.Listeners) != 1 || got.Listeners[0].Upstream != "127.0.0.1:18081" {
		t.Errorf("listeners = %+v", got.Listeners)
	}
	if r := got.Guardrails.Rules; len(r) != 1 || r[0].Message != cfg.Guardrails.Rules[0].Message {
		t.Errorf("rules = %+v", r)
	}
	v, ok, err := configyaml.ExtensionValue(b, configyaml.DemoAPIKey)
	if err != nil || !ok || v != "127.0.0.1:18081" {
		t.Errorf("ExtensionValue = %q %v %v", v, ok, err)
	}
}

func TestRenderRefusesANonExtensionKey(t *testing.T) {
	_, err := configyaml.Render(&daemon.Config{}, configyaml.RenderOptions{
		Extensions: []configyaml.Extension{{Key: "listeners", Value: "x"}},
	})
	if err == nil {
		t.Fatal("Render accepted an extension key that would shadow a config key")
	}
}

func TestStarterListen(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1:5432": "127.0.0.1:15432", "db:3306": "127.0.0.1:13306", "h:60000": "127.0.0.1:15000",
	} {
		if got, err := configyaml.StarterListen(in); err != nil || got != want {
			t.Errorf("StarterListen(%s) = %s, %v; want %s", in, got, err, want)
		}
	}
	if _, err := configyaml.StarterListen("nope"); err == nil {
		t.Error("StarterListen accepted an address with no port")
	}
}

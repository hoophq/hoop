package daemon

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/policy"
)

// The control plane serves its own Go struct, and a build that predates a key
// refuses the whole document over it (DisallowUnknownFields). So no field may
// reach the wire with its zero value: omitempty for scalars, pointers, slices
// and maps, omitzero for structs, which omitempty never drops (ADR-0022).
func TestEveryConfigFieldOmitsItsZeroValue(t *testing.T) {
	var problems []string
	walkConfigType(reflect.TypeFor[Config](), "", func(path string, f jsonField) {
		_, opts, _ := strings.Cut(f.tag.Get("json"), ",")
		has := func(o string) bool { return slices.Contains(strings.Split(opts, ","), o) }
		switch {
		case f.typ.Kind() == reflect.Struct && !has("omitzero"):
			problems = append(problems, path+": a struct field needs omitzero; omitempty never drops a struct")
		case !has("omitempty") && !has("omitzero"):
			problems = append(problems, path+": needs omitempty or omitzero, so an older build never sees the key")
		}
	})
	if len(problems) > 0 {
		t.Fatalf("fields served with their zero value:\n%s", strings.Join(problems, "\n"))
	}
}

// What an admin did not set is not on the wire, so a control plane deploy
// alone never changes what an older sidecar receives.
func TestAnUnsetFieldIsNotServed(t *testing.T) {
	raw, err := json.Marshal(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{}" {
		t.Errorf("an empty config serves keys: %s", raw)
	}

	cfg := Config{Listeners: []ListenerConfig{{
		Name: "appdb", Protocol: "postgres", Listen: ":15432", Upstream: "db:5432",
	}}}
	raw, err = json.Marshal(ServedForm(cfg))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"listeners":[{"name":"appdb","protocol":"postgres","listen":":15432","upstream":"db:5432"}]}`
	if string(raw) != want {
		t.Errorf("a minimal listener serves more than it sets:\n got %s\nwant %s", raw, want)
	}

	// An explicitly empty rule list is a listener's opt-out from inherited
	// rules and must survive; only the nil list is absence.
	cfg.Listeners[0].Guardrails = &GuardrailsConfig{Rules: []policy.Rule{}}
	raw, _ = json.Marshal(cfg)
	if !strings.Contains(string(raw), `"guardrails":{"rules":[]}`) {
		t.Errorf("the empty rule list did not survive: %s", raw)
	}
	cfg.Listeners[0].Guardrails = &GuardrailsConfig{}
	raw, _ = json.Marshal(cfg)
	if strings.Contains(string(raw), `"rules"`) {
		t.Errorf("a nil rule list was served: %s", raw)
	}
}

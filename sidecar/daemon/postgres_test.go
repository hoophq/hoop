package daemon

import (
	"encoding/json"
	"strings"
	"testing"
)

func fieldList(f ...StartupMetadataConfig) *[]StartupMetadataConfig { return &f }

func TestPostgresStartupMetadataConfigValidation(t *testing.T) {
	valid := ListenerConfig{
		Name: "appdb", Protocol: "postgres", Listen: ":15432", Upstream: "db:5432",
		Postgres: &PostgresConfig{StartupMetadata: fieldList(
			StartupMetadataConfig{Option: "claude.session.id", As: "claude.session.id"},
			StartupMetadataConfig{Parameter: "application_name"},
		)},
	}
	if problems := (&Config{}).validateLane(valid, valid.Name, true); len(problems) != 0 {
		t.Fatalf("valid config problems = %v", problems)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*ListenerConfig)
		want   []string
	}{
		{"block on another protocol", func(l *ListenerConfig) {
			l.Protocol = "mysql"
		}, []string{`"postgres" block is only valid on a postgres listener`}},
		// The value is the client's; the key must not be a field the policy
		// context owns, or the client names its own principal.
		{"reserved key", func(l *ListenerConfig) {
			l.Postgres = &PostgresConfig{StartupMetadata: fieldList(
				StartupMetadataConfig{Option: "claude.user", As: "principal"},
			)}
		}, []string{"startup_metadata[0]", "reserved"}},
		{"duplicate key, both positions named", func(l *ListenerConfig) {
			l.Postgres = &PostgresConfig{StartupMetadata: fieldList(
				StartupMetadataConfig{Option: "a.b", As: "trace"},
				StartupMetadataConfig{Parameter: "application_name", As: "trace"},
			)}
		}, []string{"startup_metadata[1]", "[0] already writes"}},
		// Without `as`, both default to postgres.option.a_b.
		{"duplicate default key", func(l *ListenerConfig) {
			l.Postgres = &PostgresConfig{StartupMetadata: fieldList(
				StartupMetadataConfig{Option: "a-b"},
				StartupMetadataConfig{Option: "A_B"},
			)}
		}, []string{"postgres.option.a_b"}},
		{"neither source", func(l *ListenerConfig) {
			l.Postgres = &PostgresConfig{StartupMetadata: fieldList(StartupMetadataConfig{As: "x"})}
		}, []string{"neither"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lc := valid
			tc.mutate(&lc)
			problems := strings.Join((&Config{}).validateLane(lc, lc.Name, true), "\n")
			for _, w := range tc.want {
				if !strings.Contains(problems, w) {
					t.Errorf("problems = %q, want one mentioning %q", problems, w)
				}
			}
		})
	}
}

// Absent records every option, a list narrows it, [] records none. The three
// are opposite enough that each spelling is pinned, and the pinning has to
// hold after the reloader and the control plane re-marshal the block: a []
// that came back as the default would start recording every option on a lane
// whose operator turned it off.
func TestStartupMetadataTriStateSurvivesARoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name   string
		block  string
		all    bool
		fields int
	}{
		{"no block", ``, true, 0},
		{"block without the key", `"postgres": {},`, true, 0},
		{"empty list", `"postgres": {"startup_metadata": []},`, false, 0},
		{"a list", `"postgres": {"startup_metadata": [{"option": "claude.session.id"}]},`, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := `{"listeners": [{"name": "appdb", "protocol": "postgres", ` + tc.block +
				` "listen": ":15432", "upstream": "db:5432"}]}`
			cfg, err := LoadConfigBytes([]byte(doc))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			raw, err := json.Marshal(cfg)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			again, err := LoadConfigBytes(raw)
			if err != nil {
				t.Fatalf("reload of the re-marshaled config: %v", err)
			}
			for _, c := range []*Config{cfg, again} {
				p := c.Listeners[0].Postgres
				if got := p.recordsAllOptions(); got != tc.all {
					t.Errorf("recordsAllOptions = %v, want %v", got, tc.all)
				}
				if got := len(p.startupMetadata()); got != tc.fields {
					t.Errorf("fields = %d, want %d", got, tc.fields)
				}
			}
		})
	}
}

// `startup_metadata:` with no value is null, which reads as absent (every
// option) though it looks like "none". Refused, so the operator says which.
func TestStartupMetadataWithNoValueIsRefused(t *testing.T) {
	_, err := LoadConfigBytes([]byte(`{"listeners": [{"name": "appdb", "protocol": "postgres",
      "listen": ":15432", "upstream": "db:5432", "postgres": {"startup_metadata": null}}]}`))
	if err == nil || !strings.Contains(err.Error(), "written with no value") {
		t.Fatalf("load = %v, want the no-value refusal", err)
	}
	_, err = LoadConfigBytes([]byte(`{"listeners": [{"name": "appdb", "protocol": "postgres",
      "listen": ":15432", "upstream": "db:5432", "postgres": {"startup_metdata": []}}]}`))
	if err == nil || !strings.Contains(err.Error(), "startup_metdata") {
		t.Fatalf("load = %v, want the misspelled key refused", err)
	}
}

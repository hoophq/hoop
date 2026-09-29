package daemon

import (
	"strings"
	"testing"
)

func TestPostgresStartupMetadataConfigValidation(t *testing.T) {
	valid := ListenerConfig{
		Name: "appdb", Protocol: "postgres", Listen: ":15432", Upstream: "db:5432",
		Postgres: &PostgresConfig{StartupMetadata: []StartupMetadataConfig{
			{Option: "claude.session.id", As: "claude.session.id"},
			{Parameter: "application_name"},
		}},
	}
	if problems := (&Config{}).validateLane(valid, valid.Name); len(problems) != 0 {
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
			l.Postgres = &PostgresConfig{StartupMetadata: []StartupMetadataConfig{
				{Option: "claude.user", As: "principal"},
			}}
		}, []string{"startup_metadata[0]", "reserved"}},
		{"duplicate key, both positions named", func(l *ListenerConfig) {
			l.Postgres = &PostgresConfig{StartupMetadata: []StartupMetadataConfig{
				{Option: "a.b", As: "trace"},
				{Parameter: "application_name", As: "trace"},
			}}
		}, []string{"startup_metadata[1]", "[0] already writes"}},
		{"neither source", func(l *ListenerConfig) {
			l.Postgres = &PostgresConfig{StartupMetadata: []StartupMetadataConfig{{As: "x"}}}
		}, []string{"neither"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lc := valid
			tc.mutate(&lc)
			problems := strings.Join((&Config{}).validateLane(lc, lc.Name), "\n")
			for _, w := range tc.want {
				if !strings.Contains(problems, w) {
					t.Errorf("problems = %q, want one mentioning %q", problems, w)
				}
			}
		})
	}
}

// Without `as`, a field lands in a namespace no codec and no policy-context
// field writes, so an operator who only names the source cannot collide.
func TestStartupMetadataDefaultKeysAreNamespaced(t *testing.T) {
	got := (&PostgresConfig{StartupMetadata: []StartupMetadataConfig{
		{Option: "claude.session.id"},
		{Parameter: "application_name"},
	}}).startupMetadata()
	if got[0].Key != "postgres.option.claude.session.id" ||
		got[1].Key != "postgres.parameter.application_name" {
		t.Fatalf("default keys = %q, %q", got[0].Key, got[1].Key)
	}
}

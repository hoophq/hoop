package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hoophq/hoop/sidecar/proxy"
)

// PostgresConfig configures what a postgres lane reads from the client's
// StartupMessage beyond the user. Only valid on a postgres lane.
type PostgresConfig struct {
	// StartupMetadata selects what the lane lifts from the StartupMessage onto
	// the session's metadata, which every statement record, session_end and
	// the OPA input carry. The values are the client's claim; see
	// proxy.StartupMetadata.
	//
	// Three configurations, not two, the way `mask: rules: []` is:
	//
	//   - absent: every setting the `options` parameter assigns, each as
	//     postgres.option.<name>. A client that sends
	//     PGOPTIONS='-c claude.session.id=...' is traced with no config.
	//   - a list: only those fields, under their `as` keys.
	//   - []: nothing.
	//
	// A pointer, so the three survive a round trip: the reloader and the
	// control plane re-marshal this struct, and a plain slice would come back
	// as null (refused below) or, under omitempty, turn [] into the default.
	StartupMetadata *[]StartupMetadataConfig `json:"startup_metadata,omitempty"`
}

// UnmarshalJSON decodes the postgres block, refusing the one spelling the
// tri-state cannot answer.
//
// `startup_metadata:` written with NO VALUE transcodes to null, and null on a
// slice is indistinguishable from an absent key once the field is set. Absent
// records every option and [] records none: opposite readings, so an operator
// who wrote neither has to say which they meant.
//
// The decode also re-imposes DisallowUnknownFields, which the outer decoder
// applies at the top level but does not propagate into a type that
// unmarshals itself.
func (p *PostgresConfig) UnmarshalJSON(b []byte) error {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(b, &probe); err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	if raw, ok := probe["startup_metadata"]; ok && string(bytes.TrimSpace(raw)) == "null" {
		return errors.New(
			"postgres.startup_metadata is written with no value; omit the key to record every " +
				"option the client sends, or write [] to record none")
	}
	type plain PostgresConfig
	var out plain
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	*p = PostgresConfig(out)
	return nil
}

// StartupMetadataConfig is one field of postgres.startup_metadata.
type StartupMetadataConfig struct {
	// Option names a setting in the `options` startup parameter, sent as
	// PGOPTIONS='-c claude.session.id=...' or `--claude.session.id=...`.
	Option string `json:"option,omitempty"`

	// Parameter names a StartupMessage parameter, such as application_name.
	Parameter string `json:"parameter,omitempty"`

	// As is the metadata key the value is recorded under. Absent records it
	// as postgres.option.<option> or postgres.parameter.<parameter>, the
	// same key the default records an option under.
	As string `json:"as,omitempty"`
}

// recordsAllOptions reports whether the lane takes the default: no list, so
// every option the client sends.
func (p *PostgresConfig) recordsAllOptions() bool {
	return p == nil || p.StartupMetadata == nil
}

// fields is the configured list: nil for the default, empty for [].
func (p *PostgresConfig) fields() []StartupMetadataConfig {
	if p == nil || p.StartupMetadata == nil {
		return nil
	}
	return *p.StartupMetadata
}

// startupMetadata resolves a configured list to the fields proxy.Config takes.
// Nil for the default and for [], which recordsAllOptions tells apart.
func (p *PostgresConfig) startupMetadata() []proxy.StartupMetadata {
	list := p.fields()
	if len(list) == 0 {
		return nil
	}
	out := make([]proxy.StartupMetadata, 0, len(list))
	for _, f := range list {
		out = append(out, proxy.StartupMetadata{
			Option:    f.Option,
			Parameter: f.Parameter,
			Key:       f.As,
		})
	}
	return out
}

// validate reports every unusable field, each by its position, so one run
// names them all. The rules are proxy.StartupMetadata.Validate's: one
// definition of what a lane refuses, reported here at load rather than at the
// first listen.
func (p *PostgresConfig) validate(lane string) []string {
	if p == nil {
		return nil
	}
	var problems []string
	seen := make(map[string]int, len(p.fields()))
	for i, f := range p.startupMetadata() {
		if err := f.Validate(); err != nil {
			problems = append(problems, fmt.Sprintf(
				"%s: postgres.startup_metadata[%d]: %v", lane, i, err))
			continue
		}
		key := f.MetadataKey()
		if first, dup := seen[key]; dup {
			problems = append(problems, fmt.Sprintf(
				"%s: postgres.startup_metadata[%d] writes key %q, which [%d] already writes",
				lane, i, key, first))
			continue
		}
		seen[key] = i
	}
	return problems
}

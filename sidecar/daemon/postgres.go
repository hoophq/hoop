package daemon

import (
	"fmt"

	"github.com/hoophq/hoop/sidecar/proxy"
)

// PostgresConfig configures what a postgres lane reads from the client's
// StartupMessage beyond the user. Only valid on a postgres lane.
type PostgresConfig struct {
	// StartupMetadata lifts values the client sent at connect time onto the
	// session's metadata, so every statement record and the OPA input carry
	// them. The values are the client's claim; see proxy.StartupMetadata.
	StartupMetadata []StartupMetadataConfig `json:"startup_metadata,omitempty"`
}

// StartupMetadataConfig is one field of postgres.startup_metadata.
type StartupMetadataConfig struct {
	// Option names a setting in the `options` startup parameter, sent as
	// PGOPTIONS='-c claude.session.id=...' or `--claude.session.id=...`.
	Option string `json:"option,omitempty"`

	// Parameter names a StartupMessage parameter, such as application_name.
	Parameter string `json:"parameter,omitempty"`

	// As is the metadata key the value is recorded under. Absent records it
	// as postgres.option.<option> or postgres.parameter.<parameter>, a
	// namespace no codec and no policy-context field writes.
	As string `json:"as,omitempty"`
}

// key is the metadata key this field writes.
func (f StartupMetadataConfig) key() string {
	switch {
	case f.As != "":
		return f.As
	case f.Option != "":
		return "postgres.option." + f.Option
	case f.Parameter != "":
		return "postgres.parameter." + f.Parameter
	}
	return ""
}

// startupMetadata resolves the block to the fields proxy.Config takes. Nil
// for an absent block, so a lane without one runs exactly as before.
func (p *PostgresConfig) startupMetadata() []proxy.StartupMetadata {
	if p == nil || len(p.StartupMetadata) == 0 {
		return nil
	}
	out := make([]proxy.StartupMetadata, 0, len(p.StartupMetadata))
	for _, f := range p.StartupMetadata {
		out = append(out, proxy.StartupMetadata{
			Option:    f.Option,
			Parameter: f.Parameter,
			Key:       f.key(),
		})
	}
	return out
}

// validate reports every unusable field, each by its position, so one run
// names them all. The rules are proxy.StartupMetadata.Validate's, applied to
// the resolved key: one definition of what a lane refuses, reported here at
// load rather than at the first listen.
func (p *PostgresConfig) validate(lane string) []string {
	if p == nil {
		return nil
	}
	var problems []string
	seen := make(map[string]int, len(p.StartupMetadata))
	for i, f := range p.startupMetadata() {
		if err := f.Validate(); err != nil {
			problems = append(problems, fmt.Sprintf(
				"%s: postgres.startup_metadata[%d]: %v", lane, i, err))
			continue
		}
		if first, dup := seen[f.Key]; dup {
			problems = append(problems, fmt.Sprintf(
				"%s: postgres.startup_metadata[%d] writes key %q, which [%d] already writes",
				lane, i, f.Key, first))
			continue
		}
		seen[f.Key] = i
	}
	return problems
}

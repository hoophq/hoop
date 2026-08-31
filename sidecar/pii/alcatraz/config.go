package alcatraz

import (
	"encoding/json"
	"fmt"

	"github.com/hoophq/hoop/sidecar/gate"
)

// Plugin is the detection interface the sidecar consumes, declared here so a
// caller can wire this module in without either side importing the other.
//
// sidecar.Plugin is the same method set. Go converts between the two on
// assignment, and a nil Plugin converts to a nil sidecar.Plugin, which is the
// property that matters: a *Detector-typed nil inside a non-nil interface
// would make the sidecar believe detection is on and call through it.
type Plugin interface {
	ScanText(text string) []string
	Entities() []string
	BuildMasker(rawRules []byte) (gate.Masker, error)
}

// piiSection is the "pii" key of the sidecar config.
//
// It is decoded here rather than in the sidecar package so the sidecar never
// has to know what an Options looks like: knowing would drag this module's
// dependency back into the one place the split exists to keep clean.
type piiSection struct {
	// Entities is optional, and an empty one selects every supported type.
	// The zero piiSection therefore describes the permissive detector,
	// which is what an absent section builds.
	Entities  []string `json:"entities,omitempty"`
	Ignored   []string `json:"ignored,omitempty"`
	Threshold float64  `json:"threshold,omitempty"`
	AllowList []string `json:"allow_list,omitempty"`
	Language  string   `json:"language,omitempty"`
}

// PluginFromConfig builds the detector described by a sidecar config's "pii"
// section. Pass Config.PII straight in.
//
// An absent section is not "detection off". It builds the permissive
// detector, with every supported entity type active, because the section's
// job is to subtract: a deployment whose ordinary data trips a recognizer
// names it in "ignored", and a deployment that knows its schema names the
// types it holds in "entities". Neither is a precondition for detecting
// anything, and requiring the section made masking and the analyzer's
// redaction refuse to start on a config that had simply not heard of it.
//
// A present but invalid section IS an error, so an operator who wrote entity
// names hears that they do not resolve rather than watching masking quietly
// do nothing.
//
// A nil Plugin remains possible and now means one thing only: this build
// linked no detector at all. The sidecar reports that case separately
// (checkPIIPlugin), and it can no longer be reached through configuration.
func PluginFromConfig(raw json.RawMessage) (Plugin, error) {
	var s piiSection
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("pii section: %w", err)
		}
	}
	det, err := NewDetector(Options{
		Entities:  s.Entities,
		Ignored:   s.Ignored,
		Threshold: s.Threshold,
		AllowList: s.AllowList,
		Language:  s.Language,
	})
	if err != nil {
		return nil, fmt.Errorf("pii section: %w", err)
	}
	return det, nil
}

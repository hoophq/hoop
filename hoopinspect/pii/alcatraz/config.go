package alcatraz

import (
	"encoding/json"
	"fmt"

	"github.com/hoophq/hoopinspect/gate"
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
	Entities  []string `json:"entities"`
	Ignored   []string `json:"ignored,omitempty"`
	Threshold float64  `json:"threshold,omitempty"`
	AllowList []string `json:"allow_list,omitempty"`
	Language  string   `json:"language,omitempty"`
}

// PluginFromConfig builds the detector described by a sidecar config's "pii"
// section. Pass Config.PII straight in.
//
// An absent section returns a nil Plugin and no error: the relay then runs
// with detection disabled, which is the right answer for a config written
// before the section existed. A present but invalid section IS an error, so
// an operator who wrote entity names hears that they do not resolve rather
// than watching masking quietly do nothing.
func PluginFromConfig(raw json.RawMessage) (Plugin, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var s piiSection
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("pii section: %w", err)
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

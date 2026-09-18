package services

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/policy"
	"gorm.io/gorm"
)

// ComposeSidecarConfiguration returns the document to serve: the sidecar's
// stored configuration with every bound rule placed on the listener it names.
//
// Nothing is persisted. The stored document stays what an admin authored, and
// the served one is derived on every handshake, so editing a rule bound to
// three hundred sidecars is one row update with no fan-out and no half-written
// fleet.
//
// Composition PLACES a stored block; it does not convert one. A rule for a
// sidecar is authored and stored in the sidecar's own vocabulary
// (sidecar_spec, migration 000120), so there is no translation step to get
// wrong and no rule that saves cleanly and arrives meaning something else.
//
// It only ever touches a listener's guardrails, mask and analyzer blocks.
// Those are exactly the sections sidecar/daemon/reload.go strips into its
// per-lane rule document, so a rule edit hot-swaps into a running sidecar
// instead of asking it to restart (ADR-0014).
func ComposeSidecarConfiguration(db *gorm.DB, sc *models.Sidecar) (daemon.Config, error) {
	cfg := daemon.Config(sc.Configuration)

	orgID, err := uuid.Parse(sc.OrgID)
	if err != nil {
		return cfg, fmt.Errorf("failed parsing the sidecar organization id: %w", err)
	}

	guardrails, err := models.ListGuardrailRulesForSidecar(db, orgID, sc.ID)
	if err != nil {
		return cfg, fmt.Errorf("failed loading the guardrail rules bound to this sidecar: %w", err)
	}
	masking, err := models.ListDataMaskingRulesForSidecar(db, orgID, sc.ID)
	if err != nil {
		return cfg, fmt.Errorf("failed loading the data masking rules bound to this sidecar: %w", err)
	}
	analyzers, err := models.ListAnalyzerRulesForSidecar(db, orgID, sc.ID)
	if err != nil {
		return cfg, fmt.Errorf("failed loading the analyzer rules bound to this sidecar: %w", err)
	}
	return foldSidecarRules(cfg, guardrails, masking, analyzers)
}

// foldSidecarRules is the composition itself, separated from the loading so
// the invariants the fleet depends on can be tested over values instead of a
// database: that the result still decodes strictly (no key an older sidecar
// would refuse), and that nothing a rule contributes lands outside the
// hot-reloadable sections.
func foldSidecarRules(cfg daemon.Config, guardrails, masking, analyzers []models.BoundRule) (daemon.Config, error) {
	if len(guardrails) == 0 && len(masking) == 0 && len(analyzers) == 0 {
		return cfg, nil
	}

	// Copy the listener slice before writing into it. cfg shares its backing
	// array with the row the middleware loaded, and composing in place would
	// leave the next request reading a document with rules already folded in
	// -- which would then be folded again.
	listeners := make([]daemon.ListenerConfig, len(cfg.Listeners))
	copy(listeners, cfg.Listeners)
	cfg.Listeners = listeners

	// listenerIndex resolves a binding to its lane, or reports that the lane is
	// gone. Skipping a missing one would serve a document that silently
	// enforces less than the admin sees bound, so every caller turns it into a
	// refusal the handshake answers with -- and the sidecar keeps the rules it
	// already has rather than losing them.
	listenerIndex := func(kind, ruleName, listenerName string) (int, error) {
		if listenerName == "" {
			return -1, fmt.Errorf("%s rule %q is bound to this sidecar without naming a listener, "+
				"which this version does not distribute; rebind it to the listeners that must "+
				"enforce it", kind, ruleName)
		}
		for i, l := range listeners {
			if l.Name == listenerName {
				return i, nil
			}
		}
		return -1, fmt.Errorf("%s rule %q is bound to listener %q, which this sidecar's "+
			"configuration no longer has; rebind or restore the listener", kind, ruleName, listenerName)
	}

	// Guardrails CONCATENATE. The daemon evaluates first-match-wins among
	// rules that deny, so adding is monotonic in the allow/deny outcome.
	// Appending also preserves `rules: []`, which is how a lane opts out of
	// the defaults entirely.
	for _, b := range guardrails {
		var block daemon.GuardrailsConfig
		if err := decodeSpec(b.Spec, &block); err != nil {
			return cfg, fmt.Errorf("guardrail rule %q: %w", b.RuleName, err)
		}
		if len(block.Rules) == 0 {
			continue
		}
		idx, err := listenerIndex("guardrail", b.RuleName, b.ListenerName)
		if err != nil {
			return cfg, err
		}
		listeners[idx].Guardrails = appendGuardrails(listeners[idx].Guardrails, block.Rules)
	}

	// Masking REPLACES, so every rule bound to one lane is collected and that
	// lane is written once. The asymmetry with guardrails is the daemon's, not
	// a choice here: a mask rule owns an entity type, and two rules rewriting
	// one value is not a policy anyone wrote.
	maskByLane := map[string][]models.BoundRule{}
	lanes := []string{}
	for _, b := range masking {
		if _, seen := maskByLane[b.ListenerName]; !seen {
			lanes = append(lanes, b.ListenerName)
		}
		maskByLane[b.ListenerName] = append(maskByLane[b.ListenerName], b)
	}
	for _, lane := range lanes {
		rules := []json.RawMessage{}
		for _, b := range maskByLane[lane] {
			// The rules stay RAW here. Their shape belongs to the detector
			// plugin the sidecar links, which is why daemon.MaskConfig keeps
			// them as bytes too; what this layer owns is which lane they land
			// on. The write gate is where they are checked.
			var block struct {
				Rules []json.RawMessage `json:"rules"`
			}
			if err := decodeSpec(b.Spec, &block); err != nil {
				return cfg, fmt.Errorf("data masking rule %q: %w", b.RuleName, err)
			}
			rules = append(rules, block.Rules...)
		}
		if len(rules) == 0 {
			continue
		}
		idx, err := listenerIndex("data masking", maskByLane[lane][0].RuleName, lane)
		if err != nil {
			return cfg, err
		}
		raw, err := json.Marshal(rules)
		if err != nil {
			return cfg, fmt.Errorf("failed rendering the mask rules for listener %q: %w", lane, err)
		}
		listeners[idx].Mask = &daemon.MaskConfig{Rules: raw}
	}

	// The analyzer is ONE BLOCK per lane, not a list: a lane wanting several
	// analyzers has no block equivalent in the daemon. Two rules bound to one
	// listener is therefore a conflict rather than a merge, and it is refused
	// instead of resolved by whichever row came back last.
	claimed := map[string]string{}
	for _, b := range analyzers {
		if owner, taken := claimed[b.ListenerName]; taken {
			return cfg, fmt.Errorf("analyzer rules %q and %q are both bound to listener %q; a "+
				"listener runs one analyzer block, so bind one rule to it", owner, b.RuleName,
				b.ListenerName)
		}
		claimed[b.ListenerName] = b.RuleName

		idx, err := listenerIndex("analyzer", b.RuleName, b.ListenerName)
		if err != nil {
			return cfg, err
		}
		var block daemon.LaneAnalyzerConfig
		if err := decodeSpec(b.Spec, &block); err != nil {
			return cfg, fmt.Errorf("analyzer rule %q: %w", b.RuleName, err)
		}
		listeners[idx].Analyzer = &block
	}
	return cfg, nil
}

// decodeSpec reads a stored sidecar_spec into the daemon's own type, strictly.
//
// Strictly, because the column is JSONB and nothing in the database constrains
// its shape: a key the daemon does not declare would reach a sidecar that
// refuses its WHOLE document over it, and the sidecar cannot recover on its
// own. Failing here answers the handshake with an error instead, which a
// sidecar survives by keeping the rules it already has.
func decodeSpec(raw json.RawMessage, into any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("its stored sidecar rule is not a shape a sidecar accepts: %w", err)
	}
	return nil
}

// appendGuardrails adds rules to a block, creating it when absent.
func appendGuardrails(gc *daemon.GuardrailsConfig, rules []policy.Rule) *daemon.GuardrailsConfig {
	if gc == nil {
		return &daemon.GuardrailsConfig{Rules: rules}
	}
	out := *gc
	out.Rules = append(append([]policy.Rule{}, gc.Rules...), rules...)
	return &out
}

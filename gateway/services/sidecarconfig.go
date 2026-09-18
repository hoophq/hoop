package services

import (
	"bytes"
	"encoding/json"
	"errors"
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
// (sidecar_spec, migration 000121), so there is no translation step to get
// wrong and no rule that saves cleanly and arrives meaning something else.
//
// It only ever touches a listener's guardrails, mask and analyzer blocks.
// Those are exactly the sections sidecar/daemon/reload.go strips into its
// per-lane rule document, so a rule edit hot-swaps into a running sidecar
// instead of asking it to restart (ADR-0014).
func ComposeSidecarConfiguration(db *gorm.DB, sc *models.Sidecar) (daemon.Config, error) {
	return composeSidecarConfiguration(db, sc, "", "")
}

// ErrSidecarRulesUnavailable marks a composition that could not be ATTEMPTED,
// as opposed to one the rules make impossible.
//
// The two are different answers to an admin and different HTTP statuses. A
// conflicting binding is theirs to fix and reads 422; a junction table that
// would not read is ours and reads 500. Both still refuse, because a write
// gate that passes when it could not run is the failure this whole file
// exists to prevent.
var ErrSidecarRulesUnavailable = errors.New("the rules bound to this sidecar could not be read")

// composeSidecarConfiguration is ComposeSidecarConfiguration with one rule left
// out of the fold.
//
// The exclusion serves the WRITE path. A rule being edited is already in the
// database under its stored name, so composing it and then folding the incoming
// version in on top counts one rule twice -- and the free tier then refuses an
// edit that changes nothing about the count. skipName is the name the bindings
// currently sit under, which a rename makes differ from the new one; empty
// excludes nothing, which is what a read wants.
// candidates are bindings that are not in the database yet -- the write being
// checked. They are folded in with the stored ones rather than on top of an
// already-composed document, because the three features do not fold the same
// way: a second mask rule REPLACES a lane's block and a second analyzer rule
// on one lane is a conflict, so a candidate laid over a composed result would
// under-count the first and never see the second.
func composeSidecarConfiguration(db *gorm.DB, sc *models.Sidecar, skipKind SidecarRuleKind, skipName string, candidates ...models.BoundRule) (daemon.Config, error) {
	cfg := daemon.Config(sc.Configuration)

	orgID, err := uuid.Parse(sc.OrgID)
	if err != nil {
		return cfg, fmt.Errorf("failed parsing the sidecar organization id: %w", err)
	}

	guardrails, err := models.ListGuardrailRulesForSidecar(db, orgID, sc.ID)
	if err != nil {
		return cfg, fmt.Errorf("%w: failed loading the guardrail rules bound to this sidecar: %v", ErrSidecarRulesUnavailable, err)
	}
	masking, err := models.ListDataMaskingRulesForSidecar(db, orgID, sc.ID)
	if err != nil {
		return cfg, fmt.Errorf("%w: failed loading the data masking rules bound to this sidecar: %v", ErrSidecarRulesUnavailable, err)
	}
	analyzers, err := models.ListAnalyzerRulesForSidecar(db, orgID, sc.ID)
	if err != nil {
		return cfg, fmt.Errorf("%w: failed loading the analyzer rules bound to this sidecar: %v", ErrSidecarRulesUnavailable, err)
	}
	if skipName != "" {
		switch skipKind {
		case SidecarRuleGuardrail:
			guardrails = withoutRule(guardrails, skipName)
		case SidecarRuleMask:
			masking = withoutRule(masking, skipName)
		case SidecarRuleAnalyzer:
			analyzers = withoutRule(analyzers, skipName)
		}
	}
	switch {
	case len(candidates) == 0:
	case skipKind == SidecarRuleGuardrail:
		guardrails = append(guardrails, candidates...)
	case skipKind == SidecarRuleMask:
		masking = append(masking, candidates...)
	case skipKind == SidecarRuleAnalyzer:
		analyzers = append(analyzers, candidates...)
	default:
		return cfg, fmt.Errorf("cannot compose a candidate %s rule", skipKind)
	}
	return foldSidecarRules(cfg, guardrails, masking, analyzers)
}

// withoutRule drops every binding of one rule, by the name the bindings are
// stored under.
func withoutRule(bound []models.BoundRule, name string) []models.BoundRule {
	kept := make([]models.BoundRule, 0, len(bound))
	for _, b := range bound {
		if b.RuleName == name {
			continue
		}
		kept = append(kept, b)
	}
	return kept
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
		listeners[idx].Analyzer = mergeAnalyzerBlock(listeners[idx].Analyzer, block)
	}
	return cfg, nil
}

// mergeAnalyzerBlock lays a rule over the listener's own analyzer block.
//
// The rule owns the risk DECISION -- what is classified and what each verdict
// does. The listener keeps the controls a control plane has no business
// setting from a rule form: what leaves the process, what happens when the
// provider is down, and what one classification may cost.
//
// Replacing the whole block instead would silently revert two safety
// properties an operator wrote down. `send: refuse` becomes the inherited
// default, so statement text a relay exists to keep away from a model vendor
// is posted to one; `fail_open: false` becomes true, so a provider outage
// starts allowing the statements the block was there to deny. Neither failure
// is visible anywhere: the document still validates and the lane still
// reports an analyzer.
//
// A nil base cannot happen through the API -- ValidateSidecarRuleTargets
// refuses a rule bound to a lane with the analyzer off -- and is handled
// rather than dereferenced, because composition also runs over rows written
// before that guard existed.
func mergeAnalyzerBlock(base *daemon.LaneAnalyzerConfig, rule daemon.LaneAnalyzerConfig) *daemon.LaneAnalyzerConfig {
	if base == nil {
		return &rule
	}
	out := *base

	// What the rule form writes, and every one of these REPLACES: a rule
	// naming no trigger classifies everything on purpose, and a level it
	// leaves unset allows on purpose. Falling back to the listener here
	// would make a rule unable to widen what it narrowed.
	out.Trigger = rule.Trigger
	out.HighRisk, out.MediumRisk, out.LowRisk = rule.HighRisk, rule.MediumRisk, rule.LowRisk
	out.Prompt, out.Message = rule.Prompt, rule.Message

	// The overrides, kept from the listener unless the rule names one. The
	// form writes only max_calls today; the rest are here because the spec is
	// stored as the daemon's own type, so a spec that names one means it.
	if rule.MaxCalls != 0 {
		out.MaxCalls = rule.MaxCalls
	}
	if rule.TimeoutSec != 0 {
		out.TimeoutSec = rule.TimeoutSec
	}
	if rule.MaxInputBytes != 0 {
		out.MaxInputBytes = rule.MaxInputBytes
	}
	if rule.Send != "" {
		out.Send = rule.Send
	}
	if rule.FailOpen != nil {
		out.FailOpen = rule.FailOpen
	}
	if rule.Cache != nil {
		out.Cache = rule.Cache
	}
	if rule.ApprovalRule != "" {
		out.ApprovalRule = rule.ApprovalRule
	}
	return &out
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

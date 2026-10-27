package services

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/pii/alcatraz"
	"github.com/hoophq/hoop/sidecar/policy"
)

// A rule the agent runs (input/output rules, supported entity types; no
// sidecar_spec) binds to a sidecar listener the way a native sidecar rule
// does: sidecar_targets. Composition translates it into the sidecar's own
// vocabulary and places it on the listener AFTER the listener's native rules.
// Nothing is stored; the translation is derived on every handshake.
//
// A rule that cannot be translated is refused whole, never applied in part:
// a rule that saved and enforces less than the admin sees bound is the
// failure this file exists to prevent. The rule APIs refuse at the save
// (422); what slips through (a sidecar's pii section edited under a bound
// rule) is refused at the handshake, where the sidecar keeps its last good
// rules and the page shows the reason.
//
// Known difference: a sidecar matches each statement, the agent the whole input.

// ErrSidecarGatewayRuleRefused is a gateway rule bound to a sidecar that
// cannot run there. The admin's to fix, so it reads 422 and the handshake
// records it as not_served.
type ErrSidecarGatewayRuleRefused struct{ Reason string }

func (e ErrSidecarGatewayRuleRefused) Error() string { return e.Reason }

// gatewayGuardrailRule is one entry of a guardrail rule's input or output, as
// the agent reads it (gateway/guardrails).
type gatewayGuardrailRule struct {
	Type         string   `json:"type"`
	Words        []string `json:"words"`
	PatternRegex string   `json:"pattern_regex"`
	Message      string   `json:"message"`
}

func decodeGatewayGuardrails(raw json.RawMessage) ([]gatewayGuardrailRule, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var block struct {
		Rules []gatewayGuardrailRule `json:"rules"`
	}
	if err := json.Unmarshal(raw, &block); err != nil {
		return nil, err
	}
	return block.Rules, nil
}

// ruleWhere names a rule and, when bound, its listener, for a refusal.
func ruleWhere(kind SidecarRuleKind, b models.BoundRule) string {
	if b.ListenerName == "" {
		return fmt.Sprintf("%s rule %q", kind, b.RuleName)
	}
	return fmt.Sprintf("%s rule %q, bound to listener %q,", kind, b.RuleName, b.ListenerName)
}

// translateGuardrail turns a gateway guardrail rule into sidecar rules.
//
// deny_words_list becomes a pattern_match on the quoted words: the sidecar's
// own deny_words_list is case-insensitive and would deny more than the agent
// does. An output rule is refused: a sidecar checks statements, not results.
func translateGuardrail(r models.BoundRule) ([]policy.Rule, error) {
	where := ruleWhere(SidecarRuleGuardrail, r)
	output, err := decodeGatewayGuardrails(r.Output)
	if err != nil {
		return nil, fmt.Errorf("%s has output rules that are not readable: %v", where, err)
	}
	if len(output) > 0 {
		return nil, fmt.Errorf("%s has output rules, which a sidecar cannot run: it checks statements, "+
			"not results; keep only input rules on a rule bound to a sidecar", where)
	}
	input, err := decodeGatewayGuardrails(r.Input)
	if err != nil {
		return nil, fmt.Errorf("%s has input rules that are not readable: %v", where, err)
	}
	var out []policy.Rule
	for i, entry := range input {
		name := r.RuleName
		if len(input) > 1 {
			name = fmt.Sprintf("%s/%d", r.RuleName, i+1)
		}
		switch entry.Type {
		case "deny_words_list":
			var quoted []string
			for _, w := range entry.Words {
				// The agent skips an empty word.
				if w != "" {
					quoted = append(quoted, regexp.QuoteMeta(w))
				}
			}
			if len(quoted) == 0 {
				continue
			}
			out = append(out, policy.Rule{Name: name, Type: policy.MatchPattern,
				Pattern: strings.Join(quoted, "|"), Message: entry.Message})
		case "pattern_match":
			// The agent skips an empty pattern.
			if entry.PatternRegex == "" {
				continue
			}
			out = append(out, policy.Rule{Name: name, Type: policy.MatchPattern,
				Pattern: entry.PatternRegex, Message: entry.Message})
		default:
			return nil, fmt.Errorf("%s has an input rule of type %q, which a sidecar cannot run",
				where, entry.Type)
		}
	}
	return out, nil
}

// translateDataMasking turns a gateway data masking rule into ONE sidecar
// mask rule with strategy mask, the only strategy every lane accepts.
//
// masked is what the listener already masks: an entity a native rule owns
// stays with that rule, because two rules on one entity is a block the
// sidecar refuses whole. det is the detector the sidecar's pii section
// builds. The result has no entities when every entity is already masked.
func translateDataMasking(r models.BoundRule, det *alcatraz.Detector, masked map[string]bool) (maskRule, error) {
	where := ruleWhere(SidecarRuleMask, r)
	if len(r.CustomEntityTypes) > 0 {
		return maskRule{}, fmt.Errorf("%s has custom entity types, which a sidecar cannot run: it has "+
			"no custom recognizer; keep only supported entity types on a rule bound to a sidecar", where)
	}
	if r.ScoreThreshold != nil && *r.ScoreThreshold < det.Threshold() {
		return maskRule{}, fmt.Errorf("%s asks for a score threshold of %g, below the %g the sidecar "+
			"detects at, so the sidecar would mask less than the rule asks; raise the rule's threshold "+
			"or lower the sidecar's pii.threshold", where, *r.ScoreThreshold, det.Threshold())
	}
	known := map[string]bool{}
	for _, e := range det.Entities() {
		known[e] = true
	}
	seen := map[string]bool{}
	var entities []string
	for _, group := range r.SupportedEntityTypes {
		for _, e := range group.EntityTypes {
			if seen[e] {
				continue
			}
			seen[e] = true
			if !known[e] {
				return maskRule{}, fmt.Errorf("%s names entity %q, which the sidecar's detector does not "+
					"load; remove it from the rule or add it to the sidecar's pii section", where, e)
			}
			if masked[e] {
				continue
			}
			entities = append(entities, e)
		}
	}
	return maskRule{Name: r.RuleName, Entities: entities, Strategy: string(alcatraz.StrategyMask)}, nil
}

// translatedSpec is the block an agent rule puts on a lane of the sidecar,
// in the sidecar's vocabulary, for the write gate: the same checks a native
// spec gets run over it. The lane's own mask rules are not folded out here;
// composition does that when it places the rule.
func translatedSpec(kind SidecarRuleKind, r models.BoundRule, cfg daemon.Config) (json.RawMessage, error) {
	switch kind {
	case SidecarRuleGuardrail:
		rules, err := translateGuardrail(r)
		if err != nil {
			return nil, err
		}
		return json.Marshal(daemon.GuardrailsConfig{Rules: rules})
	case SidecarRuleMask:
		det, err := alcatraz.DetectorFromConfig(cfg.PII)
		if err != nil {
			return nil, fmt.Errorf("the sidecar's pii section cannot build a detector: %v", err)
		}
		rule, err := translateDataMasking(r, det, nil)
		if err != nil {
			return nil, err
		}
		return json.Marshal(struct {
			Rules []maskRule `json:"rules"`
		}{Rules: []maskRule{rule}})
	}
	return nil, fmt.Errorf("a %s rule has no agent form to translate", kind)
}

// maskedEntities reads the entities a mask block already rewrites. Lenient on
// purpose: the block is the listener's own, written in the plugin's shape,
// and the deprecated singular spelling still loads on a sidecar.
func maskedEntities(mc *daemon.MaskConfig) (map[string]bool, error) {
	out := map[string]bool{}
	if mc == nil || len(mc.Rules) == 0 {
		return out, nil
	}
	var rules []struct {
		Entity   string   `json:"entity"`
		Entities []string `json:"entities"`
	}
	if err := json.Unmarshal(mc.Rules, &rules); err != nil {
		return nil, err
	}
	for _, r := range rules {
		if r.Entity != "" {
			out[r.Entity] = true
		}
		for _, e := range r.Entities {
			out[e] = true
		}
	}
	return out, nil
}

// foldGatewayRules places the translated gateway rules on their listeners,
// after everything the native fold put there. Every rule here has no
// sidecar_spec. Anything that cannot land is a refusal, a gone listener
// included, as it is for a native rule.
func foldGatewayRules(cfg daemon.Config, sidecarName string, guardrails, masking []models.BoundRule) (daemon.Config, error) {
	if len(guardrails) == 0 && len(masking) == 0 {
		return cfg, nil
	}
	listeners := make([]daemon.ListenerConfig, len(cfg.Listeners))
	copy(listeners, cfg.Listeners)
	cfg.Listeners = listeners
	refuse := func(err error) (daemon.Config, error) {
		return cfg, ErrSidecarGatewayRuleRefused{Reason: err.Error()}
	}
	index := func(kind SidecarRuleKind, b models.BoundRule) (int, error) {
		for i, l := range listeners {
			if l.Name == b.ListenerName && b.ListenerName != "" {
				return i, nil
			}
		}
		return -1, fmt.Errorf("%s rule %q is bound to listener %q, which this sidecar's "+
			"configuration no longer has; rebind or restore the listener", kind, b.RuleName, b.ListenerName)
	}
	hasAnalyzer := cfg.Analyzer != nil

	for _, r := range guardrails {
		idx, err := index(SidecarRuleGuardrail, r)
		if err != nil {
			return refuse(err)
		}
		rules, err := translateGuardrail(r)
		if err != nil {
			return refuse(err)
		}
		if len(rules) == 0 {
			continue
		}
		// The translated block passes every check a native block passes.
		spec, err := json.Marshal(daemon.GuardrailsConfig{Rules: rules})
		if err != nil {
			return refuse(fmt.Errorf("guardrail rule %q: %v", r.RuleName, err))
		}
		if err := ValidateSidecarRuleSpec(SidecarRuleGuardrail, r.RuleName, spec); err != nil {
			return refuse(err)
		}
		if err := validateSpecForLane(SidecarRuleGuardrail, r.RuleName, spec, sidecarName, listeners[idx], hasAnalyzer); err != nil {
			return refuse(err)
		}
		listeners[idx].Guardrails = appendGuardrails(listeners[idx].Guardrails, rules)
	}

	if len(masking) == 0 {
		return cfg, nil
	}
	det, err := alcatraz.DetectorFromConfig(cfg.PII)
	if err != nil {
		return refuse(fmt.Errorf("the sidecar's pii section cannot build a detector: %v", err))
	}
	for _, r := range masking {
		idx, err := index(SidecarRuleMask, r)
		if err != nil {
			return refuse(err)
		}
		// What the lane masks today: its own block after the native fold, or
		// the top-level block it inherits when it has none.
		base := listeners[idx].Mask
		if base == nil {
			base = cfg.Mask
		}
		masked, err := maskedEntities(base)
		if err != nil {
			return refuse(fmt.Errorf("listener %q: its mask rules are not readable: %v", r.ListenerName, err))
		}
		rule, err := translateDataMasking(r, det, masked)
		if err != nil {
			return refuse(err)
		}
		if len(rule.Entities) == 0 {
			continue
		}
		spec, err := json.Marshal(struct {
			Rules []maskRule `json:"rules"`
		}{Rules: []maskRule{rule}})
		if err != nil {
			return refuse(fmt.Errorf("data masking rule %q: %v", r.RuleName, err))
		}
		if err := ValidateSidecarRuleSpec(SidecarRuleMask, r.RuleName, spec); err != nil {
			return refuse(err)
		}
		if err := validateSpecForLane(SidecarRuleMask, r.RuleName, spec, sidecarName, listeners[idx], hasAnalyzer); err != nil {
			return refuse(err)
		}
		var rules []json.RawMessage
		if base != nil && len(base.Rules) > 0 {
			if err := json.Unmarshal(base.Rules, &rules); err != nil {
				return refuse(fmt.Errorf("listener %q: its mask rules are not a list: %v", r.ListenerName, err))
			}
		}
		translated, err := json.Marshal(rule)
		if err != nil {
			return refuse(fmt.Errorf("data masking rule %q: %v", r.RuleName, err))
		}
		raw, err := json.Marshal(append(rules, translated))
		if err != nil {
			return refuse(fmt.Errorf("listener %q: %v", r.ListenerName, err))
		}
		block := daemon.MaskConfig{Rules: raw}
		if listeners[idx].Mask != nil {
			block = *listeners[idx].Mask
			block.Rules = raw
		}
		listeners[idx].Mask = &block
	}
	return cfg, nil
}

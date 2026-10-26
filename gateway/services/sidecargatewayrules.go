package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/pii/alcatraz"
	"github.com/hoophq/hoop/sidecar/policy"
	"gorm.io/gorm"
)

// A gateway rule reaches a sidecar listener through the listener's mirror
// connection, the way it reaches an agent connection: by connection id or by
// a shared attribute. Composition translates it into the sidecar's own
// vocabulary and places it on the listener AFTER the listener's native rules.
// Nothing is stored; the translation is derived on every handshake.
//
// A rule that cannot be translated is refused whole, never applied in part:
// a rule that saved and enforces less than the admin sees bound is the
// failure this file exists to prevent. The rule APIs refuse at the save
// (422); a rule bound another way is refused at the handshake, where the
// sidecar keeps its last good rules and the page shows the reason.
//
// Known difference: a sidecar matches each statement, the agent the whole input.

// ErrSidecarGatewayRuleRefused is a gateway rule that reaches a sidecar and
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

// translateGuardrail turns a gateway guardrail rule into sidecar rules.
//
// deny_words_list becomes a pattern_match on the quoted words: the sidecar's
// own deny_words_list is case-insensitive and would deny more than the agent
// does. An output rule is refused: a sidecar checks statements, not results.
func translateGuardrail(r models.MirrorGuardrailRule) ([]policy.Rule, error) {
	where := fmt.Sprintf("guardrail rule %q, reaching listener %q,", r.RuleName, r.ListenerName)
	output, err := decodeGatewayGuardrails(r.Output)
	if err != nil {
		return nil, fmt.Errorf("%s has output rules that are not readable: %v", where, err)
	}
	if len(output) > 0 {
		return nil, fmt.Errorf("%s has output rules, which a sidecar cannot run: it checks statements, "+
			"not results; keep only input rules on a rule that reaches a sidecar", where)
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
func translateDataMasking(r models.MirrorDataMaskingRule, det *alcatraz.Detector, masked map[string]bool) (maskRule, error) {
	where := fmt.Sprintf("data masking rule %q, reaching listener %q,", r.RuleName, r.ListenerName)
	if len(r.CustomEntityTypes) > 0 {
		return maskRule{}, fmt.Errorf("%s has custom entity types, which a sidecar cannot run: it has "+
			"no custom recognizer; keep only supported entity types on a rule that reaches a sidecar", where)
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
// after everything foldSidecarRules put there.
//
// A listener the configuration no longer has is skipped, not refused: its
// mirror goes with it in the same write, and with the mirror every binding
// that reached it. Everything else that cannot land is a refusal.
func foldGatewayRules(cfg daemon.Config, sidecarName string, guardrails []models.MirrorGuardrailRule, masking []models.MirrorDataMaskingRule) (daemon.Config, error) {
	if len(guardrails) == 0 && len(masking) == 0 {
		return cfg, nil
	}
	listeners := make([]daemon.ListenerConfig, len(cfg.Listeners))
	copy(listeners, cfg.Listeners)
	cfg.Listeners = listeners
	index := map[string]int{}
	for i, l := range listeners {
		index[l.Name] = i
	}
	refuse := func(err error) (daemon.Config, error) {
		return cfg, ErrSidecarGatewayRuleRefused{Reason: err.Error()}
	}
	hasAnalyzer := cfg.Analyzer != nil

	for _, r := range guardrails {
		idx, ok := index[r.ListenerName]
		if !ok {
			continue
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
		idx, ok := index[r.ListenerName]
		if !ok {
			continue
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

// CheckSidecarsReachedByGatewayRule re-composes every sidecar a gateway rule
// reaches through a mirror, in the caller's transaction, after the rule and
// its bindings are written there. A sidecar that cannot run the rule refuses
// the write with ErrSidecarBindingBroken, which reads 422.
func CheckSidecarsReachedByGatewayRule(tx *gorm.DB, orgID string, kind SidecarRuleKind, ruleName string) error {
	org, err := uuid.Parse(orgID)
	if err != nil {
		return fmt.Errorf("%w: parsing the organization id: %v", ErrSidecarRulesUnavailable, err)
	}
	var ids []string
	switch kind {
	case SidecarRuleGuardrail:
		ids, err = models.SidecarsReachedByGuardrailRule(tx, org, ruleName)
	case SidecarRuleMask:
		ids, err = models.SidecarsReachedByDataMaskingRule(tx, org, ruleName)
	default:
		return fmt.Errorf("a %s rule does not reach a sidecar through a mirror", kind)
	}
	if err != nil {
		return fmt.Errorf("%w: reading the sidecars %s rule %q reaches: %v", ErrSidecarRulesUnavailable, kind, ruleName, err)
	}
	for _, id := range ids {
		sc, err := models.GetSidecarByNameOrID(tx, orgID, id)
		if err != nil {
			return fmt.Errorf("%w: reading sidecar %q: %v", ErrSidecarRulesUnavailable, id, err)
		}
		// The plane serves no rules to a sidecar that runs its config file.
		if sc.Configuration.LoadFromDisk != nil && *sc.Configuration.LoadFromDisk {
			continue
		}
		if err := checkComposedSidecar(tx, sc); err != nil {
			if errors.Is(err, ErrSidecarRulesUnavailable) {
				return err
			}
			return ErrSidecarBindingBroken{Reason: fmt.Sprintf("sidecar %q: %v", sc.Name, err)}
		}
	}
	return nil
}

// checkComposedSidecar is the document check of checkComposedWithRule over
// the sidecar as stored, with no candidate: the write is already in the
// transaction.
func checkComposedSidecar(db *gorm.DB, sc *models.Sidecar) error {
	licenseData, err := models.GetOrgLicenseData(db, sc.OrgID)
	if err != nil {
		return fmt.Errorf("%w: reading the license of the organization owning sidecar %q: %v",
			ErrSidecarRulesUnavailable, sc.Name, err)
	}
	composed, err := ComposeSidecarConfiguration(db, sc)
	if err != nil {
		return err
	}
	if err := validateComposedAnalyzers(composed); err != nil {
		return err
	}
	if err := CheckSidecarCapabilities(sc, composed); err != nil {
		return err
	}
	if err := CheckSidecarConfigurationLimits(composed, licenseData); err != nil {
		return fmt.Errorf("the rules reaching it put it over its rule limit: %w", err)
	}
	return nil
}

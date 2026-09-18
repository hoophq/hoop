package services

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/policy"
	"gorm.io/gorm"
)

// The two guardrail rule types both engines spell identically. The sidecar
// knows six more (operation, table, pii, http_resource, http_status,
// grpc_status) and the gateway knows none of them, so a rule carrying one is
// refused at the bind rather than served to a sidecar that would enforce
// something the gateway page cannot show, or shipped to an agent that would
// drop it.
//
// ADR-0009 declined to federate the two vocabularies. Staying inside the
// subset ADR-0011 observed is spelled the same in both is what keeps this
// inside that decision instead of reversing it.
const (
	ruleTypeDenyWords = "deny_words_list"
	ruleTypePattern   = "pattern_match"
)

// ComposeSidecarConfiguration returns the document to serve: the sidecar's
// stored configuration with every bound rule folded into the block the daemon
// reads for it.
//
// Nothing is persisted. The stored document stays what an admin authored, and
// the served one is derived on every handshake, so editing a rule bound to
// three hundred sidecars is one row update with no fan-out and no half-written
// fleet.
//
// Composition only ever touches guardrails, mask and the per-listener analyzer
// block. Those are exactly the sections sidecar/daemon/reload.go strips into
// its per-lane rule document, so a rule edit hot-swaps into a running sidecar
// instead of asking it to restart (ADR-0014).
func ComposeSidecarConfiguration(db *gorm.DB, sc *models.Sidecar) (daemon.Config, error) {
	cfg := daemon.Config(sc.Configuration)

	orgID, err := uuid.Parse(sc.OrgID)
	if err != nil {
		return cfg, fmt.Errorf("failed parsing the sidecar organization id: %w", err)
	}

	bound, err := models.ListGuardrailRulesForSidecar(db, orgID, sc.ID)
	if err != nil {
		return cfg, fmt.Errorf("failed loading the guardrail rules bound to this sidecar: %w", err)
	}
	if len(bound) == 0 {
		return cfg, nil
	}

	// Copy the listener slice before writing into it. cfg shares its backing
	// array with the row the middleware loaded, and composing in place would
	// leave the next request reading a document with rules already folded in
	// -- which would then be folded again.
	listeners := make([]daemon.ListenerConfig, len(cfg.Listeners))
	copy(listeners, cfg.Listeners)
	cfg.Listeners = listeners

	for _, b := range bound {
		rules, err := guardrailRulesToPolicy(b.RuleName, b.Input)
		if err != nil {
			return cfg, err
		}
		if len(rules) == 0 {
			continue
		}
		if b.ListenerName == "" {
			cfg.Guardrails = appendGuardrails(cfg.Guardrails, rules)
			continue
		}
		idx := -1
		for i, l := range listeners {
			if l.Name == b.ListenerName {
				idx = i
				break
			}
		}
		if idx == -1 {
			// The listener was renamed or removed after the rule bound to it.
			// Skipping would serve a document that silently enforces less than
			// the admin sees bound; the handshake answers 422 and the sidecar
			// keeps the rules it already has.
			return cfg, fmt.Errorf("guardrail rule %q is bound to listener %q, which this sidecar's "+
				"configuration no longer has; rebind or restore the listener", b.RuleName, b.ListenerName)
		}
		listeners[idx].Guardrails = appendGuardrails(listeners[idx].Guardrails, rules)
	}
	return cfg, nil
}

// appendGuardrails adds rules to a block, creating it when absent.
//
// Append, never replace. The daemon concatenates a lane's own rules with the
// top-level ones and the first match wins, so adding is monotonic in the
// allow/deny outcome. Replacing would also erase `rules: []`, which is how a
// lane opts out of the defaults entirely.
func appendGuardrails(gc *daemon.GuardrailsConfig, rules []policy.Rule) *daemon.GuardrailsConfig {
	if gc == nil {
		return &daemon.GuardrailsConfig{Rules: rules}
	}
	out := *gc
	out.Rules = append(append([]policy.Rule{}, gc.Rules...), rules...)
	return &out
}

// guardrailRulesToPolicy translates one stored guardrail rule into the
// sidecar's own rule type.
//
// Only the request side is read. The gateway rule has an output half and the
// sidecar has no output guardrail: it denies requests and masks responses
// (ADR-0009 rule 2). Serving an output rule as if it were a request rule would
// deny statements the admin meant to redact, so ValidateGuardrailRuleForSidecar
// refuses the binding instead and this function never sees one.
func guardrailRulesToPolicy(ruleName string, input json.RawMessage) ([]policy.Rule, error) {
	items, err := decodeGuardrailRules(input)
	if err != nil {
		return nil, fmt.Errorf("guardrail rule %q: %w", ruleName, err)
	}
	out := make([]policy.Rule, 0, len(items))
	for i, it := range items {
		r := policy.Rule{
			// The name reaches the sidecar's audit rows and its deny message,
			// so it carries the gateway rule's name rather than an index.
			// Suffixed only when one gateway rule holds several entries.
			Name:    ruleName,
			Type:    policy.MatchType(it.Type),
			Words:   it.Words,
			Pattern: it.PatternRegex,
			Message: it.Message,
		}
		if len(items) > 1 {
			r.Name = fmt.Sprintf("%s[%d]", ruleName, i)
		}
		out = append(out, r)
	}
	return out, nil
}

// guardrailRuleEntry is the stored shape of one rule entry, matching
// gateway/guardrails.Rule. Declared here rather than imported so the
// translation reads the wire shape the column actually holds.
type guardrailRuleEntry struct {
	Type         string   `json:"type"`
	Words        []string `json:"words"`
	PatternRegex string   `json:"pattern_regex"`
	Message      string   `json:"message"`
}

func decodeGuardrailRules(raw json.RawMessage) ([]guardrailRuleEntry, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var doc struct {
		Rules []guardrailRuleEntry `json:"rules"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("stored rules are not the expected shape: %w", err)
	}
	return doc.Rules, nil
}

// ValidateGuardrailRuleForSidecar refuses a rule that cannot reach a sidecar.
//
// Every refusal here exists because the alternative is silent. A rule the UI
// accepts and no sidecar can enforce looks identical to a working one: the
// fleet reports itself healthy, the rule matches nothing, and nobody learns
// until the statement it was written for goes through.
//
// It runs on the bind AND on every later write to a rule that is already
// bound, because editing a compliant rule into a non-compliant one otherwise
// walks straight past it.
func ValidateGuardrailRuleForSidecar(ruleName string, input, output json.RawMessage) error {
	outItems, err := decodeGuardrailRules(output)
	if err != nil {
		return fmt.Errorf("guardrail rule %q: %w", ruleName, err)
	}
	if len(outItems) > 0 {
		return fmt.Errorf("guardrail rule %q carries output rules, which a sidecar cannot enforce: "+
			"it denies requests and masks responses, so a response-side control is a data masking "+
			"rule there. Bind a rule with input rules only", ruleName)
	}

	items, err := decodeGuardrailRules(input)
	if err != nil {
		return fmt.Errorf("guardrail rule %q: %w", ruleName, err)
	}
	if len(items) == 0 {
		return fmt.Errorf("guardrail rule %q has no input rules, so binding it to a sidecar would "+
			"enforce nothing", ruleName)
	}

	for _, it := range items {
		switch it.Type {
		case ruleTypeDenyWords:
			if len(it.Words) == 0 {
				return fmt.Errorf("guardrail rule %q has a %s entry with no words", ruleName, it.Type)
			}
		case ruleTypePattern:
			if it.PatternRegex == "" {
				return fmt.Errorf("guardrail rule %q has a %s entry with no pattern", ruleName, it.Type)
			}
			// The sidecar compiles patterns with Go's RE2 when it LOADS the
			// document, so one bad pattern refuses the whole configuration and
			// takes every other rule on that sidecar down with it. The gateway
			// compiles at match time and only errors per statement, which is
			// why a PCRE pattern saves clean today. Five of the nine seeded
			// rulepacks use negative lookahead, which RE2 rejects.
			if _, err := regexp.Compile(it.PatternRegex); err != nil {
				return fmt.Errorf("guardrail rule %q has a pattern a sidecar cannot compile: %v. "+
					"Sidecars use Go's RE2, which has no lookahead or backreferences", ruleName, err)
			}
		default:
			return fmt.Errorf("guardrail rule %q carries the rule type %q, which a sidecar's "+
				"configuration cannot express: only %s and %s are shared by both engines",
				ruleName, it.Type, ruleTypeDenyWords, ruleTypePattern)
		}
	}
	return nil
}

// ValidateSidecarRuleTargets refuses a binding that does not name exactly one
// listener on a sidecar this organization owns.
//
// Listener names are not unique in the daemon's own validation, which keys on
// the bind address, so "the listener called appdb" can mean two lanes. The
// control plane refuses such a document on write (ValidateListenerNames), and
// this is the second half: a binding must still resolve against the document
// as stored right now, because a listener can be renamed after a rule bound to
// it. The same refusal the review path already applies.
func ValidateSidecarRuleTargets(db *gorm.DB, orgID string, targets []models.SidecarRuleTarget) error {
	seen := map[string]*models.Sidecar{}
	for _, t := range targets {
		sc, ok := seen[t.SidecarID]
		if !ok {
			var err error
			sc, err = models.GetSidecarByNameOrID(db, orgID, t.SidecarID)
			if err != nil {
				return fmt.Errorf("sidecar %q was not found in this organization", t.SidecarID)
			}
			seen[t.SidecarID] = sc
		}
		if t.ListenerName == "" {
			continue
		}
		matches := 0
		for _, l := range sc.Configuration.Listeners {
			if l.Name == t.ListenerName {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("sidecar %q has %d listeners named %q; a rule must name exactly one",
				sc.Name, matches, t.ListenerName)
		}
	}
	return nil
}

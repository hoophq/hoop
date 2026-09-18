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
func foldSidecarRules(cfg daemon.Config, guardrails []models.BoundRule, masking []models.MaskBinding, analyzers []models.AnalyzerBinding) (daemon.Config, error) {
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
		for i, l := range listeners {
			if l.Name == listenerName {
				return i, nil
			}
		}
		return -1, fmt.Errorf("%s rule %q is bound to listener %q, which this sidecar's "+
			"configuration no longer has; rebind or restore the listener", kind, ruleName, listenerName)
	}

	// The detection threshold is process-wide -- one detector is built from the
	// top-level pii section and every lane shares it -- so it is folded once,
	// for the whole document, rather than per scope.
	if th, err := maskThresholdFor(masking); err != nil {
		return cfg, err
	} else if th != nil {
		raw, err := withPIIThreshold(cfg.PII, *th)
		if err != nil {
			return cfg, err
		}
		cfg.PII = raw
	}

	// Masking replaces rather than appends, so the rules are grouped by scope
	// and each scope is written once.
	maskByScope := map[string][]models.MaskBinding{}
	for _, b := range masking {
		maskByScope[b.ListenerName] = append(maskByScope[b.ListenerName], b)
	}
	for scope, group := range maskByScope {
		raw, err := maskRulesFor(group)
		if err != nil {
			return cfg, err
		}
		if raw == nil {
			continue
		}
		if scope == "" {
			cfg.Mask = &daemon.MaskConfig{Rules: raw}
			continue
		}
		idx, err := listenerIndex("data masking", group[0].RuleName, scope)
		if err != nil {
			return cfg, err
		}
		listeners[idx].Mask = &daemon.MaskConfig{Rules: raw}
	}

	// The analyzer is a per-lane component: there is no top-level risk action
	// to set, so a rule bound to the whole sidecar reaches every lane that has
	// an analyzer block and is refused if none does.
	for _, b := range analyzers {
		scopes := []int{}
		if b.ListenerName == "" {
			for i := range listeners {
				if listeners[i].Analyzer != nil {
					scopes = append(scopes, i)
				}
			}
			if len(scopes) == 0 {
				return cfg, fmt.Errorf("analyzer rule %q is bound to every listener on this sidecar "+
					"and none of them has an analyzer block; enable the analyzer on at least one "+
					"listener first", b.RuleName)
			}
		} else {
			idx, err := listenerIndex("analyzer", b.RuleName, b.ListenerName)
			if err != nil {
				return cfg, err
			}
			scopes = append(scopes, idx)
		}
		for _, idx := range scopes {
			block, err := analyzerBlockFor(b.RuleName, b.RiskEvaluation, b.CustomPrompt, listeners[idx].Analyzer)
			if err != nil {
				return cfg, err
			}
			listeners[idx].Analyzer = block
		}
	}

	for _, b := range guardrails {
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
		idx, err := listenerIndex("guardrail", b.RuleName, b.ListenerName)
		if err != nil {
			return cfg, err
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

// ---------------------------------------------------------------------------
// Data masking
// ---------------------------------------------------------------------------

// maskRule is the alcatraz rule shape, declared here because MaskConfig.Rules
// is raw JSON: the daemon keeps the shape with the plugin that owns it, and
// this is the control plane writing that same shape.
//
// Only Entities is produced. Columns, Strategy, KeepLast and MaskChar have no
// counterpart on the gateway rule, so a composed rule redacts whole values of
// the named entity types, which is what the gateway page already means.
type maskRule struct {
	Name     string   `json:"name,omitempty"`
	Entities []string `json:"entities,omitempty"`
}

// maskRulesFor renders the mask block for one scope from the rules bound there.
//
// REPLACE, not append, and that asymmetry with guardrails is the daemon's, not
// a choice here: a lane's mask.rules replace the top-level ones rather than
// concatenating, because a rule owns an entity and two rewrites of one value
// is not a policy anyone wrote. So each scope carries the complete list for
// that scope.
func maskRulesFor(bound []models.MaskBinding) (json.RawMessage, error) {
	rules := make([]maskRule, 0, len(bound))
	for _, b := range bound {
		entities, err := maskEntities(b.RuleName, b.SupportedEntityTypes)
		if err != nil {
			return nil, err
		}
		if len(entities) == 0 {
			continue
		}
		rules = append(rules, maskRule{Name: b.RuleName, Entities: entities})
	}
	if len(rules) == 0 {
		return nil, nil
	}
	return json.Marshal(rules)
}

// maskThresholdFor resolves the one detection threshold a sidecar can have.
//
// The gateway carries a threshold PER RULE; the sidecar builds ONE detector
// for the process from its pii section, so there is no per-rule and no
// per-lane threshold to write it into. Two bound rules asking for different
// thresholds therefore has no correct answer, and picking one would mask at a
// sensitivity nobody configured. It is refused instead -- here, and on the
// write that creates the second binding, so an admin hears it while editing
// rather than through a sidecar that quietly runs the other rule's number.
//
// A nil threshold is "unset", not zero: alcatraz reads zero as "use the
// default", so a rule that leaves it blank does not compete for the value.
func maskThresholdFor(bound []models.MaskBinding) (*float64, error) {
	var out *float64
	var owner string
	for _, b := range bound {
		if b.ScoreThreshold == nil {
			continue
		}
		if out == nil {
			v := *b.ScoreThreshold
			out, owner = &v, b.RuleName
			continue
		}
		if *out != *b.ScoreThreshold {
			return nil, fmt.Errorf("data masking rules %q and %q are bound to this sidecar with "+
				"different score thresholds (%v and %v); a sidecar detects with one threshold for "+
				"the whole process, so give both rules the same one", owner, b.RuleName,
				*out, *b.ScoreThreshold)
		}
	}
	return out, nil
}

// withPIIThreshold writes the threshold into the sidecar's pii section without
// disturbing what the operator put there.
//
// Through a map rather than a struct, because the section belongs to the
// detector plugin and this package links none: entities, ignored, allow_list
// and language are read by alcatraz and must survive untouched. Only the one
// key the control plane owns is replaced.
func withPIIThreshold(raw json.RawMessage, threshold float64) (json.RawMessage, error) {
	section := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &section); err != nil {
			return nil, fmt.Errorf("this sidecar's pii section is not an object, so a bound rule's "+
				"score threshold has nowhere to go: %w", err)
		}
	}
	section["threshold"] = threshold
	return json.Marshal(section)
}

// ValidateMaskThresholdForSidecars refuses a binding that would leave one
// sidecar carrying two different detection thresholds.
//
// Run at write time so the refusal reaches the admin editing the rule. The
// compose-time check in maskThresholdFor stays as the backstop: a threshold
// can also start conflicting because the OTHER rule was edited, and this guard
// only sees the rule in front of it.
func ValidateMaskThresholdForSidecars(db *gorm.DB, orgID uuid.UUID, ruleName string, threshold *float64, targets []models.SidecarRuleTarget) error {
	seen := map[string]bool{}
	for _, t := range targets {
		if seen[t.SidecarID] {
			continue
		}
		seen[t.SidecarID] = true

		bound, err := models.ListDataMaskingRulesForSidecar(db, orgID, t.SidecarID)
		if err != nil {
			return fmt.Errorf("failed reading the data masking rules already bound to sidecar %q: %w",
				t.SidecarID, err)
		}
		// The rule's own rows are whatever it was bound with before this
		// request; the request's threshold replaces them.
		others := []models.MaskBinding{{RuleName: ruleName, ScoreThreshold: threshold}}
		for _, b := range bound {
			if b.RuleName != ruleName {
				others = append(others, b)
			}
		}
		if _, err := maskThresholdFor(others); err != nil {
			return err
		}
	}
	return nil
}

// maskEntities flattens the gateway's grouped entity types into the flat list
// the sidecar rule takes. The gateway groups them under a display name the
// sidecar has no field for; the group name is dropped and the rule keeps the
// rule's own name, which is what reaches the audit trail.
func maskEntities(ruleName string, groups []models.SupportedEntityTypesEntry) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, g := range groups {
		for _, e := range g.EntityTypes {
			if e == "" || seen[e] {
				continue
			}
			seen[e] = true
			out = append(out, e)
		}
	}
	return out, nil
}

// ValidateDataMaskingRuleForSidecar refuses a masking rule a sidecar cannot
// enforce.
//
// The entity vocabulary itself is shared: alcatraz is a DLP provider on both
// sides, so "US_SSN" means the same thing in either engine. What does not
// cross is a custom entity type: the sidecar's pii section narrows and ignores
// built-in recognizers, and has no way to register a regex of its own. Storing
// one and serving the rest would mask less than the admin configured, silently.
func ValidateDataMaskingRuleForSidecar(ruleName string, supported []models.SupportedEntityTypesEntry, custom []models.CustomEntityTypesEntry) error {
	if len(custom) > 0 {
		return fmt.Errorf("data masking rule %q defines custom entity types, which a sidecar cannot "+
			"detect: its pii section selects and ignores built-in recognizers and cannot register a "+
			"regex of its own. Bind a rule that names supported entity types only", ruleName)
	}
	entities, err := maskEntities(ruleName, supported)
	if err != nil {
		return err
	}
	if len(entities) == 0 {
		return fmt.Errorf("data masking rule %q names no entity types, so binding it to a sidecar "+
			"would mask nothing", ruleName)
	}
	return nil
}

// ---------------------------------------------------------------------------
// AI session analyzer
// ---------------------------------------------------------------------------

// riskActionForSidecar maps one gateway risk action onto the sidecar's own.
//
// Two of the three cross. The gateway's require_access_request corresponds to
// the sidecar's require_review, and daemon.LaneAnalyzerConfig even carries the
// ApprovalRule field it needs -- but the sidecar REFUSES require_review at
// startup today (sidecar/daemon/analyzer.go, EVL-289 is still open). Serving
// it would kill every sidecar that restarts and leave the running ones on
// stale rules, which is the control plane manufacturing the divergence it
// exists to report. So it is refused here, naming the ticket, until the
// runtime catches up with the schema.
func riskActionForSidecar(ruleName, tier string, a models.RiskEvaluationAction) (string, error) {
	switch a {
	case "":
		return "", nil // a tier the admin left unset; the analyzer defaults it to allow
	case models.AllowExecution:
		return "allow", nil
	case models.BlockExecution:
		return "block", nil
	case models.RequireAccessRequest:
		return "", fmt.Errorf("analyzer rule %q sets %s risk to require_access_request, which a "+
			"sidecar refuses at startup: the review action is declared in its configuration but not "+
			"yet implemented on its data path (EVL-289). Use block or allow until it lands",
			ruleName, tier)
	}
	return "", fmt.Errorf("analyzer rule %q sets %s risk to the unknown action %q", ruleName, tier, a)
}

// ValidateAnalyzerRuleForSidecar refuses an analyzer rule that cannot run on a
// listener.
//
// Beyond the action mapping there is a precondition the gateway rule cannot
// express: the analyzer is a per-lane component, and a lane only has one when
// its configuration carries an analyzer block naming the trigger and the cost
// controls. Composing risk actions onto a lane that has none would either be
// refused by the sidecar or classify every statement it sees, which is a bill
// nobody approved.
func ValidateAnalyzerRuleForSidecar(ruleName string, ev models.AISessionAnalyzerRiskEvaluation) error {
	for _, tier := range []struct {
		name   string
		action models.RiskEvaluationAction
	}{
		{"low", effectiveAction(ev.LowRisk, ev.LowRiskAction)},
		{"medium", effectiveAction(ev.MediumRisk, ev.MediumRiskAction)},
		{"high", effectiveAction(ev.HighRisk, ev.HighRiskAction)},
	} {
		if _, err := riskActionForSidecar(ruleName, tier.name, tier.action); err != nil {
			return err
		}
	}
	return nil
}

// effectiveAction reads the tier form when present and the flat form
// otherwise. Both spellings are stored; the tier one is the newer and carries
// the approval rule name beside the action.
func effectiveAction(tier *models.AISessionAnalyzerRiskTier, flat models.RiskEvaluationAction) models.RiskEvaluationAction {
	if tier != nil {
		return tier.Action
	}
	return flat
}

// ValidateAnalyzerTargetListeners refuses a binding whose listener has no
// analyzer block.
//
// The precondition the gateway rule cannot express: the analyzer is a per-lane
// component, and a lane only has one when its configuration names the trigger
// and the call budget. Composing risk actions onto a lane without them is
// refused by the sidecar, so it is refused here first, where the admin is
// looking.
//
// A rule bound to the whole sidecar needs at least ONE lane with the block --
// it reaches those lanes and leaves the rest alone, so requiring every lane to
// have an analyzer would refuse the ordinary case of one analyzed lane beside
// several plain ones.
func ValidateAnalyzerTargetListeners(db *gorm.DB, orgID, ruleName string, targets []models.SidecarRuleTarget) error {
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
		found := false
		for _, l := range sc.Configuration.Listeners {
			if t.ListenerName != "" && l.Name != t.ListenerName {
				continue
			}
			if l.Analyzer != nil {
				found = true
				break
			}
		}
		if found {
			continue
		}
		if t.ListenerName == "" {
			return fmt.Errorf("analyzer rule %q is bound to every listener on sidecar %q and none of "+
				"them has an analyzer block; enable the analyzer on at least one listener first, so it "+
				"carries a trigger and a call budget", ruleName, sc.Name)
		}
		return fmt.Errorf("analyzer rule %q is bound to listener %q on sidecar %q, which has no "+
			"analyzer block; enable the analyzer on that listener first, so it carries a trigger and "+
			"a call budget", ruleName, t.ListenerName, sc.Name)
	}
	return nil
}

// analyzerBlockFor folds one analyzer rule onto a listener's existing analyzer
// block.
//
// The rule owns the risk decision and the prompt. The listener keeps the
// trigger, send mode, fail_open, timeouts, cache and max_calls: those are cost
// controls an operator tunes per lane, and a rule distributed to a fleet has no
// business overwriting them.
func analyzerBlockFor(ruleName string, ev models.AISessionAnalyzerRiskEvaluation, prompt *string, base *daemon.LaneAnalyzerConfig) (*daemon.LaneAnalyzerConfig, error) {
	if base == nil {
		return nil, fmt.Errorf("analyzer rule %q is bound to a listener with no analyzer block; "+
			"enable the analyzer on that listener first, so it carries a trigger and a call budget",
			ruleName)
	}
	out := *base
	for _, tier := range []struct {
		name   string
		action models.RiskEvaluationAction
		field  *string
	}{
		{"low", effectiveAction(ev.LowRisk, ev.LowRiskAction), &out.LowRisk},
		{"medium", effectiveAction(ev.MediumRisk, ev.MediumRiskAction), &out.MediumRisk},
		{"high", effectiveAction(ev.HighRisk, ev.HighRiskAction), &out.HighRisk},
	} {
		v, err := riskActionForSidecar(ruleName, tier.name, tier.action)
		if err != nil {
			return nil, err
		}
		if v != "" {
			*tier.field = v
		}
	}
	if prompt != nil && *prompt != "" {
		out.Prompt = *prompt
	}
	return &out, nil
}

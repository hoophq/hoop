package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/policy"
	"gorm.io/gorm"
)

// ImportedRule is one rule of an imported config file, as a control-plane rule
// item: its kind, its name, its sidecar_spec and the listeners it binds to.
type ImportedRule struct {
	Kind    SidecarRuleKind
	Name    string
	Spec    json.RawMessage
	Targets []ImportedTarget
}

// ImportedTarget is one listener an imported rule binds to, and the rule's
// place in it. The sidecar runs rules in order and the first match wins, so
// the place is the file's order and composition must keep it.
type ImportedTarget struct {
	Listener string
	Position int
}

// SplitSidecarConfiguration moves the rules of an imported file out of the
// document and into rule items, one per file rule (one per lane for the
// analyzer). The stripped document plus the items, folded by foldSidecarRules,
// serve what the file served.
//
// taken reports a name already used in the organization for that kind. Its
// error, and a name no try finds free, end the split with an
// ErrImportedRuleNameCheck.
func SplitSidecarConfiguration(sidecarName string, cfg daemon.Config, taken func(SidecarRuleKind, string) (bool, error)) (daemon.Config, []ImportedRule, error) {
	listeners := make([]daemon.ListenerConfig, len(cfg.Listeners))
	copy(listeners, cfg.Listeners)
	cfg.Listeners = listeners

	used := map[string]bool{}
	name := func(kind SidecarRuleKind, parts ...string) (string, error) {
		base := slugRuleName(strings.Join(parts, "-"))
		candidate := base
		for i := 2; i < maxNameTries+2; i++ {
			busy := used[string(kind)+"/"+candidate]
			if !busy && taken != nil {
				var err error
				if busy, err = taken(kind, candidate); err != nil {
					return "", ErrImportedRuleNameCheck{fmt.Errorf("checking %s rule name %q: %w", kind, candidate, err)}
				}
			}
			if !busy {
				used[string(kind)+"/"+candidate] = true
				return candidate, nil
			}
			candidate = base + "-" + strconv.Itoa(i)
		}
		return "", ErrImportedRuleNameCheck{fmt.Errorf("no free %s rule name for %q after %d tries", kind, base, maxNameTries)}
	}

	var out []ImportedRule
	var err error
	if out, err = splitGuardrails(sidecarName, &cfg, name, out); err != nil {
		return cfg, nil, err
	}
	if out, err = splitMask(sidecarName, &cfg, name, out); err != nil {
		return cfg, nil, err
	}
	out, err = splitAnalyzer(sidecarName, &cfg, name, out)
	return cfg, out, err
}

type ruleNamer func(kind SidecarRuleKind, parts ...string) (string, error)

// splitGuardrails: a lane evaluates its own rules, then the top-level ones,
// unless it opts out with `rules: []` (daemon config.go resolve).
func splitGuardrails(scName string, cfg *daemon.Config, name ruleNamer, out []ImportedRule) ([]ImportedRule, error) {
	// The daemon runs a lane's own rules, then the top-level ones, so a
	// top-level rule sits after however many rules that lane has.
	own := map[string]int{}
	for i := range cfg.Listeners {
		l := &cfg.Listeners[i]
		if l.Guardrails == nil || len(l.Guardrails.Rules) == 0 {
			continue
		}
		own[l.Name] = len(l.Guardrails.Rules)
		for n, r := range l.Guardrails.Rules {
			ruleName, err := name(SidecarRuleGuardrail, scName, l.Name, entryName(r.Name, "guardrail", n))
			if err != nil {
				return nil, err
			}
			item, err := guardrailItem(ruleName, r, ImportedTarget{Listener: l.Name, Position: n})
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		block := *l.Guardrails
		block.Rules = nil
		l.Guardrails = &block
	}
	if top := cfg.Guardrails; top != nil && len(top.Rules) > 0 {
		var inheriting []string
		for _, l := range cfg.Listeners {
			if optsOutOfGuardrails(l) {
				continue
			}
			inheriting = append(inheriting, l.Name)
		}
		// A rule no lane inherits enforces nothing, so it is not imported:
		// a rule item bound nowhere would outlive the sidecar that carried it.
		for n, r := range top.Rules {
			if len(inheriting) == 0 {
				break
			}
			targets := make([]ImportedTarget, 0, len(inheriting))
			for _, lane := range inheriting {
				targets = append(targets, ImportedTarget{Listener: lane, Position: own[lane] + n})
			}
			ruleName, err := name(SidecarRuleGuardrail, scName, entryName(r.Name, "guardrail", n))
			if err != nil {
				return nil, err
			}
			item, err := guardrailItem(ruleName, r, targets...)
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		block := *top
		block.Rules = nil
		cfg.Guardrails = &block
	}
	return out, nil
}

// optsOutOfGuardrails reports a lane whose `rules: []` drops the top-level set.
// The lane's own rules were already stripped to nil, so it reads the ORIGINAL
// opt-out through the non-nil empty slice kept for it.
func optsOutOfGuardrails(l daemon.ListenerConfig) bool {
	return l.Guardrails != nil && l.Guardrails.Rules != nil && len(l.Guardrails.Rules) == 0
}

func guardrailItem(ruleName string, r policy.Rule, targets ...ImportedTarget) (ImportedRule, error) {
	spec, err := json.Marshal(daemon.GuardrailsConfig{Rules: []policy.Rule{r}})
	if err != nil {
		return ImportedRule{}, fmt.Errorf("failed rendering guardrail rule %q: %w", ruleName, err)
	}
	return ImportedRule{Kind: SidecarRuleGuardrail, Name: ruleName, Spec: spec, Targets: targets}, nil
}

// splitMask: a lane with a non-empty `rules` value replaces the top-level list
// (`[]` included, which is the opt-out). A block switched off with the
// deprecated `enabled: false` is left in the document as it is.
func splitMask(scName string, cfg *daemon.Config, name ruleNamer, out []ImportedRule) ([]ImportedRule, error) {
	owns := func(l daemon.ListenerConfig) bool { return l.Mask != nil && len(l.Mask.Rules) > 0 }
	disabled := func(m *daemon.MaskConfig) bool { return m != nil && m.Enabled != nil && !*m.Enabled }

	var inheriting []string
	for i := range cfg.Listeners {
		l := &cfg.Listeners[i]
		if !owns(*l) {
			inheriting = append(inheriting, l.Name)
			continue
		}
		if disabled(l.Mask) || isEmptyList(l.Mask.Rules) {
			continue
		}
		entries, err := splitRawList(l.Mask.Rules)
		if err != nil {
			return nil, fmt.Errorf("listener %q: the mask rules are not a list: %w", l.Name, err)
		}
		for n, e := range entries {
			ruleName, err := name(SidecarRuleMask, scName, l.Name, entryName(rawEntryName(e), "mask", n))
			if err != nil {
				return nil, err
			}
			item, err := maskItem(ruleName, e, ImportedTarget{Listener: l.Name, Position: n})
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		if l.Mask.Enabled == nil {
			l.Mask = nil
		} else {
			l.Mask = &daemon.MaskConfig{Enabled: l.Mask.Enabled}
		}
	}
	top := cfg.Mask
	if top == nil || len(top.Rules) == 0 || disabled(top) || isEmptyList(top.Rules) {
		return out, nil
	}
	entries, err := splitRawList(top.Rules)
	if err != nil {
		return nil, fmt.Errorf("the top-level mask rules are not a list: %w", err)
	}
	for n, e := range entries {
		if len(inheriting) == 0 {
			break
		}
		targets := make([]ImportedTarget, 0, len(inheriting))
		for _, lane := range inheriting {
			targets = append(targets, ImportedTarget{Listener: lane, Position: n})
		}
		ruleName, err := name(SidecarRuleMask, scName, entryName(rawEntryName(e), "mask", n))
		if err != nil {
			return nil, err
		}
		item, err := maskItem(ruleName, e, targets...)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if top.Enabled == nil {
		cfg.Mask = nil
	} else {
		cfg.Mask = &daemon.MaskConfig{Enabled: top.Enabled}
	}
	return out, nil
}

func maskItem(ruleName string, entry json.RawMessage, targets ...ImportedTarget) (ImportedRule, error) {
	spec, err := json.Marshal(struct {
		Rules []json.RawMessage `json:"rules"`
	}{Rules: []json.RawMessage{entry}})
	if err != nil {
		return ImportedRule{}, fmt.Errorf("failed rendering data masking rule %q: %w", ruleName, err)
	}
	return ImportedRule{Kind: SidecarRuleMask, Name: ruleName, Spec: spec, Targets: targets}, nil
}

// splitAnalyzer moves each lane's risk DECISION into a rule: the fields
// mergeAnalyzerBlock replaces. The lane keeps the rest as the base the rule
// merges over. A block that decides nothing (only overrides such as
// max_calls) stays in the lane: as a rule it would allow everything. The
// file's approval_rule travels with the rule; ImportSidecarRulesTx checks it.
func splitAnalyzer(scName string, cfg *daemon.Config, name ruleNamer, out []ImportedRule) ([]ImportedRule, error) {
	for i := range cfg.Listeners {
		l := &cfg.Listeners[i]
		if l.Analyzer == nil {
			continue
		}
		base := *l.Analyzer
		rule := daemon.LaneAnalyzerConfig{
			Trigger: base.Trigger, HighRisk: base.HighRisk, MediumRisk: base.MediumRisk,
			LowRisk: base.LowRisk, Prompt: base.Prompt, Message: base.Message,
			ApprovalRule: base.ApprovalRule,
		}
		if rule.Trigger == nil && rule.HighRisk == "" && rule.MediumRisk == "" && rule.LowRisk == "" &&
			rule.Prompt == "" && rule.Message == "" {
			continue
		}
		ruleName, err := name(SidecarRuleAnalyzer, scName, l.Name, "analyzer")
		if err != nil {
			return nil, err
		}
		if rule.ApprovalRule == "" && holds(rule) {
			rule.ApprovalRule = ruleName
		}
		spec, err := json.Marshal(rule)
		if err != nil {
			return nil, fmt.Errorf("failed rendering analyzer rule %q: %w", ruleName, err)
		}
		out = append(out, ImportedRule{Kind: SidecarRuleAnalyzer, Name: ruleName, Spec: spec,
			Targets: []ImportedTarget{{Listener: l.Name}}})

		base.Trigger = nil
		base.HighRisk, base.MediumRisk, base.LowRisk = "", "", ""
		base.Prompt, base.Message, base.ApprovalRule = "", "", ""
		l.Analyzer = &base
	}
	return out, nil
}

func holds(b daemon.LaneAnalyzerConfig) bool {
	for _, a := range []string{b.HighRisk, b.MediumRisk, b.LowRisk} {
		if a == analyzerReviewAction {
			return true
		}
	}
	return false
}

// ImportSidecarRulesTx writes the rule items of an import, with their bindings
// and approval rules, inside the caller's transaction.
func ImportSidecarRulesTx(tx *gorm.DB, orgID, sidecarID string, rules []ImportedRule) error {
	org, err := uuid.Parse(orgID)
	if err != nil {
		return err
	}
	// Checked before any write: a spec the rule pages refuse would store a
	// rule nobody can save again.
	for _, r := range rules {
		if err := ValidateSidecarRuleSpec(r.Kind, r.Name, r.Spec); err != nil {
			return ErrImportedRuleInvalid{err}
		}
	}
	// A name the split found free and another writer took since.
	conflict := func(err error, kind SidecarRuleKind, name string) error {
		if errors.Is(err, models.ErrAlreadyExists) {
			return fmt.Errorf("%w: %s rule %q", ErrImportedRuleConflict, kind, name)
		}
		return fmt.Errorf("%s rule %q: %w", kind, name, err)
	}
	for _, r := range rules {
		targets := make([]models.SidecarRuleTarget, 0, len(r.Targets))
		for _, t := range r.Targets {
			pos := t.Position
			targets = append(targets, models.SidecarRuleTarget{SidecarID: sidecarID, ListenerName: t.Listener, Position: &pos})
		}
		description := "Imported from the sidecar configuration file."
		switch r.Kind {
		case SidecarRuleGuardrail:
			row := &models.GuardRailRules{
				ID: uuid.NewString(), OrgID: orgID, Name: r.Name, Description: description,
				Input: map[string]any{}, Output: map[string]any{}, SidecarSpec: r.Spec,
			}
			if err := models.UpsertGuardRailRuleWithConnectionsTx(tx, row, nil, true); err != nil {
				return conflict(err, r.Kind, r.Name)
			}
			if err = models.MarkImportedRuleTx(tx, "private.guardrail_rules", org, r.Name, sidecarID); err == nil {
				err = models.SetGuardrailRuleListenersTx(tx, org, r.Name, targets)
			}
		case SidecarRuleMask:
			row := &models.DataMaskingRule{
				ID: uuid.NewString(), OrgID: orgID, Name: r.Name, Description: description,
				SupportedEntityTypes: models.SupportedEntityTypesList{},
				CustomEntityTypes:    models.CustomEntityTypesList{},
				SidecarSpec:          r.Spec,
			}
			if err := models.CreateDataMaskingRuleTx(tx, row); err != nil {
				return conflict(err, r.Kind, r.Name)
			}
			if err = models.MarkImportedRuleTx(tx, "private.datamasking_rules", org, r.Name, sidecarID); err == nil {
				err = models.SetDataMaskingRuleListenersTx(tx, org, r.Name, targets)
			}
		case SidecarRuleAnalyzer:
			if r.Spec, err = resolveImportedApprovalRule(tx, org, r.Name, r.Spec); err != nil {
				return err
			}
			row := &models.AISessionAnalyzerRules{
				OrgID: org, Name: r.Name, Description: &description,
				ConnectionNames: []string{}, SidecarSpec: r.Spec,
				RiskEvaluation: models.AISessionAnalyzerRiskEvaluation{
					LowRisk:    &models.AISessionAnalyzerRiskTier{Action: models.AllowExecution},
					MediumRisk: &models.AISessionAnalyzerRiskTier{Action: models.AllowExecution},
					HighRisk:   &models.AISessionAnalyzerRiskTier{Action: models.AllowExecution},
				},
			}
			if err := models.CreateAISessionAnalyzerRuleTx(tx, row); err != nil {
				return conflict(err, r.Kind, r.Name)
			}
			if err := SyncAnalyzerApprovalRule(tx, org, r.Name, r.Spec); err != nil {
				return err
			}
			if err = models.MarkImportedRuleTx(tx, "private.ai_session_analyzer_rules", org, r.Name, sidecarID); err == nil {
				err = models.SetAnalyzerRuleListenersTx(tx, org, r.Name, targets)
			}
		default:
			return fmt.Errorf("unknown sidecar rule kind %q", r.Kind)
		}
		if err != nil {
			return fmt.Errorf("failed binding %s rule %q: %w", r.Kind, r.Name, err)
		}
	}
	return nil
}

// resolveImportedApprovalRule keeps the approval rule the file names when the
// control plane has it as a sidecar rule, so the reviewers and the count an
// admin chose stay the ones that release a hold. A name the control plane
// does not have is replaced by the imported rule's own name, which
// SyncAnalyzerApprovalRule then creates.
func resolveImportedApprovalRule(tx *gorm.DB, orgID uuid.UUID, ruleName string, spec json.RawMessage) (json.RawMessage, error) {
	var block daemon.LaneAnalyzerConfig
	if err := decodeSpec(spec, &block); err != nil {
		return nil, fmt.Errorf("analyzer rule %q: %w", ruleName, err)
	}
	if block.ApprovalRule == "" || block.ApprovalRule == ruleName {
		return spec, nil
	}
	existing, err := models.GetAccessRequestRuleByName(tx, block.ApprovalRule, orgID)
	switch {
	case err == nil && existing.AccessType == models.AccessTypeSidecar:
		return spec, nil
	case err != nil && !errors.Is(err, gorm.ErrRecordNotFound):
		return nil, fmt.Errorf("failed reading approval rule %q: %w", block.ApprovalRule, err)
	}
	log.Infof("analyzer rule %q: the file names approval rule %q, which the control plane has not; "+
		"a managed rule named %q replaces it", ruleName, block.ApprovalRule, ruleName)
	block.ApprovalRule = ruleName
	return json.Marshal(block)
}

// DetachSidecarRulesTx unbinds every rule from one sidecar. It deletes the
// rules that sidecar's file brought and nothing now targets, with the
// approval rules the analyzer ones own; every other rule is only unbound.
func DetachSidecarRulesTx(tx *gorm.DB, orgID, sidecarID string) (models.DetachedSidecarRules, error) {
	org, err := uuid.Parse(orgID)
	if err != nil {
		return models.DetachedSidecarRules{}, err
	}
	out, err := models.DetachSidecarRulesTx(tx, org, sidecarID)
	if err != nil {
		return out, err
	}
	for _, name := range out.Analyzers {
		if err := DeleteAnalyzerApprovalRule(tx, org, name); err != nil {
			return out, err
		}
	}
	return out, nil
}

// ImportedRuleNameTaken reports a name the import cannot use: a rule of that
// kind already has it, or, for an analyzer rule, an access request rule does
// (SyncAnalyzerApprovalRule creates one with the same name).
func ImportedRuleNameTaken(db *gorm.DB, orgID string) func(SidecarRuleKind, string) (bool, error) {
	return func(kind SidecarRuleKind, name string) (bool, error) {
		var tables []string
		switch kind {
		case SidecarRuleGuardrail:
			tables = []string{"private.guardrail_rules"}
		case SidecarRuleMask:
			tables = []string{"private.datamasking_rules"}
		case SidecarRuleAnalyzer:
			tables = []string{"private.ai_session_analyzer_rules", "private.access_request_rules"}
		}
		for _, t := range tables {
			var n int64
			// A failed read is returned, not counted as taken: inside a
			// transaction every later read fails too, so no candidate would
			// ever be free.
			if err := db.Table(t).Where("org_id = ? AND name = ?", orgID, name).Count(&n).Error; err != nil {
				return false, err
			}
			if n > 0 {
				return true, nil
			}
		}
		return false, nil
	}
}

var reNotNameChar = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)
var reRepeatedSeparator = regexp.MustCompile(`[-.]{2,}`)

// slugRuleName makes a name apivalidation.ValidateResourceName accepts.
func slugRuleName(s string) string {
	s = reNotNameChar.ReplaceAllString(strings.ToLower(s), "-")
	s = reRepeatedSeparator.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-.")
	if len(s) > 240 {
		s = strings.Trim(s[:240], "-.")
	}
	for len(s) < 3 {
		s += "_"
	}
	return s
}

func entryName(name, kind string, index int) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	return kind + "-" + strconv.Itoa(index+1)
}

func rawEntryName(e json.RawMessage) string {
	var v struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(e, &v)
	return v.Name
}

func splitRawList(raw json.RawMessage) ([]json.RawMessage, error) {
	var out []json.RawMessage
	err := json.Unmarshal(raw, &out)
	return out, err
}

func isEmptyList(raw json.RawMessage) bool {
	var v []json.RawMessage
	return json.Unmarshal(raw, &v) == nil && len(v) == 0
}

// ErrImportedRuleConflict is a rule name another writer took during the import.
var ErrImportedRuleConflict = errors.New("a rule name was taken during the import; retry")

// ErrImportedRuleInvalid is an imported rule whose spec the gateway refuses.
type ErrImportedRuleInvalid struct{ Err error }

func (e ErrImportedRuleInvalid) Error() string { return e.Err.Error() }
func (e ErrImportedRuleInvalid) Unwrap() error { return e.Err }

// maxNameTries bounds the search for a free imported rule name.
const maxNameTries = 100

// ErrImportedRuleNameCheck is a name the import could not settle: the check
// failed, or no try was free. It is the plane's fault, not the file's.
type ErrImportedRuleNameCheck struct{ Err error }

func (e ErrImportedRuleNameCheck) Error() string { return e.Err.Error() }
func (e ErrImportedRuleNameCheck) Unwrap() error { return e.Err }

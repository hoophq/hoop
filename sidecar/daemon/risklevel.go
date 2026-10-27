package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/hoophq/hoop/sidecar/analyzer"
)

// CapabilityRiskLevelObject means this build decodes a risk level written as
// an object (approver, approval_groups, prompt). The cap tags on RiskLevel's
// fields put it in the header; DowngradeRiskLevels serves a build without it.
const CapabilityRiskLevelObject = "analyzer_risk_level_object"

// CapabilityAIHandoff means this build runs a risk level whose action is
// ai-handoff and decodes allowed_actions. The cap tag on AllowedActions puts
// it in the header. No downgrade exists: an older build has no action that
// lets the AI agent select one.
const CapabilityAIHandoff = "analyzer_ai_handoff"

// RiskLevel is what one risk level of an analyzer block does. It is written
// as a bare action ("block") or as an object:
//
//	{"action": "require_approval", "approver": "human", "approval_groups": ["dba"]}
//	{"action": "require_approval", "approver": "ai_agent", "prompt": "..."}
//
// A bare require_approval holds for a person under the block's approval_rule,
// as before the object form. An object that holds names its own approver,
// which defaults to ai_agent, and needs no approval_rule.
type RiskLevel struct {
	// Action is allow, warn, block, defer, require_review (also spelled
	// require_approval) or ai-handoff.
	Action string `json:"action,omitempty"`

	// Approver is human or ai_agent, for a level that holds.
	Approver string `json:"approver,omitempty" cap:"analyzer_risk_level_object"`

	// ApprovalGroups are who may approve a human level. Empty means the
	// administrators. The control plane reads them; the sidecar files the
	// level and never the groups.
	ApprovalGroups []string `json:"approval_groups,omitempty" cap:"analyzer_risk_level_object"`

	// Prompt replaces the default instruction of the approval agent on an
	// ai_agent level, or of the hand-off agent on an ai-handoff level. The
	// fixed suffix stays.
	Prompt string `json:"prompt,omitempty" cap:"analyzer_risk_level_object"`

	// AllowedActions are the actions the hand-off agent of an ai-handoff
	// level may select. Nil allows all four.
	AllowedActions *AllowedActions `json:"allowed_actions,omitempty" cap:"analyzer_ai_handoff"`

	// Guardrails and Mask are the AI rule options of this level (ENG-662):
	// the existing rules of rule_library the AI may apply to a statement
	// this level rates, and whether it may create one. They are not the
	// listener's guardrails and mask blocks, which apply as before.
	Guardrails *AIRuleOptions `json:"guardrails,omitempty" cap:"analyzer_ai_rules"`
	Mask       *AIRuleOptions `json:"mask,omitempty" cap:"analyzer_ai_rules"`

	// object records that the level was decoded from an object, so an
	// object that holds and names no approver keeps the ai_agent default
	// until foldApprovalSpellings writes it down.
	object bool
}

// IsZero reports an unset level, for omitzero.
func (r RiskLevel) IsZero() bool {
	return r.Action == "" && r.Approver == "" && len(r.ApprovalGroups) == 0 && r.Prompt == "" &&
		r.AllowedActions == nil && r.Guardrails == nil && r.Mask == nil
}

// UnmarshalJSON reads the bare action or the object, strictly like the rest
// of the config: an unknown key in the object is refused.
func (r *RiskLevel) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if bytes.Equal(b, []byte("null")) {
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var action string
		if err := json.Unmarshal(b, &action); err != nil {
			return err
		}
		*r = RiskLevel{Action: action}
		return nil
	}
	type plain RiskLevel
	var out plain
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return fmt.Errorf("a risk level is an action or an object of action, approver, "+
			"approval_groups, prompt, allowed_actions, guardrails and mask: %w", err)
	}
	*r = RiskLevel(out)
	r.object = true
	return nil
}

// MarshalJSON writes the bare action when that says everything, so a block
// in the old form serves the bytes it always did.
func (r RiskLevel) MarshalJSON() ([]byte, error) {
	if !r.objectForm() {
		return json.Marshal(r.Action)
	}
	type plain RiskLevel
	return json.Marshal(plain(r))
}

// holds reports a level that holds the statement for approval.
func (r RiskLevel) holds() bool {
	return analyzer.Action(r.Action).Canonical() == analyzer.ActionRequireReview
}

// objectForm reports a level whose object form carries meaning: a field
// beside action, or a hold decoded from an object (its approver default).
func (r RiskLevel) objectForm() bool {
	return r.Approver != "" || len(r.ApprovalGroups) > 0 || r.Prompt != "" || r.AllowedActions != nil ||
		r.Guardrails != nil || r.Mask != nil || (r.object && r.holds())
}

// IsHandoff reports a level whose action is ai-handoff.
func (r RiskLevel) IsHandoff() bool {
	return analyzer.Action(r.Action) == analyzer.ActionHandoff
}

// handoffApprover is who decides on a hold the hand-off agent selects: ""
// when the level does not hand off or cannot select require_approval.
func (r RiskLevel) handoffApprover() analyzer.Approver {
	if !r.IsHandoff() {
		return ""
	}
	ap, ok := r.AllowedActions.RequireApproval()
	if !ok {
		return ""
	}
	return analyzer.Approver(ap.Approver)
}

// approver is who releases a held level: "" for the bare form, which files
// under the block's approval_rule, else human or ai_agent. An unknown value
// reads as ai_agent, the default; LaneAnalyzerWarnings says so.
func (r RiskLevel) approver() analyzer.Approver {
	if !r.holds() || !r.objectForm() {
		return ""
	}
	if analyzer.Approver(r.Approver) == analyzer.ApproverHuman {
		return analyzer.ApproverHuman
	}
	return analyzer.ApproverAIAgent
}

// canonical folds require_approval onto require_review and writes down the
// ai_agent default of an object that holds.
func (r RiskLevel) canonical() RiskLevel {
	r.Action = canonicalAction(r.Action)
	if r.object && r.holds() && r.Approver == "" {
		r.Approver = string(analyzer.ApproverAIAgent)
	}
	return r
}

// laneLevel is one risk level of a block, with the name an operator writes.
type laneLevel struct {
	name  string
	risk  analyzer.RiskLevel
	level *RiskLevel
}

// levels lists the block's three levels, high first.
func (la *LaneAnalyzerConfig) levels() []laneLevel {
	return []laneLevel{
		{"high", analyzer.RiskHigh, &la.HighRisk},
		{"medium", analyzer.RiskMedium, &la.MediumRisk},
		{"low", analyzer.RiskLow, &la.LowRisk},
	}
}

// analyzerHolds reports whether a lane's analyzer block can hold a statement
// for approval, by a human or by the approval agent: a level that holds, or
// an ai-handoff level whose agent can select require_approval.
func analyzerHolds(la *LaneAnalyzerConfig) bool {
	return la != nil && slices.ContainsFunc(la.levels(), func(l laneLevel) bool {
		return l.level.holds() || l.level.handoffApprover() != ""
	})
}

// humanHolds reports a block where some level can wait for a person: the
// bare form, an object with approver human, or an ai-handoff level whose
// require_approval is human. Only such a lane files a review that waits, so
// only it reads review_mode, spend_approval_on, match_ignore and needs
// somewhere to file.
func humanHolds(la *LaneAnalyzerConfig) bool {
	return la != nil && slices.ContainsFunc(la.levels(), func(l laneLevel) bool {
		return l.level.holds() && l.level.approver() != analyzer.ApproverAIAgent ||
			l.level.handoffApprover() == analyzer.ApproverHuman
	})
}

// ruleHolds reports a block where some level holds in the bare form, the
// only form the block's approval_rule serves.
func ruleHolds(la *LaneAnalyzerConfig) bool {
	return la != nil && slices.ContainsFunc(la.levels(), func(l laneLevel) bool {
		return l.level.holds() && l.level.approver() == ""
	})
}

// approvals maps each held object level onto its approver, for the
// evaluator.
func (la *LaneAnalyzerConfig) approvals() map[analyzer.RiskLevel]analyzer.Approval {
	var out map[analyzer.RiskLevel]analyzer.Approval
	for _, l := range la.levels() {
		ap := l.level.approver()
		if ap == "" {
			continue
		}
		if out == nil {
			out = map[analyzer.RiskLevel]analyzer.Approval{}
		}
		a := analyzer.Approval{Approver: ap}
		if ap == analyzer.ApproverAIAgent {
			a.Prompt = l.level.Prompt
		}
		out[l.risk] = a
	}
	return out
}

// handoffs maps each ai-handoff level onto its hand-off configuration, for
// the evaluator.
func (la *LaneAnalyzerConfig) handoffs() map[analyzer.RiskLevel]analyzer.Handoff {
	var out map[analyzer.RiskLevel]analyzer.Handoff
	for _, l := range la.levels() {
		r := l.level
		if !r.IsHandoff() {
			continue
		}
		h := analyzer.Handoff{Prompt: r.Prompt, Allowed: r.AllowedActions.Actions()}
		if ap, ok := r.AllowedActions.RequireApproval(); ok {
			h.Approval = analyzer.Approval{Approver: analyzer.Approver(ap.Approver)}
			if h.Approval.Approver == analyzer.ApproverAIAgent {
				h.Approval.Prompt = ap.Prompt
			}
		}
		if out == nil {
			out = map[analyzer.RiskLevel]analyzer.Handoff{}
		}
		out[l.risk] = h
	}
	return out
}

// validateLevelObjects refuses an object level the sidecar cannot act on.
func validateLevelObjects(la *LaneAnalyzerConfig, where string) []string {
	var problems []string
	for _, l := range la.levels() {
		r := l.level
		problems = append(problems, aiRuleModeProblems(where, l.name, *r)...)
		if r.objectForm() && r.Action == "" {
			problems = append(problems, fmt.Sprintf(
				"%s: %s risk is an object with no action; set action", where, l.name))
		}
		for i, g := range r.ApprovalGroups {
			if strings.TrimSpace(g) == "" {
				problems = append(problems, fmt.Sprintf(
					"%s: %s risk approval_groups[%d] is blank", where, l.name, i))
			}
		}
		if r.AllowedActions == nil {
			continue
		}
		for i, g := range r.AllowedActions.approval.ApprovalGroups {
			if strings.TrimSpace(g) == "" {
				problems = append(problems, fmt.Sprintf(
					"%s: %s risk allowed_actions.require_approval.approval_groups[%d] is blank", where, l.name, i))
			}
		}
	}
	return problems
}

// LaneAnalyzerWarnings lists what an analyzer block sets that does nothing.
// None of it is refused: the block still works, with the setting ignored.
// The sidecar shows them in -validate and logs them at start; exported so
// the control plane can show them at the save.
func LaneAnalyzerWarnings(la *LaneAnalyzerConfig) []string {
	if la == nil {
		return nil
	}
	var out []string
	for _, l := range la.levels() {
		r := *l.level
		out = append(out, aiRuleLevelWarnings(l.name, r)...)
		if r.IsHandoff() {
			out = append(out, handoffWarnings(l.name, r)...)
			continue
		}
		if r.AllowedActions != nil {
			out = append(out, fmt.Sprintf("analyzer block: %s risk sets allowed_actions, and its action %q "+
				"is not %s, so they are ignored", l.name, r.Action, analyzer.ActionHandoff))
		}
		if !r.holds() {
			if r.Approver != "" || len(r.ApprovalGroups) > 0 || r.Prompt != "" {
				out = append(out, fmt.Sprintf("analyzer block: %s risk sets approver, approval_groups "+
					"or prompt, and its action %q holds nothing, so they are ignored", l.name, r.Action))
			}
			continue
		}
		if r.Approver != "" && !analyzer.Approver(r.Approver).Valid() {
			out = append(out, fmt.Sprintf("analyzer block: %s risk approver %q is unknown and ignored; "+
				"the level uses the default approver %q", l.name, r.Approver, analyzer.ApproverAIAgent))
		}
		switch r.approver() {
		case analyzer.ApproverAIAgent:
			if len(r.ApprovalGroups) > 0 {
				out = append(out, fmt.Sprintf("analyzer block: %s risk approval_groups name approvers "+
					"nobody consults: the level's approver is %q", l.name, analyzer.ApproverAIAgent))
			}
		case analyzer.ApproverHuman:
			if r.Prompt != "" {
				out = append(out, fmt.Sprintf("analyzer block: %s risk prompt is ignored: it instructs "+
					"the approval agent, and the level's approver is %q", l.name, analyzer.ApproverHuman))
			}
		}
	}
	if la.ApprovalRule != "" && analyzerHolds(la) && !ruleHolds(la) {
		out = append(out, fmt.Sprintf("analyzer block: approval_rule %q is ignored: every level that "+
			"holds names its own approver", la.ApprovalRule))
	}
	return out
}

// handoffWarnings lists what an ai-handoff level sets that does nothing.
func handoffWarnings(name string, r RiskLevel) []string {
	var out []string
	if r.Approver != "" || len(r.ApprovalGroups) > 0 {
		out = append(out, fmt.Sprintf("analyzer block: %s risk sets approver or approval_groups beside "+
			"action %s, so they are ignored; write them under allowed_actions.require_approval",
			name, analyzer.ActionHandoff))
	}
	a := r.AllowedActions
	if a == nil {
		return out
	}
	for _, v := range a.values {
		switch c := analyzer.Action(v).Canonical(); {
		case c == analyzer.ActionHandoff:
			out = append(out, fmt.Sprintf("analyzer block: %s risk allowed_actions value %q is ignored: "+
				"the hand-off agent cannot hand off again", name, v))
		case !handoffSelectable(c):
			out = append(out, fmt.Sprintf("analyzer block: %s risk allowed_actions value %q is unknown and "+
				"ignored (allow, warn, block or require_approval)", name, v))
		}
	}
	if len(a.values) == 0 {
		out = append(out, fmt.Sprintf("analyzer block: %s risk allowed_actions is empty, so the level "+
			"falls back to block", name))
	} else if !slices.ContainsFunc(a.values, func(v string) bool { return handoffSelectable(analyzer.Action(v).Canonical()) }) {
		out = append(out, fmt.Sprintf("analyzer block: %s risk allowed_actions names no action the "+
			"hand-off agent can select, so the level falls back to block", name))
	}
	for _, v := range a.configured {
		out = append(out, fmt.Sprintf("analyzer block: %s risk allowed_actions.%s carries a configuration, "+
			"which is ignored: only require_approval takes one", name, v))
	}
	ap := a.approval
	if !slices.ContainsFunc(a.values, func(v string) bool {
		return analyzer.Action(v).Canonical() == analyzer.ActionRequireReview
	}) {
		return out
	}
	if ap.Approver != "" && !analyzer.Approver(ap.Approver).Valid() {
		out = append(out, fmt.Sprintf("analyzer block: %s risk allowed_actions.require_approval approver %q "+
			"is unknown and ignored; it uses the default approver %q", name, ap.Approver, analyzer.ApproverAIAgent))
	}
	if analyzer.Approver(ap.Approver) == analyzer.ApproverHuman {
		if ap.Prompt != "" {
			out = append(out, fmt.Sprintf("analyzer block: %s risk allowed_actions.require_approval prompt "+
				"is ignored: it instructs the approval agent, and the approver is %q", name, analyzer.ApproverHuman))
		}
	} else if len(ap.ApprovalGroups) > 0 {
		out = append(out, fmt.Sprintf("analyzer block: %s risk allowed_actions.require_approval "+
			"approval_groups name approvers nobody consults: the approver is %q", name, analyzer.ApproverAIAgent))
	}
	return out
}

// DowngradeRiskLevels serves cfg to a sidecar that predates the object form
// of a risk level (CapabilityRiskLevelObject). It returns cfg unchanged for a
// build that has it, and never changes cfg itself.
//
// For an older build, a non-holding object becomes its bare action, and a
// human hold becomes a bare require_review with the block's approval_rule set
// to approvalRule(listener): the control plane's rule that carries the same
// groups. It refuses, naming the capability, a block with an ai_agent level,
// with human levels that name different groups, with a bare hold whose
// approval_rule differs from that rule, or when approvalRule returns "".
//
// An ai-handoff level, or allowed_actions on any level, has no downgrade:
// for a build without CapabilityAIHandoff it refuses, naming that
// capability. AI rule options and rule_library have no downgrade either:
// for a build without CapabilityAIRules it refuses, naming that capability.
func DowngradeRiskLevels(cfg Config, hs Handshake, approvalRule func(listener string) string) (Config, error) {
	since := capabilitySince()
	if err := refuseAIRules(cfg, hs, since); err != nil {
		return Config{}, err
	}
	if !hs.grants(CapabilityAIHandoff, since) {
		var problems []string
		for _, l := range cfg.Listeners {
			if l.Analyzer == nil {
				continue
			}
			for _, lv := range l.Analyzer.levels() {
				if lv.level.IsHandoff() || lv.level.AllowedActions != nil {
					problems = append(problems, fmt.Sprintf("listener %q: %s risk hands off to the AI agent "+
						"(action %s or allowed_actions)", l.Name, lv.name, analyzer.ActionHandoff))
				}
			}
		}
		if len(problems) > 0 {
			return Config{}, fmt.Errorf("%s; this sidecar does not report %q, upgrade it to a release that does",
				strings.Join(problems, "; "), CapabilityAIHandoff)
		}
	}
	if hs.grants(CapabilityRiskLevelObject, since) {
		return cfg, nil
	}
	var problems []string
	var listeners []ListenerConfig
	for i, l := range cfg.Listeners {
		if l.Analyzer == nil {
			continue
		}
		block, changed, err := downgradeBlock(*l.Analyzer, l.Name, approvalRule)
		if err != nil {
			problems = append(problems, fmt.Sprintf("listener %q: %v", l.Name, err))
			continue
		}
		if !changed {
			continue
		}
		if listeners == nil {
			listeners = slices.Clone(cfg.Listeners)
		}
		listeners[i].Analyzer = &block
	}
	if len(problems) > 0 {
		return Config{}, fmt.Errorf("%s; this sidecar does not report %q, upgrade it to a release that does",
			strings.Join(problems, "; "), CapabilityRiskLevelObject)
	}
	if listeners != nil {
		cfg.Listeners = listeners
	}
	return cfg, nil
}

// downgradeBlock is DowngradeRiskLevels for one block, on a copy.
func downgradeBlock(block LaneAnalyzerConfig, listener string,
	approvalRule func(string) string) (LaneAnalyzerConfig, bool, error) {
	changed, human := false, false
	// A bare hold files under the block's own approval_rule, so one rule
	// must then serve both forms.
	bareHold := ruleHolds(&block)
	var groups []string
	for _, l := range block.levels() {
		r := l.level.canonical()
		if !r.objectForm() {
			continue
		}
		changed = true
		switch r.approver() {
		case "":
			*l.level = RiskLevel{Action: r.Action}
			continue
		case analyzer.ApproverAIAgent:
			return block, false, fmt.Errorf("%s risk asks the %s approver", l.name, analyzer.ApproverAIAgent)
		}
		g := slices.Sorted(slices.Values(r.ApprovalGroups))
		if human && !slices.Equal(g, groups) {
			return block, false, errors.New("the human levels name different approval_groups")
		}
		human, groups = true, g
		*l.level = RiskLevel{Action: r.Action}
	}
	if !human {
		return block, changed, nil
	}
	rule := ""
	if approvalRule != nil {
		rule = approvalRule(listener)
	}
	if rule == "" {
		return block, false, errors.New("no approval rule carries the human levels' approval_groups")
	}
	if bareHold && block.ApprovalRule != "" && block.ApprovalRule != rule {
		return block, false, fmt.Errorf("approval_rule %q and the human levels' approval_groups "+
			"name different approvers", block.ApprovalRule)
	}
	block.ApprovalRule = rule
	return block, true, nil
}

// HandoffApproval configures require_approval inside allowed_actions: the
// fields of a held level, minus action. The approver defaults to ai_agent.
type HandoffApproval struct {
	Approver       string   `json:"approver,omitempty"`
	ApprovalGroups []string `json:"approval_groups,omitempty"`
	Prompt         string   `json:"prompt,omitempty"`
}

func (h HandoffApproval) isZero() bool {
	return h.Approver == "" && len(h.ApprovalGroups) == 0 && h.Prompt == ""
}

// AllowedActions are what the hand-off agent of an ai-handoff level may
// select. It reads a list ([allow, warn]) or an object keyed by action
// ({require_approval: {approver: human}, warn: {}}). Only require_approval
// takes a configuration; require_review is its alias. It writes the list
// form unless require_approval carries a configuration.
type AllowedActions struct {
	// values are the actions as written, unknown ones included, so a
	// round trip keeps what the warnings name.
	values []string

	// configured are the allow, warn or block keys written with a
	// configuration, which is ignored.
	configured []string

	approval HandoffApproval
}

// NewAllowedActions builds allowed_actions from its values and the
// require_approval configuration. A configuration adds require_approval
// when values do not name it.
func NewAllowedActions(values []string, approval HandoffApproval) *AllowedActions {
	approval.ApprovalGroups = slices.Clone(approval.ApprovalGroups)
	a := &AllowedActions{values: slices.Clone(values), approval: approval}
	if !approval.isZero() && !a.names(analyzer.ActionRequireReview) {
		a.values = append(a.values, string(analyzer.ActionRequireApproval))
	}
	return a
}

// names reports whether the values name the canonical action c.
func (a *AllowedActions) names(c analyzer.Action) bool {
	return slices.ContainsFunc(a.values, func(v string) bool { return analyzer.Action(v).Canonical() == c })
}

// handoffSelectable reports a canonical action the hand-off agent can select.
func handoffSelectable(c analyzer.Action) bool {
	switch c {
	case analyzer.ActionAllow, analyzer.ActionWarn, analyzer.ActionBlock, analyzer.ActionRequireReview:
		return true
	}
	return false
}

// Actions lists what the hand-off agent may select, canonical, in the order
// allow, warn, block, require_review. Nil allows all four. A set that names
// none of them is block alone, the fallback LaneAnalyzerWarnings reports.
func (a *AllowedActions) Actions() []analyzer.Action {
	all := []analyzer.Action{analyzer.ActionAllow, analyzer.ActionWarn, analyzer.ActionBlock,
		analyzer.ActionRequireReview}
	if a == nil {
		return all
	}
	var out []analyzer.Action
	for _, c := range all {
		if a.names(c) {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return []analyzer.Action{analyzer.ActionBlock}
	}
	return out
}

// RequireApproval returns who decides on a hold the hand-off agent selects.
// The approver is human or ai_agent: empty and unknown values read as
// ai_agent, the default. ok is false when the agent cannot select
// require_approval.
func (a *AllowedActions) RequireApproval() (HandoffApproval, bool) {
	if !slices.Contains(a.Actions(), analyzer.ActionRequireReview) {
		return HandoffApproval{}, false
	}
	var ap HandoffApproval
	if a != nil {
		ap = a.approval
		ap.ApprovalGroups = slices.Clone(ap.ApprovalGroups)
	}
	if analyzer.Approver(ap.Approver) != analyzer.ApproverHuman {
		ap.Approver = string(analyzer.ApproverAIAgent)
	}
	return ap, true
}

// UnmarshalJSON reads the list or the object form. The configuration of
// require_approval is decoded strictly, like the rest of the config.
func (a *AllowedActions) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	var out AllowedActions
	switch {
	case len(b) > 0 && b[0] == '[':
		if err := json.Unmarshal(b, &out.values); err != nil {
			return fmt.Errorf("allowed_actions is a list of action names: %w", err)
		}
	case len(b) > 0 && b[0] == '{':
		var err error
		if out, err = decodeAllowedObject(b); err != nil {
			return err
		}
	default:
		return errors.New("allowed_actions is a list of actions or an object keyed by action")
	}
	if out.values == nil {
		out.values = []string{}
	}
	*a = out
	return nil
}

// decodeAllowedObject reads the object form, keeping the key order.
func decodeAllowedObject(b []byte) (AllowedActions, error) {
	var out AllowedActions
	dec := json.NewDecoder(bytes.NewReader(b))
	if _, err := dec.Token(); err != nil {
		return out, fmt.Errorf("allowed_actions: %w", err)
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return out, fmt.Errorf("allowed_actions: %w", err)
		}
		key, _ := tok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return out, fmt.Errorf("allowed_actions.%s: %w", key, err)
		}
		if slices.Contains(out.values, key) {
			return out, fmt.Errorf("allowed_actions names %q twice", key)
		}
		out.values = append(out.values, key)
		empty, isObject := emptyConfig(raw)
		switch c := analyzer.Action(key).Canonical(); {
		case c == analyzer.ActionRequireReview:
			if empty {
				continue
			}
			if !isObject {
				return out, fmt.Errorf("allowed_actions.%s is an object of approver, approval_groups and prompt", key)
			}
			var ap HandoffApproval
			d := json.NewDecoder(bytes.NewReader(raw))
			d.DisallowUnknownFields()
			if err := d.Decode(&ap); err != nil {
				return out, fmt.Errorf("allowed_actions.%s is an object of approver, approval_groups "+
					"and prompt: %w", key, err)
			}
			if !out.approval.isZero() && !ap.isZero() {
				return out, errors.New("allowed_actions configures require_approval twice " +
					"(require_approval and require_review)")
			}
			if !ap.isZero() {
				out.approval = ap
			}
		case handoffSelectable(c) && !empty:
			out.configured = append(out.configured, key)
		}
	}
	if _, err := dec.Token(); err != nil {
		return out, fmt.Errorf("allowed_actions: %w", err)
	}
	return out, nil
}

// emptyConfig reports a value that configures nothing (null or {}), and
// whether it is an object.
func emptyConfig(raw json.RawMessage) (empty, isObject bool) {
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("null")) {
		return true, false
	}
	if len(raw) == 0 || raw[0] != '{' {
		return false, false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return false, true
	}
	return len(m) == 0, true
}

// MarshalJSON writes the list form, or the object form when require_approval
// carries a configuration. An ignored configuration is not written.
func (a AllowedActions) MarshalJSON() ([]byte, error) {
	if a.approval.isZero() {
		values := a.values
		if values == nil {
			values = []string{}
		}
		return json.Marshal(values)
	}
	cfg, err := json.Marshal(a.approval)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	written := false
	for i, v := range a.values {
		if i > 0 {
			buf.WriteByte(',')
		}
		k, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		buf.Write(k)
		buf.WriteByte(':')
		if !written && analyzer.Action(v).Canonical() == analyzer.ActionRequireReview {
			buf.Write(cfg)
			written = true
			continue
		}
		buf.WriteString("{}")
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

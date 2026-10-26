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
	// Action is allow, warn, block, defer or require_review (also spelled
	// require_approval).
	Action string `json:"action,omitempty"`

	// Approver is human or ai_agent, for a level that holds.
	Approver string `json:"approver,omitempty" cap:"analyzer_risk_level_object"`

	// ApprovalGroups are who may approve a human level. Empty means the
	// administrators. The control plane reads them; the sidecar files the
	// level and never the groups.
	ApprovalGroups []string `json:"approval_groups,omitempty" cap:"analyzer_risk_level_object"`

	// Prompt replaces the approval agent's default instruction on an
	// ai_agent level. The fixed suffix stays.
	Prompt string `json:"prompt,omitempty" cap:"analyzer_risk_level_object"`

	// object records that the level was decoded from an object, so an
	// object that holds and names no approver keeps the ai_agent default
	// until foldApprovalSpellings writes it down.
	object bool
}

// IsZero reports an unset level, for omitzero.
func (r RiskLevel) IsZero() bool {
	return r.Action == "" && r.Approver == "" && len(r.ApprovalGroups) == 0 && r.Prompt == ""
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
			"approval_groups and prompt: %w", err)
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
	return r.Approver != "" || len(r.ApprovalGroups) > 0 || r.Prompt != "" || (r.object && r.holds())
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

// analyzerHolds reports whether a lane's analyzer block holds any risk level
// for approval, by a human or by the approval agent.
func analyzerHolds(la *LaneAnalyzerConfig) bool {
	return la != nil && slices.ContainsFunc(la.levels(), func(l laneLevel) bool { return l.level.holds() })
}

// humanHolds reports a block where some level waits for a person: the bare
// form, or an object with approver human. Only such a lane files a review
// that waits, so only it reads review_mode, spend_approval_on, match_ignore
// and needs somewhere to file.
func humanHolds(la *LaneAnalyzerConfig) bool {
	return la != nil && slices.ContainsFunc(la.levels(), func(l laneLevel) bool {
		return l.level.holds() && l.level.approver() != analyzer.ApproverAIAgent
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

// validateLevelObjects refuses an object level the sidecar cannot act on.
func validateLevelObjects(la *LaneAnalyzerConfig, where string) []string {
	var problems []string
	for _, l := range la.levels() {
		r := l.level
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
func DowngradeRiskLevels(cfg Config, hs Handshake, approvalRule func(listener string) string) (Config, error) {
	if hs.grants(CapabilityRiskLevelObject, capabilitySince()) {
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

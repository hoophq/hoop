package daemon

import (
	"fmt"
	"slices"
	"strings"

	"github.com/hoophq/hoop/sidecar/analyzer"
)

// CapabilityLevelApprovers means this build reads analyzer.approvers. The cap
// tag on LaneAnalyzerConfig.Approvers puts it in the header;
// ServeLevelApprovers serves a build without it.
const CapabilityLevelApprovers = "analyzer_level_approvers"

// LevelApprovers names who releases each held risk level of an analyzer
// block. A held level with no entry files under the block's approval_rule.
type LevelApprovers struct {
	High   *LevelApprover `json:"high,omitempty"`
	Medium *LevelApprover `json:"medium,omitempty"`
	Low    *LevelApprover `json:"low,omitempty"`
}

// LevelApprover is the approver of one held level.
type LevelApprover struct {
	// Type is human or ai_agent. Required.
	Type analyzer.Approver `json:"type,omitempty"`

	// Groups are who may approve a human level. Empty means the
	// administrators. The control plane reads them; the sidecar files the
	// level and never the groups.
	Groups []string `json:"groups,omitempty"`

	// Prompt replaces the approval agent's default guidance on an ai_agent
	// level. The fixed contract stays.
	Prompt string `json:"prompt,omitempty"`
}

// LevelApprover returns the approver the block names for level, or nil.
func (la *LaneAnalyzerConfig) LevelApprover(level analyzer.RiskLevel) *LevelApprover {
	if la == nil || la.Approvers == nil {
		return nil
	}
	switch level {
	case analyzer.RiskHigh:
		return la.Approvers.High
	case analyzer.RiskMedium:
		return la.Approvers.Medium
	case analyzer.RiskLow:
		return la.Approvers.Low
	}
	return nil
}

// isZero reports a block with no level set.
func (a *LevelApprovers) isZero() bool {
	return a == nil || a.High == nil && a.Medium == nil && a.Low == nil
}

// laneLevel is one risk level of a block, with the name an operator writes.
type laneLevel struct {
	name   string
	risk   analyzer.RiskLevel
	action *string
}

// levels lists the block's three levels, high first.
func (la *LaneAnalyzerConfig) levels() []laneLevel {
	return []laneLevel{
		{"high", analyzer.RiskHigh, &la.HighRisk},
		{"medium", analyzer.RiskMedium, &la.MediumRisk},
		{"low", analyzer.RiskLow, &la.LowRisk},
	}
}

// holdsAction reports an action that holds the statement for approval.
func holdsAction(action string) bool {
	return analyzer.Action(action).Canonical() == analyzer.ActionRequireReview
}

// heldApprover is who releases a held level: "" for a level with no
// approvers entry, which files under the block's approval_rule. A level that
// does not hold has no approver.
func (la *LaneAnalyzerConfig) heldApprover(l laneLevel) analyzer.Approver {
	if !holdsAction(*l.action) {
		return ""
	}
	if a := la.LevelApprover(l.risk); a != nil {
		return a.Type
	}
	return ""
}

// analyzerHolds reports whether a lane's analyzer block holds any risk level
// for approval, by a human or by the approval agent.
func analyzerHolds(la *LaneAnalyzerConfig) bool {
	return la != nil && slices.ContainsFunc(la.levels(), func(l laneLevel) bool { return holdsAction(*l.action) })
}

// humanHolds reports a block where some level waits for a person: a level
// with no approvers entry, or one whose approver is human. Only such a lane
// files a review that waits, so only it reads review_mode,
// spend_approval_on, match_ignore and needs somewhere to file.
func humanHolds(la *LaneAnalyzerConfig) bool {
	return la != nil && slices.ContainsFunc(la.levels(), func(l laneLevel) bool {
		return holdsAction(*l.action) && la.heldApprover(l) != analyzer.ApproverAIAgent
	})
}

// ruleHolds reports a block where some held level has no approvers entry:
// the only level the block's approval_rule serves.
func ruleHolds(la *LaneAnalyzerConfig) bool {
	return la != nil && slices.ContainsFunc(la.levels(), func(l laneLevel) bool {
		return holdsAction(*l.action) && la.LevelApprover(l.risk) == nil
	})
}

// approvals maps each held level with an approvers entry onto its approver,
// for the evaluator.
func (la *LaneAnalyzerConfig) approvals() map[analyzer.RiskLevel]analyzer.Approval {
	var out map[analyzer.RiskLevel]analyzer.Approval
	for _, l := range la.levels() {
		ap := la.heldApprover(l)
		if ap == "" {
			continue
		}
		if out == nil {
			out = map[analyzer.RiskLevel]analyzer.Approval{}
		}
		a := analyzer.Approval{Approver: ap}
		if ap == analyzer.ApproverAIAgent {
			a.Prompt = la.LevelApprover(l.risk).Prompt
		}
		out[l.risk] = a
	}
	return out
}

// validateLevelApprovers refuses an approvers entry the sidecar cannot act
// on, or one that would be read by nobody.
func validateLevelApprovers(la *LaneAnalyzerConfig, where string) []string {
	var problems []string
	for _, l := range la.levels() {
		a := la.LevelApprover(l.risk)
		if a == nil {
			continue
		}
		at := fmt.Sprintf("%s: approvers.%s", where, l.name)
		if !holdsAction(*l.action) {
			problems = append(problems, fmt.Sprintf("%s names an approver, and the %s risk action %q "+
				"holds nothing; set %s: %s or remove the entry", at, l.name, *l.action, l.name,
				analyzer.ActionRequireApproval))
		}
		switch a.Type {
		case analyzer.ApproverHuman:
			if a.Prompt != "" {
				problems = append(problems, fmt.Sprintf("%s: prompt instructs the approval agent, "+
					"and the type is %q", at, analyzer.ApproverHuman))
			}
		case analyzer.ApproverAIAgent:
			if len(a.Groups) > 0 {
				problems = append(problems, fmt.Sprintf("%s: groups name people nobody asks, "+
					"and the type is %q", at, analyzer.ApproverAIAgent))
			}
		case "":
			problems = append(problems, fmt.Sprintf("%s: type is required (%s or %s)",
				at, analyzer.ApproverHuman, analyzer.ApproverAIAgent))
		default:
			problems = append(problems, fmt.Sprintf("%s: unknown type %q (%s or %s)",
				at, a.Type, analyzer.ApproverHuman, analyzer.ApproverAIAgent))
		}
		for i, g := range a.Groups {
			if strings.TrimSpace(g) == "" {
				problems = append(problems, fmt.Sprintf("%s: groups[%d] is blank", at, i))
			}
		}
	}
	return problems
}

// ServeLevelApprovers returns cfg for hs: unchanged when hs has the cap; else
// a copy with approvers dropped from every block that names an
// approval_rule. A block that sets approvers and names no approval_rule keeps
// them, so CheckServable refuses it naming the capability. cfg is never
// changed.
//
// On an old build a dropped ai_agent level becomes a human hold under
// approval_rule: more oversight, never less.
func ServeLevelApprovers(cfg Config, hs Handshake) Config {
	if hs.grants(CapabilityLevelApprovers, capabilitySince()) {
		return cfg
	}
	var listeners []ListenerConfig
	for i, l := range cfg.Listeners {
		if l.Analyzer == nil || l.Analyzer.Approvers == nil || l.Analyzer.ApprovalRule == "" {
			continue
		}
		if listeners == nil {
			listeners = slices.Clone(cfg.Listeners)
		}
		block := *l.Analyzer
		block.Approvers = nil
		listeners[i].Analyzer = &block
	}
	if listeners != nil {
		cfg.Listeners = listeners
	}
	return cfg
}

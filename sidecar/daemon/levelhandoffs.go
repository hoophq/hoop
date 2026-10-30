package daemon

import (
	"fmt"
	"slices"

	"github.com/hoophq/hoop/sidecar/analyzer"
)

// CapabilityAIHandoff means this build runs a risk level whose action is
// ai-handoff and reads analyzer.handoffs. The cap tag on
// LaneAnalyzerConfig.Handoffs puts it in the header. Every ai-handoff level
// needs a handoffs entry, so a build without the capability is refused the
// document. No downgrade exists: an older build has no action that lets the
// AI agent select one.
const CapabilityAIHandoff = "analyzer_ai_handoff"

// LevelHandoffs configures each ai-handoff risk level of an analyzer block.
type LevelHandoffs struct {
	High   *LevelHandoff `json:"high,omitempty"`
	Medium *LevelHandoff `json:"medium,omitempty"`
	Low    *LevelHandoff `json:"low,omitempty"`
}

// LevelHandoff is the hand-off agent of one ai-handoff level. Who decides a
// require_approval the agent selects is approvers.<level>; a level with no
// entry files under the block's approval_rule.
type LevelHandoff struct {
	// AllowedActions are the actions the agent may select: allow, warn,
	// block and require_approval (also spelled require_review). Required.
	AllowedActions []string `json:"allowed_actions,omitempty"`

	// Prompt replaces the hand-off agent's default instruction. The fixed
	// contract stays.
	Prompt string `json:"prompt,omitempty"`
}

// LevelHandoff returns the hand-off the block configures for level, or nil.
func (la *LaneAnalyzerConfig) LevelHandoff(level analyzer.RiskLevel) *LevelHandoff {
	if la == nil || la.Handoffs == nil {
		return nil
	}
	switch level {
	case analyzer.RiskHigh:
		return la.Handoffs.High
	case analyzer.RiskMedium:
		return la.Handoffs.Medium
	case analyzer.RiskLow:
		return la.Handoffs.Low
	}
	return nil
}

// isZero reports a block with no level set.
func (h *LevelHandoffs) isZero() bool {
	return h == nil || h.High == nil && h.Medium == nil && h.Low == nil
}

// handsOff reports an action that hands the decision to the hand-off agent.
func handsOff(action string) bool {
	return analyzer.Action(action) == analyzer.ActionHandoff
}

// handoffSelectable reports a canonical action the hand-off agent can select.
func handoffSelectable(c analyzer.Action) bool {
	switch c {
	case analyzer.ActionAllow, analyzer.ActionWarn, analyzer.ActionBlock, analyzer.ActionRequireReview:
		return true
	}
	return false
}

// actions lists the allowed actions in their canonical spelling, each once.
// Validation refuses the unknown ones; here they are skipped.
func (h *LevelHandoff) actions() []analyzer.Action {
	var out []analyzer.Action
	for _, v := range h.AllowedActions {
		c := analyzer.Action(v).Canonical()
		if handoffSelectable(c) && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

// handoffHolds reports an ai-handoff level whose agent can select
// require_approval. Such a level holds like a level whose action holds: its
// approver is approvers.<level>, or approval_rule when it has none.
func (la *LaneAnalyzerConfig) handoffHolds(l laneLevel) bool {
	if !handsOff(*l.action) {
		return false
	}
	h := la.LevelHandoff(l.risk)
	return h != nil && slices.Contains(h.actions(), analyzer.ActionRequireReview)
}

// handoffs maps each ai-handoff level onto its hand-off, for the evaluator.
func (la *LaneAnalyzerConfig) handoffs() map[analyzer.RiskLevel]analyzer.Handoff {
	var out map[analyzer.RiskLevel]analyzer.Handoff
	for _, l := range la.levels() {
		h := la.LevelHandoff(l.risk)
		if !handsOff(*l.action) || h == nil {
			continue
		}
		hd := analyzer.Handoff{Prompt: h.Prompt, Allowed: h.actions()}
		if a := la.LevelApprover(l.risk); a != nil && la.handoffHolds(l) {
			hd.Approval = analyzer.Approval{Approver: a.Type}
			if a.Type == analyzer.ApproverAIAgent {
				hd.Approval.Prompt = a.Prompt
			}
		}
		if out == nil {
			out = map[analyzer.RiskLevel]analyzer.Handoff{}
		}
		out[l.risk] = hd
	}
	return out
}

// validateLevelHandoffs refuses a hand-off the sidecar cannot act on: an
// ai-handoff level with no allowed actions, an action the agent cannot
// select, one named twice, and an entry on a level that does not hand off.
func validateLevelHandoffs(la *LaneAnalyzerConfig, where string) []string {
	var problems []string
	for _, l := range la.levels() {
		h := la.LevelHandoff(l.risk)
		at := fmt.Sprintf("%s: handoffs.%s", where, l.name)
		if !handsOff(*l.action) {
			if h != nil {
				problems = append(problems, fmt.Sprintf("%s configures a hand-off, and the %s risk action "+
					"%q is not %s; set %s: %s or remove the entry", at, l.name, *l.action,
					analyzer.ActionHandoff, l.name, analyzer.ActionHandoff))
			}
			continue
		}
		if h == nil {
			problems = append(problems, fmt.Sprintf("%s: %s risk is %s and %s is missing; it lists the "+
				"allowed_actions the hand-off agent may select", where, l.name, analyzer.ActionHandoff, at))
			continue
		}
		if len(h.AllowedActions) == 0 {
			problems = append(problems, fmt.Sprintf("%s: allowed_actions is empty; list the actions the "+
				"hand-off agent may select (allow, warn, block or require_approval)", at))
		}
		var seen []analyzer.Action
		for i, v := range h.AllowedActions {
			c := analyzer.Action(v).Canonical()
			switch {
			case !handoffSelectable(c):
				problems = append(problems, fmt.Sprintf("%s: allowed_actions[%d] %q is not an action the "+
					"hand-off agent can select (allow, warn, block or require_approval)", at, i, v))
			case slices.Contains(seen, c):
				problems = append(problems, fmt.Sprintf("%s: allowed_actions[%d] %q names %s twice",
					at, i, v, analyzer.HandoffToolName(c)))
			default:
				seen = append(seen, c)
			}
		}
	}
	return problems
}

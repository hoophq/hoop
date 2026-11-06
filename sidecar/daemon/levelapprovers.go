package daemon

import (
	"fmt"
	"maps"
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

	// Routing lets an AI agent on the control plane select the groups and
	// the people and channels to notify for a human level (ENG-663). It is
	// the alternative to Groups, never beside it. The control plane reads
	// it; the sidecar files the level and never the routing.
	Routing *ApprovalRouting `json:"routing,omitempty" cap:"analyzer_approval_routing"`
}

// CapabilityApprovalRouting means this build decodes approvers.<level>.routing.
// No downgrade exists: static groups would page other people.
const CapabilityApprovalRouting = "analyzer_approval_routing"

// ApprovalRouting is the AI routing of a human level's approval request.
type ApprovalRouting struct {
	// Candidates are the groups the routing agent selects from. Empty
	// means every group.
	Candidates []string `json:"candidates,omitempty"`

	// Notify maps an integration (slack, teams) onto how it notifies. An
	// integration the org has not set up is ignored. Absent keeps today's
	// notifications. Other keys are kept so -validate can name them.
	Notify map[string]NotifyRoute `json:"notify,omitempty"`
}

// The integrations routing notifies through, and the deliveries of each.
const (
	NotifySlack = "slack"
	NotifyTeams = "teams"

	DeliveryDM      = "dm"
	DeliveryChannel = "channel"
	DeliveryBoth    = "both"
)

// NotifyRoute is how one integration notifies the routed approvers.
type NotifyRoute struct {
	// Delivery is dm, channel or both. Empty means dm.
	Delivery string `json:"delivery,omitempty"`

	// ChannelsCandidates are the channels the routing agent selects from
	// for channel and both. Empty means every channel the integration
	// reaches.
	ChannelsCandidates []string `json:"channels_candidates,omitempty"`
}

// EffectiveDelivery is the delivery the control plane uses: Delivery when it
// is dm, channel or both, else dm. -validate notes the else.
func (n NotifyRoute) EffectiveDelivery() string {
	switch n.Delivery {
	case DeliveryChannel, DeliveryBoth:
		return n.Delivery
	}
	return DeliveryDM
}

// routes reports a block where some level sets routing.
func (a *LevelApprovers) routes() bool {
	return a != nil && slices.ContainsFunc([]*LevelApprover{a.High, a.Medium, a.Low},
		func(l *LevelApprover) bool { return l != nil && l.Routing != nil })
}

// routedLevels lists the held levels whose human approver sets routing:
// the levels whose filing sends the principal.
func (la *LaneAnalyzerConfig) routedLevels() map[analyzer.RiskLevel]bool {
	var out map[analyzer.RiskLevel]bool
	for _, l := range la.levels() {
		a := la.LevelApprover(l.risk)
		if a == nil || a.Routing == nil || a.Type != analyzer.ApproverHuman || !la.levelHolds(l) {
			continue
		}
		if out == nil {
			out = map[analyzer.RiskLevel]bool{}
		}
		out[l.risk] = true
	}
	return out
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

// levelHolds reports a level that can hold a statement for approval: its
// action holds, or its hand-off agent can select require_approval.
func (la *LaneAnalyzerConfig) levelHolds(l laneLevel) bool {
	return holdsAction(*l.action) || la.handoffHolds(l)
}

// heldApprover is who releases a held level: "" for a level with no
// approvers entry, which files under the block's approval_rule. A level that
// does not hold has no approver.
func (la *LaneAnalyzerConfig) heldApprover(l laneLevel) analyzer.Approver {
	if !la.levelHolds(l) {
		return ""
	}
	if a := la.LevelApprover(l.risk); a != nil {
		return a.Type
	}
	return ""
}

// analyzerHolds reports whether a lane's analyzer block can hold a statement
// for approval, by a human or by the approval agent.
func analyzerHolds(la *LaneAnalyzerConfig) bool {
	return la != nil && slices.ContainsFunc(la.levels(), la.levelHolds)
}

// humanHolds reports a block where some level can wait for a person: a
// level with no approvers entry, or one whose approver is human. Only such a
// lane files a review that waits, so only it reads review_mode,
// spend_approval_on, match_ignore and needs somewhere to file.
func humanHolds(la *LaneAnalyzerConfig) bool {
	return la != nil && slices.ContainsFunc(la.levels(), func(l laneLevel) bool {
		return la.levelHolds(l) && la.heldApprover(l) != analyzer.ApproverAIAgent
	})
}

// ruleHolds reports a block where some held level has no approvers entry:
// the only level the block's approval_rule serves.
func ruleHolds(la *LaneAnalyzerConfig) bool {
	return la != nil && slices.ContainsFunc(la.levels(), func(l laneLevel) bool {
		return la.levelHolds(l) && la.LevelApprover(l.risk) == nil
	})
}

// approvals maps each level whose action holds and that has an approvers
// entry onto its approver, for the evaluator. A hand-off level carries its
// approver in handoffs.
func (la *LaneAnalyzerConfig) approvals() map[analyzer.RiskLevel]analyzer.Approval {
	var out map[analyzer.RiskLevel]analyzer.Approval
	for _, l := range la.levels() {
		ap := la.heldApprover(l)
		if ap == "" || !holdsAction(*l.action) {
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
		switch {
		case handsOff(*l.action) && !la.handoffHolds(l):
			problems = append(problems, fmt.Sprintf("%s names an approver, and the %s risk hand-off "+
				"agent cannot select %s; add it to handoffs.%s.allowed_actions or remove the entry",
				at, l.name, analyzer.ActionRequireApproval, l.name))
		case !la.levelHolds(l):
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
		problems = append(problems, validateRouting(a, at)...)
	}
	return problems
}

// validateRouting refuses routing beside groups, and a blank candidate.
func validateRouting(a *LevelApprover, at string) []string {
	r := a.Routing
	if r == nil {
		return nil
	}
	var problems []string
	if len(a.Groups) > 0 {
		problems = append(problems, fmt.Sprintf("%s: groups and routing are alternatives; "+
			"set groups for static approvers or routing for AI routing, not both", at))
	}
	for i, g := range r.Candidates {
		if strings.TrimSpace(g) == "" {
			problems = append(problems, fmt.Sprintf("%s: routing.candidates[%d] is blank", at, i))
		}
	}
	for _, k := range slices.Sorted(maps.Keys(r.Notify)) {
		for i, c := range r.Notify[k].ChannelsCandidates {
			if strings.TrimSpace(c) == "" {
				problems = append(problems, fmt.Sprintf("%s: routing.notify.%s.channels_candidates[%d] is blank",
					at, k, i))
			}
		}
	}
	return problems
}

// LaneApproverWarnings lists the routing settings that the control plane
// ignores or reads as another value. None is refused. Exported so the control
// plane can show them at the save.
func LaneApproverWarnings(la *LaneAnalyzerConfig) []string {
	if la == nil {
		return nil
	}
	var out []string
	for _, l := range la.levels() {
		a := la.LevelApprover(l.risk)
		if a == nil || a.Routing == nil {
			continue
		}
		at := fmt.Sprintf("approvers.%s.routing", l.name)
		if a.Type == analyzer.ApproverAIAgent {
			out = append(out, fmt.Sprintf("%s is ignored: the type is %q, and the approval agent "+
				"notifies nobody", at, analyzer.ApproverAIAgent))
			continue
		}
		for _, k := range slices.Sorted(maps.Keys(a.Routing.Notify)) {
			n := a.Routing.Notify[k]
			if k != NotifySlack && k != NotifyTeams {
				out = append(out, fmt.Sprintf("%s.notify.%s is ignored: only %s and %s notify",
					at, k, NotifySlack, NotifyTeams))
				continue
			}
			if n.Delivery != "" && n.Delivery != n.EffectiveDelivery() {
				out = append(out, fmt.Sprintf("%s.notify.%s.delivery %q is not %s, %s or %s; %s is used",
					at, k, n.Delivery, DeliveryDM, DeliveryChannel, DeliveryBoth, DeliveryDM))
			}
			if len(n.ChannelsCandidates) > 0 && n.EffectiveDelivery() == DeliveryDM {
				out = append(out, fmt.Sprintf("%s.notify.%s.channels_candidates is ignored: "+
					"delivery %s posts to no channel", at, k, DeliveryDM))
			}
		}
	}
	return out
}

// ServeLevelApprovers returns cfg for hs: unchanged when hs has the cap; else
// a copy with approvers dropped from every block that names an
// approval_rule and sets no routing. A block that sets approvers and names no
// approval_rule, or sets routing, keeps them, so CheckServable refuses it
// naming the capability. cfg is never changed.
//
// On an old build a dropped ai_agent level becomes a human hold under
// approval_rule: more oversight, never less. Routing is never dropped:
// approval_rule would page other people than the routing selects.
func ServeLevelApprovers(cfg Config, hs Handshake) Config {
	if hs.grants(CapabilityLevelApprovers, capabilitySince()) {
		return cfg
	}
	var listeners []ListenerConfig
	for i, l := range cfg.Listeners {
		if l.Analyzer == nil || l.Analyzer.Approvers == nil || l.Analyzer.ApprovalRule == "" ||
			l.Analyzer.Approvers.routes() {
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

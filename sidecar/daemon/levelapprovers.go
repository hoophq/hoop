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

	// Notify maps an integration (slack, teams) onto how it tells the
	// approvers of a human level with static groups. Nothing selects: every
	// approver of the groups gets the direct message, and every channel the
	// key allows gets the post. Absent keeps the channel post of a level
	// without it. A level with routing sets routing.notify, never this.
	// The control plane reads it; the sidecar files the level and never the
	// notify.
	Notify map[string]NotifyRoute `json:"notify,omitempty" cap:"analyzer_approver_notify"`

	// Owners lets the sidecar's owners approve a human level, alongside any
	// Groups named on the same entry. Empty Groups with Owners means only the
	// owners approve, not the administrators. The control plane reads it; the
	// sidecar files the level and never the owners.
	Owners bool `json:"owners,omitempty" cap:"analyzer_approver_owners"`

	// Escalate adds Groups to a pending review and notifies their members by
	// direct message when nobody decides within AfterSec seconds. The control
	// plane reads it; the sidecar files the level and never the escalation.
	Escalate *ApprovalEscalation `json:"escalate,omitempty" cap:"analyzer_approver_escalate"`

	// AllGroupsMustApprove requires every approver group (including Owners
	// when set) to approve a human level, matching Access Request Rules.
	// Refused beside MinApprovals.
	AllGroupsMustApprove bool `json:"all_groups_must_approve,omitempty" cap:"analyzer_approver_min_approvals"`

	// MinApprovals sets how many approver groups must approve a human level
	// (1 when unset, matching Access Request Rules). Refused beside
	// AllGroupsMustApprove.
	MinApprovals int `json:"min_approvals,omitempty" cap:"analyzer_approver_min_approvals"`
}

// CapabilityApprovalRouting means this build decodes approvers.<level>.routing.
// No downgrade exists: static groups would page other people.
const CapabilityApprovalRouting = "analyzer_approval_routing"

// CapabilityApproverNotify means this build decodes approvers.<level>.notify.
// The sidecar does nothing with the key, so ServeLevelApprovers serves a
// build without it the document with the key removed. A build that does not
// decode approvers at all is refused: it files under approval_rule, and the
// control plane would post in a channel what notify keeps out of one.
const CapabilityApproverNotify = "analyzer_approver_notify"

// CapabilityApproverOwners means this build decodes approvers.<level>.owners.
// The sidecar does nothing with the key, so ServeLevelApprovers serves a
// build without it the document with the key removed. A build that does not
// decode approvers at all is refused: it files under approval_rule, which
// lets other groups approve and posts in a channel.
const CapabilityApproverOwners = "analyzer_approver_owners"

// CapabilityApproverEscalate means this build decodes approvers.<level>.escalate.
// The sidecar does nothing with the key, so ServeLevelApprovers serves a
// build without it the document with the key removed. A build that does not
// decode approvers at all is refused: under approval_rule the escalation
// would not run.
const CapabilityApproverEscalate = "analyzer_approver_escalate"

// CapabilityApproverMinApprovals means this build decodes
// approvers.<level>.all_groups_must_approve and approvers.<level>.min_approvals.
// The sidecar does nothing with either key, so ServeLevelApprovers strips them
// for builds with approvers and without this cap; builds without approvers are
// refused.
const CapabilityApproverMinApprovals = "analyzer_approver_min_approvals"

// Bounds of ApprovalEscalation.AfterSec: one minute to seven days, matching
// the sidecar review TTL bounds on the control plane.
const (
	MinEscalationDelaySec = 60
	MaxEscalationDelaySec = 604800
)

// ApprovalEscalation sends a pending approval request to more groups by
// direct message when nobody decides within AfterSec seconds.
type ApprovalEscalation struct {
	AfterSec int      `json:"after_sec,omitempty"`
	Groups   []string `json:"groups,omitempty"`
}

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
	// reaches. Under approvers.<level>.notify nothing selects: they are the
	// channels that get the post, and empty means the channels a level
	// without notify posts to.
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

// notifies reports a block where some level sets notify.
func (a *LevelApprovers) notifies() bool {
	return a != nil && slices.ContainsFunc([]*LevelApprover{a.High, a.Medium, a.Low},
		func(l *LevelApprover) bool { return l != nil && len(l.Notify) > 0 })
}

// hasOwners reports a block where some level sets owners.
func (a *LevelApprovers) hasOwners() bool {
	return a != nil && slices.ContainsFunc([]*LevelApprover{a.High, a.Medium, a.Low},
		func(l *LevelApprover) bool { return l != nil && l.Owners })
}

// escalates reports a block where some level sets escalate.
func (a *LevelApprovers) escalates() bool {
	return a != nil && slices.ContainsFunc([]*LevelApprover{a.High, a.Medium, a.Low},
		func(l *LevelApprover) bool { return l != nil && l.Escalate != nil })
}

// hasMinApprovals reports a block where some level sets all_groups_must_approve
// or min_approvals.
func (a *LevelApprovers) hasMinApprovals() bool {
	return a != nil && slices.ContainsFunc([]*LevelApprover{a.High, a.Medium, a.Low},
		func(l *LevelApprover) bool { return l != nil && (l.AllGroupsMustApprove || l.MinApprovals != 0) })
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
		problems = append(problems, validateNotify(a, at)...)
		problems = append(problems, validateOwners(a, at)...)
		problems = append(problems, validateEscalate(a, at)...)
		problems = append(problems, validateMinApprovals(a, at)...)
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

// validateNotify refuses notify where nobody would read it, and a blank
// channel.
func validateNotify(a *LevelApprover, at string) []string {
	if len(a.Notify) == 0 {
		return nil
	}
	var problems []string
	if a.Routing != nil {
		problems = append(problems, fmt.Sprintf("%s: notify and routing are alternatives; "+
			"a level with routing sets routing.notify", at))
	}
	if a.Type == analyzer.ApproverAIAgent {
		problems = append(problems, fmt.Sprintf("%s: notify names people nobody asks, "+
			"and the type is %q", at, analyzer.ApproverAIAgent))
	}
	for _, k := range slices.Sorted(maps.Keys(a.Notify)) {
		for i, c := range a.Notify[k].ChannelsCandidates {
			if strings.TrimSpace(c) == "" {
				problems = append(problems, fmt.Sprintf("%s: notify.%s.channels_candidates[%d] is blank", at, k, i))
			}
		}
	}
	return problems
}

// validateOwners refuses owners where nobody would read it or beside routing.
func validateOwners(a *LevelApprover, at string) []string {
	if !a.Owners {
		return nil
	}
	var problems []string
	if a.Routing != nil {
		problems = append(problems, fmt.Sprintf("%s: owners and routing are alternatives; "+
			"set owners for the sidecar's owners or routing for AI routing, not both", at))
	}
	if a.Type == analyzer.ApproverAIAgent {
		problems = append(problems, fmt.Sprintf("%s: owners name people nobody asks, "+
			"and the type is %q", at, analyzer.ApproverAIAgent))
	}
	return problems
}

// validateEscalate refuses an escalation on ai_agent, an out-of-range delay,
// an empty group list, or a blank group.
func validateEscalate(a *LevelApprover, at string) []string {
	e := a.Escalate
	if e == nil {
		return nil
	}
	var problems []string
	if a.Type == analyzer.ApproverAIAgent {
		problems = append(problems, fmt.Sprintf("%s: escalate names people nobody asks, "+
			"and the type is %q", at, analyzer.ApproverAIAgent))
	}
	if e.AfterSec < MinEscalationDelaySec || e.AfterSec > MaxEscalationDelaySec {
		problems = append(problems, fmt.Sprintf("%s: escalate.after_sec must be between %d and %d seconds, got %d",
			at, MinEscalationDelaySec, MaxEscalationDelaySec, e.AfterSec))
	}
	if len(e.Groups) == 0 {
		problems = append(problems, fmt.Sprintf("%s: escalate.groups is required", at))
	}
	for i, g := range e.Groups {
		if strings.TrimSpace(g) == "" {
			problems = append(problems, fmt.Sprintf("%s: escalate.groups[%d] is blank", at, i))
		}
	}
	return problems
}

// validateMinApprovals refuses all_groups_must_approve / min_approvals where
// invalid, matching Access Request Rules.
func validateMinApprovals(a *LevelApprover, at string) []string {
	if !a.AllGroupsMustApprove && a.MinApprovals == 0 {
		return nil
	}
	var problems []string
	if a.Type == analyzer.ApproverAIAgent {
		problems = append(problems, fmt.Sprintf("%s: approval count does not apply to type %q", at, analyzer.ApproverAIAgent))
		return problems
	}
	if a.MinApprovals < 0 {
		problems = append(problems, fmt.Sprintf("%s: min_approvals must be at least 1, got %d", at, a.MinApprovals))
	}
	if a.AllGroupsMustApprove && a.MinApprovals != 0 {
		problems = append(problems, fmt.Sprintf("%s: all_groups_must_approve and min_approvals are alternatives; set one", at))
	}
	if a.MinApprovals > 0 && a.Routing == nil {
		count := staticApproverGroupCount(a)
		if a.MinApprovals > count {
			problems = append(problems, fmt.Sprintf("%s: min_approvals (%d) exceeds the number of approver groups (%d)",
				at, a.MinApprovals, count))
		}
	}
	return problems
}

func staticApproverGroupCount(a *LevelApprover) int {
	seen := map[string]struct{}{}
	for _, g := range a.Groups {
		g = strings.TrimSpace(g)
		if g != "" {
			seen[g] = struct{}{}
		}
	}
	n := len(seen)
	if a.Owners {
		delete(seen, "sidecar-owners")
		return len(seen) + 1
	}
	if n == 0 {
		return 1
	}
	return n
}

// LaneApproverWarnings lists the routing and notify settings that the control
// plane ignores or reads as another value. None is refused. Exported so the
// control plane can show them at the save.
func LaneApproverWarnings(la *LaneAnalyzerConfig) []string {
	if la == nil {
		return nil
	}
	var out []string
	for _, l := range la.levels() {
		a := la.LevelApprover(l.risk)
		if a == nil {
			continue
		}
		out = append(out, notifyWarnings(fmt.Sprintf("approvers.%s", l.name), a.Notify)...)
		if a.Routing == nil {
			continue
		}
		at := fmt.Sprintf("approvers.%s.routing", l.name)
		if a.Type == analyzer.ApproverAIAgent {
			out = append(out, fmt.Sprintf("%s is ignored: the type is %q, and the approval agent "+
				"notifies nobody", at, analyzer.ApproverAIAgent))
			continue
		}
		out = append(out, notifyWarnings(at, a.Routing.Notify)...)
	}
	return out
}

// notifyWarnings lists the keys of one notify map that are ignored or read
// as another value. at names the map's parent.
func notifyWarnings(at string, notify map[string]NotifyRoute) []string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(notify)) {
		n := notify[k]
		if k != NotifySlack && k != NotifyTeams {
			out = append(out, fmt.Sprintf("%s.notify.%s is ignored: only %s and %s are notify keys",
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
	return out
}

// ServeLevelApprovers returns cfg for hs. cfg is never changed.
//
// A build that decodes approvers gets cfg, without the keys of
// approvers.<level> that only the control plane reads (notify) when the build
// does not decode them: the sidecar does nothing with them, and the control
// plane acts on its own copy.
//
// A build that does not decode approvers gets a copy with approvers dropped
// from every block that names an approval_rule and sets no routing and no
// notify. A block that sets approvers and names no approval_rule, or sets
// routing or notify, keeps them, so CheckServable refuses it naming the
// capability.
//
// On an old build a dropped ai_agent level becomes a human hold under
// approval_rule: more oversight, never less. Routing is never dropped:
// approval_rule would page other people than the routing selects. Notify is
// never dropped with approvers either: approval_rule posts in a channel.
func ServeLevelApprovers(cfg Config, hs Handshake) Config {
	since := capabilitySince()
	if hs.grants(CapabilityLevelApprovers, since) {
		stripNotify := !hs.grants(CapabilityApproverNotify, since)
		stripOwners := !hs.grants(CapabilityApproverOwners, since)
		stripEscalate := !hs.grants(CapabilityApproverEscalate, since)
		stripMin := !hs.grants(CapabilityApproverMinApprovals, since)
		if !stripNotify && !stripOwners && !stripEscalate && !stripMin {
			return cfg
		}
		return mapLevelApprovers(cfg, func(a *LevelApprover) bool {
			changed := false
			if stripNotify && len(a.Notify) > 0 {
				a.Notify = nil
				changed = true
			}
			if stripOwners && a.Owners {
				a.Owners = false
				changed = true
			}
			if stripEscalate && a.Escalate != nil {
				a.Escalate = nil
				changed = true
			}
			if stripMin && (a.AllGroupsMustApprove || a.MinApprovals != 0) {
				a.AllGroupsMustApprove = false
				a.MinApprovals = 0
				changed = true
			}
			return changed
		})
	}
	var listeners []ListenerConfig
	for i, l := range cfg.Listeners {
		if l.Analyzer == nil || l.Analyzer.Approvers == nil || l.Analyzer.ApprovalRule == "" ||
			l.Analyzer.Approvers.routes() || l.Analyzer.Approvers.notifies() ||
			l.Analyzer.Approvers.hasOwners() || l.Analyzer.Approvers.escalates() ||
			l.Analyzer.Approvers.hasMinApprovals() {
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

// mapLevelApprovers returns cfg with change applied to a copy of every level
// approver. change reports whether it changed the copy; a listener nothing
// changed in keeps its block. cfg is never changed.
func mapLevelApprovers(cfg Config, change func(*LevelApprover) bool) Config {
	var listeners []ListenerConfig
	for i, l := range cfg.Listeners {
		if l.Analyzer == nil || l.Analyzer.Approvers == nil {
			continue
		}
		approvers := *l.Analyzer.Approvers
		changed := false
		for _, level := range []**LevelApprover{&approvers.High, &approvers.Medium, &approvers.Low} {
			if *level == nil {
				continue
			}
			a := **level
			if change(&a) {
				*level = &a
				changed = true
			}
		}
		if !changed {
			continue
		}
		if listeners == nil {
			listeners = slices.Clone(cfg.Listeners)
		}
		block := *l.Analyzer
		block.Approvers = &approvers
		listeners[i].Analyzer = &block
	}
	if listeners != nil {
		cfg.Listeners = listeners
	}
	return cfg
}

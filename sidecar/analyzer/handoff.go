package analyzer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// Handoff configures one ai-handoff risk level: after the classification, a
// second model turn, the hand-off agent, selects the action.
type Handoff struct {
	// Prompt replaces DefaultHandoffPrompt. The fixed suffix is appended
	// either way.
	Prompt string

	// Allowed are the actions the agent may select: allow, warn, block and
	// require_review (also spelled require_approval). New refuses an empty
	// list and any other action.
	Allowed []Action

	// Approval is who decides on a require_review the agent selects, as
	// for a held level: ApproverHuman files the level, ApproverAIAgent
	// decides in the sidecar, and an empty Approver files under the lane's
	// approval rule. Set only when require_review is allowed.
	Approval Approval
}

// DefaultHandoffPrompt is the hand-off agent's instruction when the level
// sets no prompt.
const DefaultHandoffPrompt = `You are a decision agent for a production access proxy. A risk analyzer
rated this statement. You select what the proxy does with it.

Select the least restrictive action that keeps the data safe:
- allow: the statement is safe for this connection.
- warn: the statement is probably safe, but a person should see it later.
- require_approval: the statement can be correct, but it needs a decision
  before it runs.
- block: the statement is dangerous or has no valid purpose here.

When you are not sure, select the most restrictive action you have.`

// handoffContract is appended to every hand-off prompt. No config removes it.
const handoffContract = `Report your decision by calling exactly one tool. You can call only the
tools you have. Do not answer in prose.
Put one short reason in the reason field.
Do not quote a literal value from the statement, such as an ID, an email,
a name or a token.`

// BuildHandoffPrompt renders the hand-off agent's system prompt: the level's
// prompt, or the default, then the fixed suffix.
func BuildHandoffPrompt(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = DefaultHandoffPrompt
	}
	return prompt + "\n\n" + handoffContract
}

// handoffUndecided is the reason recorded when the hand-off agent did not
// reach a valid decision.
const handoffUndecided = "the hand-off agent did not decide"

// handoffOrder lists the actions an agent may select, most restrictive
// first. A fail-safe applies the first one the level allows.
var handoffOrder = [...]Action{ActionBlock, ActionRequireReview, ActionWarn, ActionAllow}

// handoffTools are the tools in the order the agent reads them, with the
// action each one selects.
var handoffTools = [...]struct {
	action      Action
	name        string
	description string
}{
	{ActionAllow, "allow", "Run the statement."},
	{ActionWarn, "warn", "Run the statement and record a warning."},
	{ActionBlock, "block", "Deny the statement."},
	{ActionRequireReview, "require_approval", "Hold the statement for approval."},
}

// HandoffToolName is the tool, and the audit value of
// MetadataHandoffDecision, that selects action a.
func HandoffToolName(a Action) string {
	for _, t := range handoffTools {
		if t.action == a.Canonical() {
			return t.name
		}
	}
	return string(a)
}

// handoffLevel is the runtime form of one Handoff.
type handoffLevel struct {
	// allowed is canonical, most restrictive first.
	allowed []Action
	tools   []ToolSpec
	prompt  string

	// approval and approvalPrompt are for a require_review the agent
	// selects.
	approval       Approval
	approvalPrompt string

	// key fingerprints everything above. A cached decision is reused only
	// under the same key.
	key string
}

// newHandoffs checks Config.Handoffs against the action map and builds the
// runtime form.
func newHandoffs(cfg Config, actions ActionMap) (map[RiskLevel]*handoffLevel, error) {
	for level, a := range actions {
		if _, ok := cfg.Handoffs[level]; a == ActionHandoff && !ok {
			return nil, fmt.Errorf("sidecar/analyzer: risk %q hands off and has no hand-off configuration", level)
		}
	}
	var out map[RiskLevel]*handoffLevel
	for level, h := range cfg.Handoffs {
		if actions.actionFor(level) != ActionHandoff {
			return nil, fmt.Errorf("sidecar/analyzer: risk %q has a hand-off configuration and its action "+
				"is not %s", level, ActionHandoff)
		}
		if _, ok := cfg.Provider.(ToolCaller); !ok {
			return nil, fmt.Errorf("sidecar/analyzer: provider %q cannot run the hand-off agent that risk %q "+
				"names", cfg.Provider.Name(), level)
		}
		hl, err := newHandoffLevel(level, h)
		if err != nil {
			return nil, err
		}
		if out == nil {
			out = map[RiskLevel]*handoffLevel{}
		}
		out[level] = hl
	}
	return out, nil
}

func newHandoffLevel(level RiskLevel, h Handoff) (*handoffLevel, error) {
	seen := map[Action]bool{}
	for _, a := range h.Allowed {
		switch c := a.Canonical(); c {
		case ActionAllow, ActionWarn, ActionBlock, ActionRequireReview:
			seen[c] = true
		default:
			return nil, fmt.Errorf("sidecar/analyzer: risk %q: the hand-off agent cannot select %q", level, a)
		}
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("sidecar/analyzer: risk %q hands off with no allowed action", level)
	}
	hl := &handoffLevel{prompt: BuildHandoffPrompt(h.Prompt)}
	for _, a := range handoffOrder {
		if seen[a] {
			hl.allowed = append(hl.allowed, a)
		}
	}
	input := ToolInput{
		Properties: map[string]ToolProperty{
			"reason": {Type: "string", Description: "One short sentence. Written to the audit trail."},
		},
		Required: []string{"reason"},
	}
	names := make([]string, 0, len(hl.allowed))
	for _, t := range handoffTools {
		if seen[t.action] {
			hl.tools = append(hl.tools, ToolSpec{Name: t.name, Description: t.description, Input: input})
			names = append(names, t.name)
		}
	}
	ap := h.Approval
	switch {
	case !seen[ActionRequireReview]:
		if ap != (Approval{}) {
			return nil, fmt.Errorf("sidecar/analyzer: risk %q names approver %q and its hand-off agent "+
				"cannot select %s", level, ap.Approver, ActionRequireReview)
		}
	case ap.Approver == "":
		if ap.Prompt != "" {
			return nil, fmt.Errorf("sidecar/analyzer: risk %q sets an approval prompt and names no "+
				"approver for its hand-off", level)
		}
	case ap.Approver == ApproverAIAgent:
		hl.approvalPrompt = BuildApprovalPrompt(ap.Prompt)
	case ap.Approver == ApproverHuman:
		ap.Prompt = ""
	default:
		return nil, fmt.Errorf("sidecar/analyzer: unknown approver %q for the hand-off of risk %q", ap.Approver, level)
	}
	hl.approval = ap
	hl.key = fingerprint(strings.Join([]string{string(level), hl.prompt, strings.Join(names, ","),
		string(ap.Approver), hl.approvalPrompt}, "\x00"))
	return hl, nil
}

// failSafe is the most restrictive action the level allows.
func (h *handoffLevel) failSafe() Action { return h.allowed[0] }

// toolNames lists the agent's tools, in the order it reads them.
func (h *handoffLevel) toolNames() []string {
	out := make([]string, len(h.tools))
	for i, t := range h.tools {
		out[i] = t.Name
	}
	return out
}

// handoffDecision is what an ai-handoff level resolved to.
type handoffDecision struct {
	action Action
	reason string

	// cause is why the agent did not decide, or nil for a decision.
	cause error
}

// handoffKey keys a hand-off decision on a classification's cache entry: the
// level's hand-off configuration and the exact content the agent reads. The
// shape key drops literals and quoted identifiers, so a decision is reused
// only for the same statement, principal, database and connection.
func handoffKey(h *handoffLevel, content string) string {
	return fingerprint(h.key + "\x00" + content)
}

// handoff runs the hand-off agent for one classified statement, or reuses
// the decision the cache keeps for its shape and handoffKey. It never
// returns an action the level does not allow. Anything but one valid call
// of an allowed tool applies the most restrictive allowed action, and that
// is never cached.
func (e *Evaluator) handoff(ctx context.Context, stmt inspect.Statement, ec *policy.EvalContext,
	level RiskLevel, c classified) handoffDecision {
	h := e.handoffs[level]
	if h == nil {
		// New refuses this; kept so a broken invariant still denies.
		return handoffDecision{action: ActionBlock, reason: handoffUndecided,
			cause: fmt.Errorf("risk %q has no hand-off configuration", level)}
	}
	failSafe := func(reason string, cause error) handoffDecision {
		return handoffDecision{action: h.failSafe(), reason: reason, cause: cause}
	}
	// Before the cache: a decision on the shape must not reach a statement
	// the agent could not have read in full.
	if reason := e.incompleteFor(stmt, "hand-off agent"); reason != "" {
		return failSafe(reason, errors.New(reason))
	}
	user := ""
	if ec != nil {
		user = ec.Context["principal"]
	}
	content := approvalContent(c.text, level, c.res.Title, e.cfg.Listener, stmt.Protocol, user) +
		"\nAllowed actions: " + strings.Join(h.toolNames(), ", ")
	memoKey := handoffKey(h, content)
	if m, ok := e.cache.decision(c.key, memoKey); ok {
		return handoffDecision{action: m.action, reason: m.reason}
	}
	caller, ok := e.cfg.Provider.(ToolCaller)
	if !ok {
		return failSafe(handoffUndecided, fmt.Errorf("provider %q cannot run the hand-off agent", e.cfg.Provider.Name()))
	}
	if err := e.takeCall(); err != nil {
		return failSafe(handoffUndecided, err)
	}
	var d handoffDecision
	err := e.agentCall(ctx, caller, h.prompt, content, h.tools, func(calls []ToolCall) error {
		action, reason, ok := parseHandoff(calls, h.tools)
		if !ok {
			return fmt.Errorf("the hand-off agent answered %d tool call(s), not one allowed action with a reason",
				len(calls))
		}
		d = handoffDecision{action: action, reason: e.recordReason(reason)}
		return nil
	})
	if err != nil {
		return failSafe(handoffUndecided, err)
	}
	e.cache.remember(c.key, memoKey, handoffMemo{action: d.action, reason: d.reason})
	return d
}

// parseHandoff accepts exactly one call of a tool the agent has, with a
// non-blank reason. ok is false for anything else.
func parseHandoff(calls []ToolCall, tools []ToolSpec) (Action, string, bool) {
	if len(calls) != 1 {
		return "", "", false
	}
	c := calls[0]
	var action Action
	for _, t := range handoffTools {
		if t.name == c.Name {
			action = t.action
		}
	}
	allowed := false
	for _, t := range tools {
		allowed = allowed || t.Name == c.Name
	}
	if action == "" || !allowed {
		return "", "", false
	}
	var args struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal(c.Args, &args) != nil {
		return "", "", false
	}
	reason := strings.TrimSpace(args.Reason)
	if reason == "" {
		return "", "", false
	}
	return action, reason, true
}

// applyHandoff runs the action the hand-off resolved to through the paths
// the level's own action would take: allow and warn forward, block denies
// with the recorded reason, and require_review holds under the hand-off's
// approver.
func (e *Evaluator) applyHandoff(stmt inspect.Statement, ec *policy.EvalContext,
	notes map[string]string, level RiskLevel, c classified, d handoffDecision) policy.Verdict {
	notes[MetadataHandoffDecision] = HandoffToolName(d.action)
	reason := d.reason
	if reason != "" {
		notes[MetadataHandoffReason] = reason
	}
	switch d.action {
	case ActionAllow, ActionWarn:
		return policy.Verdict{Annotations: notes}
	case ActionRequireReview:
		h := e.handoffs[level]
		return e.requestApproval(stmt, ec, notes, level, c, h.approval.Approver, h.approvalPrompt, true)
	}
	msg := e.cfg.Message
	if msg == "" {
		msg = c.res.Title
	}
	if msg == "" {
		msg = "refused by risk analysis"
	}
	if d.cause != nil {
		v := e.deny(notes, msg+": "+d.reason+", so the statement is blocked")
		v.Err = d.cause
		return v
	}
	const blocked = "the hand-off agent blocked the statement"
	denial := blocked
	room := maxDenialBytes - len(msg+": "+blocked+": ")
	if q := rejectionReason(reason, min(maxReasonBytes, room)); q != "" {
		denial = blocked + ": " + q
	}
	return e.deny(notes, msg+": "+denial)
}

// recordReason is the hand-off agent's prose as the record may keep it:
// the whole text goes through Config.RecordRedact first, so a cut cannot
// split a value the detector would find, and only then is it bounded.
// With no record redactor nothing is kept. The cache stores this result.
func (e *Evaluator) recordReason(reason string) string {
	if e.cfg.RecordRedact == nil {
		return ""
	}
	return Truncate(e.cfg.RecordRedact(reason), maxAgentReasonBytes)
}

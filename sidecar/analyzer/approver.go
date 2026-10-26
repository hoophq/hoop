package analyzer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// Approver is who decides on a statement a risk level holds.
type Approver string

const (
	// ApproverHuman files the statement for a person to approve, under the
	// level's own approval groups on the control plane.
	ApproverHuman Approver = "human"

	// ApproverAIAgent asks the lane's model to approve or reject the
	// statement, at once, with the approve and reject tools.
	ApproverAIAgent Approver = "ai_agent"
)

// Valid reports whether a is one of the two approvers.
func (a Approver) Valid() bool { return a == ApproverHuman || a == ApproverAIAgent }

// Approval is the approver of one held risk level written in the per-level
// form. A level held with no Approval is the lane-wide form: a human approves
// under the lane's approval rule.
type Approval struct {
	Approver Approver

	// Prompt replaces DefaultApprovalPrompt for an ai_agent level. The
	// fixed suffix is appended either way.
	Prompt string
}

// Metadata keys an approval agent's decision writes onto the audit record.
const (
	MetadataApprover         = "approver"
	MetadataApprovalDecision = "approval_decision"

	// MetadataApprovalReason is the agent's reason. It is model prose, an
	// exception to the rule above MetadataRiskLevel, accepted because the
	// fixed suffix forbids quoting a literal and the user reads the reason
	// anyway. It is bounded by maxAgentReasonBytes.
	MetadataApprovalReason = "approval_reason"
)

// Decisions an approval agent reaches.
const (
	DecisionApproved = "approved"
	DecisionRejected = "rejected"
)

// DefaultApprovalPrompt is the approval agent's instruction when the level
// sets no prompt.
const DefaultApprovalPrompt = `You are an approval agent for a production access proxy. A risk analyzer held
this statement. You decide to approve it or reject it.

You receive:
- The statement, as the client sent it.
- The risk level and the title from the risk analyzer.
- The connection name, the protocol and the user identity.

Approve the statement only when all of these are true:
- The statement has a clear, bounded scope. A write or a delete names its
  target rows, for example with a WHERE clause, an ID or a specific path.
- The statement does not change or remove a schema, a permission, a user or
  an audit record.
- The statement does not read or export a full table or a bulk data set.
- The statement agrees with the normal work of this connection.

Reject the statement when one of these is true:
- The scope is unbounded or you cannot find the scope.
- The statement is destructive and you cannot reverse it.
- The statement tries to hide its effect, for example with comments,
  encoding, or a harmless operation that wraps a dangerous one.
- You are not sure. When you are not sure, reject.`

// approvalContract is appended to every approval prompt. No config removes it.
const approvalContract = `Report your decision by calling exactly one tool: approve or reject.
Do not answer in prose.
Put one short reason in the reason field. The user reads the reject reason.
Do not quote a literal value from the statement, such as an ID, an email,
a name or a token.`

// BuildApprovalPrompt renders the approval agent's system prompt: the
// level's prompt, or the default, then the fixed suffix.
func BuildApprovalPrompt(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = DefaultApprovalPrompt
	}
	return prompt + "\n\n" + approvalContract
}

// Approval tool names.
const (
	toolApprove = "approve"
	toolReject  = "reject"
)

// ApprovalTools are the two tools the approval agent may call.
func ApprovalTools() []ToolSpec {
	input := ToolInput{
		Properties: map[string]ToolProperty{
			"reason": {
				Type:        "string",
				Description: "One short reason for the decision. Never quote a value from the statement.",
			},
		},
		Required: []string{"reason"},
	}
	return []ToolSpec{
		{Name: toolApprove, Description: "Approve the held statement, so it runs.", Input: input},
		{Name: toolReject, Description: "Reject the held statement, so it does not run.", Input: input},
	}
}

// undecidedReason is the reason recorded when the agent did not reach a
// valid decision. The statement is then rejected.
const undecidedReason = "the approval agent did not decide"

// maxAgentReasonBytes bounds the reason kept on the record and filed.
const maxAgentReasonBytes = 1 << 10

// Fixed reasons for a statement the approval agent cannot read in full.
const (
	reasonOverInput = "the statement is longer than max_input_bytes, so the approval agent " +
		"cannot read all of it"
	reasonBodyCut = "the request body is larger than the capture limit, so the approval agent " +
		"cannot read all of it"
)

// noCutLimit renders content with no practical bound, to compare with the
// bounded rendering. A statement past it is refused too.
const noCutLimit = 1 << 30

// incompleteForAgent reports why the agent's content would not hold the
// whole statement, or "". It rebuilds the content without the input bound
// and compares, so it catches every builder that truncates by
// max_input_bytes. Redaction runs after this and is not a cut.
func (e *Evaluator) incompleteForAgent(stmt inspect.Statement) string {
	if stmt.HTTP != nil && stmt.HTTP.BodyTruncated {
		return reasonBodyCut
	}
	builder, ok := BuilderFor(stmt.Protocol)
	if !ok {
		return reasonOverInput
	}
	bounded, ok1 := builder.Build(stmt, e.cfg.MaxInputBytes)
	full, ok2 := builder.Build(stmt, noCutLimit)
	if !ok1 || !ok2 || bounded.Text != full.Text || len(full.Text) >= noCutLimit {
		return reasonOverInput
	}
	return ""
}

// Decision is an approval agent's answer about one statement.
type Decision struct {
	RiskLevel RiskLevel
	Approved  bool
	Reason    string
}

// DecisionFiler is a Reviewer that records an approval agent's decision with
// its backend, so the approval history shows it. The control plane's
// reviewer implements it; the terminal one does not, and the audit record
// alone keeps the decision.
type DecisionFiler interface {
	FileDecision(ctx context.Context, statement string, d Decision) (ReviewResult, error)
}

type filedLevelKey struct{}

// FiledLevel reports the risk level a hold files under its own approver, the
// per-level form. ok is false for a hold under the lane's approval rule.
func FiledLevel(ctx context.Context) (RiskLevel, bool) {
	l, ok := ctx.Value(filedLevelKey{}).(RiskLevel)
	return l, ok
}

func withFiledLevel(ctx context.Context, level RiskLevel) context.Context {
	return context.WithValue(ctx, filedLevelKey{}, level)
}

// requestAgentApproval hands the agent's decision to the Chain like a hold,
// so it runs (and spends a model call) only after every later evaluator
// allowed. It has no wait mode: the decision is synchronous.
func (e *Evaluator) requestAgentApproval(ctx context.Context, stmt inspect.Statement,
	ec *policy.EvalContext, notes map[string]string, level RiskLevel, res Result, text string) policy.Verdict {
	user := ""
	if ec != nil {
		user = ec.Context["principal"]
	}
	resolve := func() policy.Verdict {
		return e.agentApproval(ctx, stmt, notes, level, res, text, user)
	}
	if ec.RequestReview(policy.ReviewRequest{Resolve: resolve}) {
		return policy.Verdict{Annotations: notes}
	}
	return resolve()
}

// agentApproval asks the approval agent and acts on its answer. It fails
// closed: anything but one valid approve rejects. With a backend that keeps
// decisions, an approval forwards only once the backend took it; a rejection
// is filed when it can be and denies either way.
func (e *Evaluator) agentApproval(ctx context.Context, stmt inspect.Statement, notes map[string]string,
	level RiskLevel, res Result, text, user string) policy.Verdict {
	d, cause := e.agentDecide(ctx, stmt, level, res, text, user)
	notes[MetadataApprover] = string(ApproverAIAgent)
	notes[MetadataApprovalDecision] = DecisionRejected
	if d.Approved {
		notes[MetadataApprovalDecision] = DecisionApproved
	}
	notes[MetadataApprovalReason] = d.Reason

	reviewID, status := "", ""
	if filer, ok := e.cfg.Review.(DecisionFiler); ok {
		statement, _, err := reviewText(stmt, e.cfg.MatchIgnore)
		if err == nil {
			var r ReviewResult
			r, err = e.ask(ctx, func(c context.Context) (ReviewResult, error) {
				return filer.FileDecision(c, statement, d)
			})
			reviewID, status = r.ID, r.Status
		}
		if reviewID != "" {
			notes[MetadataReviewID] = reviewID
		}
		if err != nil {
			e.errs.Add(1)
			if d.Approved {
				v := e.deny(notes, agentMessage(e.cfg.Message, reviewID,
					"the approval agent approved the statement and the decision could not be recorded"))
				v.Err = err
				return v
			}
			if cause == nil {
				cause = err
			}
		}
	}
	if d.Approved {
		return policy.Verdict{Annotations: notes}
	}
	const rejected = "the approval agent rejected the statement"
	reason := rejected
	room := maxDenialBytes - len(agentMessage(e.cfg.Message, reviewID, rejected+": "))
	if q := rejectionReason(d.Reason, min(maxReasonBytes, room)); q != "" {
		reason = rejected + ": " + q
	}
	v := e.deny(notes, agentMessage(e.cfg.Message, reviewID, reason))
	v.Err = cause
	if reviewID != "" {
		if status == "" {
			status = reviewRejected
		}
		v.Review = &policy.Review{ID: reviewID, Status: status}
	}
	return v
}

// agentMessage renders the denial an approval agent's decision produces.
func agentMessage(operator, reviewID, reason string) string {
	msg := operator
	if msg == "" {
		msg = "statement held for approval"
	}
	if reviewID == "" {
		return msg + ": " + reason
	}
	return fmt.Sprintf("%s: %s (approval %s)", msg, reason, reviewID)
}

// agentDecide makes one model call with the approval tools and reads the
// decision. It never returns an approval it could not verify. The error is
// why it did not decide, for the record, or nil.
func (e *Evaluator) agentDecide(ctx context.Context, stmt inspect.Statement, level RiskLevel,
	res Result, text, user string) (Decision, error) {
	undecided := Decision{RiskLevel: level, Reason: undecidedReason}
	// The agent must read the whole statement: an approval of a prefix
	// would release bytes nobody read. No model call is made.
	if reason := e.incompleteForAgent(stmt); reason != "" {
		return Decision{RiskLevel: level, Reason: reason}, errors.New(reason)
	}
	caller, ok := e.cfg.Provider.(ToolCaller)
	if !ok {
		// New refuses this; kept so a broken invariant still rejects.
		return undecided, fmt.Errorf("provider %q cannot run the approval agent", e.cfg.Provider.Name())
	}
	// One call against the same purse as the classification.
	status, edge, refused := e.budget.take(e.cfg.MaxCalls, e.cfg.RateLimit)
	if edge != edgeNone && e.cfg.OnRateLimit != nil {
		e.cfg.OnRateLimit(edge == edgeStarted, refused)
	}
	switch status {
	case StatusBudget:
		e.budgetLogged.Do(func() {
			if e.onBudget != nil {
				e.onBudget()
			}
		})
		return undecided, fmt.Errorf("the risk analysis budget is spent")
	case StatusRateLimited:
		e.rateLimited.Add(1)
		return undecided, fmt.Errorf("the risk analysis rate limit is reached")
	}

	callCtx, cancel := context.WithTimeout(ctx, e.cfg.Timeout)
	defer cancel()
	start := time.Now()
	content := approvalContent(text, level, res.Title, e.cfg.Listener, stmt.Protocol, user)
	var calls []ToolCall
	attempts, err := e.retrying(callCtx, func() error {
		var err error
		calls, err = caller.Decide(callCtx, e.agentPrompts[level], content, ApprovalTools())
		return err
	})
	if err == nil {
		var reason string
		var approved bool
		if approved, reason, ok = parseDecision(calls); ok {
			return Decision{RiskLevel: level, Approved: approved,
				Reason: Truncate(reason, maxAgentReasonBytes)}, e.reportCall(callCtx, start, attempts, nil)
		}
		err = fmt.Errorf("the approval agent answered %d tool call(s), not one approve or reject with a reason", len(calls))
	}
	e.errs.Add(1)
	return undecided, e.reportCall(callCtx, start, attempts, err)
}

// parseDecision accepts exactly one call of approve or reject with a
// non-blank reason. ok is false for anything else.
func parseDecision(calls []ToolCall) (approved bool, reason string, ok bool) {
	if len(calls) != 1 {
		return false, "", false
	}
	c := calls[0]
	if c.Name != toolApprove && c.Name != toolReject {
		return false, "", false
	}
	var args struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal(c.Args, &args) != nil {
		return false, "", false
	}
	reason = strings.TrimSpace(args.Reason)
	if reason == "" {
		return false, "", false
	}
	return c.Name == toolApprove, reason, true
}

// approvalContent is what the approval agent reads. The statement is the
// content the classification sent, under the same send and redaction rules.
func approvalContent(text string, level RiskLevel, title, connection string, protocol inspect.Protocol, user string) string {
	if user == "" {
		user = "unknown"
	}
	var b strings.Builder
	b.WriteString("Statement:\n")
	b.WriteString(text)
	b.WriteString("\n\nRisk level: ")
	b.WriteString(string(level))
	b.WriteString("\nRisk title: ")
	b.WriteString(title)
	b.WriteString("\nConnection: ")
	b.WriteString(connection)
	b.WriteString("\nProtocol: ")
	b.WriteString(string(protocol))
	b.WriteString("\nUser: ")
	b.WriteString(user)
	return b.String()
}

package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hoophq/hoop/sidecar/daemon"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The same numbers as the gateway's reviews_wait (gateway/api/mcpserver/
// poll.go). MCP clients drop a tool call that blocks past 60 to 120 seconds,
// so a wait is capped at 5 minutes and the agent calls again on timed_out.
const (
	defaultWaitTimeout = 60 * time.Second
	maxWaitTimeout     = 300 * time.Second
	pollInterval       = 2 * time.Second
)

// The plane's statuses this server acts on. Any other one stops the agent.
const (
	statusPending  = "PENDING"
	statusApproved = "APPROVED"
	statusRejected = "REJECTED"
	statusRevoked  = "REVOKED"
	statusExecuted = "EXECUTED"
	statusExpired  = "EXPIRED"
)

type tools struct {
	reviews daemon.ReviewStatusReader
	// poll is pollInterval outside tests.
	poll time.Duration
	// life ends every call when the server stops. Nil means no bound.
	life context.Context
}

// bound cancels ctx when the server stops.
func (t *tools) bound(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	if t.life == nil {
		return ctx, cancel
	}
	stop := context.AfterFunc(t.life, cancel)
	return ctx, func() { stop(); cancel() }
}

type statusInput struct {
	ID string `json:"id" jsonschema:"the review id from the sidecar's deny message"`
}

type listInput struct {
	Status string `json:"status,omitempty" jsonschema:"only reviews in this status: PENDING, APPROVED, REJECTED, REVOKED, EXECUTED or EXPIRED"`
	Limit  int    `json:"limit,omitempty" jsonschema:"how many of the newest reviews, default 20, max 200"`
}

// listOutput wraps the list: a tool's structured output is an object.
type listOutput struct {
	Reviews []reviewOutput `json:"reviews"`
}

// defaultListLimit keeps an agent's context small. The plane allows more.
const defaultListLimit = 20

type waitInput struct {
	ID             string `json:"id" jsonschema:"the review id from the sidecar's deny message"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"how long to wait, default 60, max 300"`
}

// reviewOutput is one review and what to do about it.
type reviewOutput struct {
	ID              string     `json:"id"`
	Status          string     `json:"status"`
	ListenerName    string     `json:"listener_name"`
	ApprovalRule    string     `json:"approval_rule"`
	CreatedAt       time.Time  `json:"created_at"`
	DecidedAt       *time.Time `json:"decided_at,omitempty"`
	RejectionReason string     `json:"rejection_reason,omitempty"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	Next            string     `json:"next" jsonschema:"wait, resend_identical_statement or stop"`
	Instruction     string     `json:"instruction"`
	// TimedOut and WaitedSeconds are set by review_wait only.
	TimedOut      *bool `json:"timed_out,omitempty"`
	WaitedSeconds *int  `json:"waited_seconds,omitempty"`
}

func (t *tools) register(server *sdk.Server) {
	readOnly := &sdk.ToolAnnotations{ReadOnlyHint: true}
	sdk.AddTool(server, &sdk.Tool{
		Name: "review_status",
		Description: "Get the status of a review the sidecar filed when it held a statement for " +
			"human approval. The result's next field says what to do: wait, " +
			"resend_identical_statement, or stop.",
		Annotations: readOnly,
	}, t.status)
	sdk.AddTool(server, &sdk.Tool{
		Name: "review_list",
		Description: "List the reviews this sidecar filed, newest first, across every listener. " +
			"Each one's next field says what to do: wait, resend_identical_statement, or stop.",
		Annotations: readOnly,
	}, t.list)
	sdk.AddTool(server, &sdk.Tool{
		Name: daemon.ReviewWaitTool,
		Description: "Wait until a review is decided or the timeout elapses (default 60s, max 300s). " +
			"timed_out=true is not an error: call again to keep waiting. The result's next " +
			"field says what to do: wait, resend_identical_statement, or stop.",
		Annotations: readOnly,
	}, t.wait)
}

func (t *tools) status(ctx context.Context, _ *sdk.CallToolRequest, in statusInput) (*sdk.CallToolResult, reviewOutput, error) {
	if in.ID == "" {
		return nil, reviewOutput{}, errors.New("id is required")
	}
	ctx, cancel := t.bound(ctx)
	defer cancel()
	rev, err := t.read(ctx, in.ID)
	if err != nil {
		return nil, reviewOutput{}, err
	}
	return nil, describe(rev), nil
}

func (t *tools) list(ctx context.Context, _ *sdk.CallToolRequest, in listInput) (*sdk.CallToolResult, listOutput, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	limit = min(limit, daemon.MaxReviewListLimit)
	ctx, cancel := t.bound(ctx)
	defer cancel()
	revs, err := t.reviews.ListReviews(ctx, strings.ToUpper(in.Status), limit)
	if errors.Is(err, daemon.ErrPlaneTooOld) {
		return nil, listOutput{}, fmt.Errorf("%w. Use review_status with an id from a deny message", err)
	}
	if err != nil {
		return nil, listOutput{}, err
	}
	out := listOutput{Reviews: make([]reviewOutput, 0, len(revs))}
	for _, r := range revs {
		out.Reviews = append(out.Reviews, describe(r))
	}
	return nil, out, nil
}

func (t *tools) wait(ctx context.Context, req *sdk.CallToolRequest, in waitInput) (*sdk.CallToolResult, reviewOutput, error) {
	if in.ID == "" {
		return nil, reviewOutput{}, errors.New("id is required")
	}
	ctx, cancel := t.bound(ctx)
	defer cancel()
	timeout := resolveWaitTimeout(in.TimeoutSeconds)
	progress := progressNotifier(ctx, req, timeout)

	started := time.Now()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(t.poll)
	defer ticker.Stop()

	for {
		rev, err := t.read(ctx, in.ID)
		if err != nil {
			return nil, reviewOutput{}, err
		}
		elapsed := time.Since(started)
		if rev.Status != statusPending {
			return nil, withWait(describe(rev), false, elapsed), nil
		}
		select {
		case <-ctx.Done():
			return nil, reviewOutput{}, ctx.Err()
		case <-deadline.C:
			// One last read, so the answer is the state at the deadline
			// and not two seconds before it.
			rev, err = t.read(ctx, in.ID)
			if err != nil {
				return nil, reviewOutput{}, err
			}
			return nil, withWait(describe(rev), rev.Status == statusPending, time.Since(started)), nil
		case <-ticker.C:
			progress(time.Since(started))
		}
	}
}

// read asks the plane and turns its failures into what the agent should do.
func (t *tools) read(ctx context.Context, id string) (daemon.ReviewStatus, error) {
	rev, err := t.reviews.ReviewStatus(ctx, id)
	switch {
	case errors.Is(err, daemon.ErrReviewNotFound):
		return rev, fmt.Errorf("review %s was not found on this sidecar. Stop: check the id "+
			"against the deny message, and that this MCP entry is the sidecar that denied it", id)
	case errors.Is(err, daemon.ErrPlaneTooOld):
		return rev, fmt.Errorf("%w. Ask a human to check review %s in the control plane", err, id)
	}
	return rev, err
}

func describe(r daemon.ReviewStatus) reviewOutput {
	out := reviewOutput{
		ID:              r.ID,
		Status:          r.Status,
		ListenerName:    r.ListenerName,
		ApprovalRule:    r.ApprovalRule,
		CreatedAt:       r.CreatedAt,
		DecidedAt:       r.DecidedAt,
		RejectionReason: r.RejectionReason,
		ExpiresAt:       r.ExpiresAt,
		Next:            daemon.ReviewNext(r.Status),
	}
	switch r.Status {
	case statusPending:
		out.Instruction = "No reviewer has decided yet. Call review_wait with this id. Do not " +
			"resend the statement now, and never reformat it: different bytes file a new review."
		if r.ExpiresAt != nil {
			out.Instruction += fmt.Sprintf(" It expires at %s if nobody decides.", deadline(r.ExpiresAt))
		}
	case statusApproved:
		out.Instruction = fmt.Sprintf("Approved. Resend the identical statement, byte for byte, "+
			"to listener %s. It runs once.", r.ListenerName)
		if r.ExpiresAt != nil {
			out.Instruction += fmt.Sprintf(" Resend it before %s, when the approval expires.", deadline(r.ExpiresAt))
		}
	case statusRejected:
		out.Instruction = "Rejected. The statement will not run; do not resend it."
		if r.RejectionReason != "" {
			out.Instruction += " Reason: " + r.RejectionReason
		}
	case statusRevoked:
		out.Instruction = "The approval was revoked. The statement will not run; do not resend it."
	case statusExecuted:
		out.Instruction = "The approval was already used by a resent statement. Running it " +
			"again needs a new review."
	case statusExpired:
		out.Instruction = "The review expired before it was decided or used. The statement did " +
			"not run. Resending the identical statement files a new review and asks the " +
			"approvers again; do that only if a human asks for it."
	default:
		out.Instruction = fmt.Sprintf("Unknown review status %q. Stop and ask a human.", r.Status)
	}
	return out
}

// deadline spells a review deadline for an agent: UTC, so every agent reads
// the same instant whatever the host's zone.
func deadline(t *time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func withWait(out reviewOutput, timedOut bool, elapsed time.Duration) reviewOutput {
	waited := int(elapsed.Round(time.Second) / time.Second)
	out.TimedOut, out.WaitedSeconds = &timedOut, &waited
	return out
}

// resolveWaitTimeout clamps a caller's timeout into [pollInterval,
// maxWaitTimeout]. Zero or negative means defaultWaitTimeout.
func resolveWaitTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return defaultWaitTimeout
	}
	// Clamp the seconds before converting: a huge value would overflow
	// time.Duration and wrap negative.
	seconds = min(seconds, int(maxWaitTimeout/time.Second))
	return max(time.Duration(seconds)*time.Second, pollInterval)
}

// progressNotifier reports elapsed seconds on the call's own response
// stream, which keeps the long request alive through proxies and clients.
// It does nothing when the client sent no progress token.
func progressNotifier(ctx context.Context, req *sdk.CallToolRequest, total time.Duration) func(time.Duration) {
	noop := func(time.Duration) {}
	if req == nil || req.Session == nil || req.Params == nil {
		return noop
	}
	token := req.Params.GetProgressToken()
	if token == nil {
		return noop
	}
	return func(elapsed time.Duration) {
		// Best effort: a lost notification must not end the wait.
		_ = req.Session.NotifyProgress(ctx, &sdk.ProgressNotificationParams{
			ProgressToken: token,
			Progress:      elapsed.Seconds(),
			Total:         total.Seconds(),
			Message:       "waiting for a reviewer",
		})
	}
}

// Package approvalrouting is the shared AI routing step of a human approval
// (ENG-663). Every code path that files a human review under an approver
// with routing calls RouteReview before it creates the review, and Notify
// after.
//
// The model sees opaque ids only: no user name and no email reaches the
// prompt. Names are resolved after the decision, for display.
package approvalrouting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	laia "github.com/hoophq/hoop/common/aianalyzer"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/hoophq/hoop/sidecar/daemon"
	"gorm.io/gorm"
)

// Timeout is the hard limit of one routing step. The step runs inside the
// sidecar's filing request, so the sidecar gives that request more time.
const Timeout = 20 * time.Second

// Fallback causes recorded in models.ReviewRouting.FallbackCause.
const (
	CauseNoProvider      = "no_provider"
	CauseTimeout         = "timeout"
	CauseProviderError   = "provider_error"
	CauseNoToolCall      = "no_tool_call"
	CauseRefusal         = "refusal"
	CauseInvalidDecision = "invalid_decision"
	CauseNoValidGroup    = "no_valid_group"
)

// Delivery values of a notify route, as the sidecar config spells them.
const (
	DeliveryDM      = daemon.DeliveryDM
	DeliveryChannel = daemon.DeliveryChannel
	DeliveryBoth    = daemon.DeliveryBoth
)

// Bounds of the recorded text.
const (
	maxReasonRunes = 500
	maxDetailRunes = 300
)

// RouteInput is what one filing knows about the request it routes.
type RouteInput struct {
	OrgID string
	// RequesterID is the hoop user id that asked, "" when no user did (a
	// sidecar filing). Never routed to, never notified.
	RequesterID     string
	RequesterGroups []string
	// Principal names the requester as the sidecar saw it: a user id, an
	// auth subject or an email. Every org user it names is excluded, as
	// RequesterID is.
	Principal    string
	SidecarID    string
	ListenerName string
	Connection   string
	Protocol     string
	RiskLevel    string
	Title        string
	// Statement is the raw statement. The model reads only its shape
	// (analyzer.StatementShape), never its literals.
	Statement string
	Routing   daemon.ApprovalRouting
}

// RouteDecision is the outcome of RouteReview.
type RouteDecision struct {
	// Groups are the groups the review is filed to. Never empty.
	Groups []string
	// Record is what the review row stores.
	Record *models.ReviewRouting

	// plan is nil when the notifications keep today's behavior: notify
	// absent, or a fallback.
	plan *notifyPlan
}

// Notifies reports a decision whose notifications go through Notify. When
// false, the caller notifies as it did before routing existed.
func (d *RouteDecision) Notifies() bool {
	return d != nil && d.plan != nil
}

// Users are the users the decision selected to notify, nil without a plan.
func (d *RouteDecision) Users() []string {
	if !d.Notifies() {
		return nil
	}
	return slices.Clone(d.plan.users)
}

// ClientFactory returns the LLM client of the org. It reads the provider
// through db bounded by ctx. gorm.ErrRecordNotFound means the org has no AI
// provider.
type ClientFactory func(ctx context.Context, db *gorm.DB, orgID string) (laia.LLMClient, error)

// clientFactory is replaced by tests through SetClientFactory.
var clientFactory ClientFactory = orgClient

// SetClientFactory replaces the LLM client source and returns a function
// that restores the previous one. For tests.
func SetClientFactory(f ClientFactory) (restore func()) {
	prev := clientFactory
	clientFactory = f
	return func() { clientFactory = prev }
}

// budget is the limit RouteReview applies: Timeout, except in tests.
var budget = Timeout

// SetTimeout replaces the routing limit and returns a function that
// restores it. For tests.
func SetTimeout(d time.Duration) (restore func()) {
	prev := budget
	budget = d
	return func() { budget = prev }
}

// orgClient builds the client of the org's session analyzer provider.
func orgClient(ctx context.Context, db *gorm.DB, orgID string) (laia.LLMClient, error) {
	id, err := parseOrgID(orgID)
	if err != nil {
		return nil, err
	}
	provider, err := models.GetAIProviderWithDB(db.WithContext(ctx), id, models.AISessionAnalyzerFeature)
	if err != nil {
		return nil, err
	}
	return laia.NewLLMClient(laia.ProviderConfig{
		Provider: provider.Provider,
		APIURL:   provider.ApiUrl,
		APIKey:   provider.ApiKey,
		Model:    provider.Model,
	})
}

// RouteReview selects the groups of a new review and the users and channels
// to notify. A failed or invalid AI decision falls back to static routing:
// every candidate. Every step runs inside the routing limit. It returns an
// error only when the candidates, or the rest of the context, cannot be read
// for a reason other than the limit; the caller then files nothing.
func RouteReview(ctx context.Context, db *gorm.DB, in RouteInput) (*RouteDecision, error) {
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	// The candidates first: the static fallback needs them, and it must not
	// read the database again once the limit has passed.
	candidates, err := candidateGroups(ctx, db, in)
	if err != nil {
		return nil, fmt.Errorf("failed reading the routing candidates in %s: %w", budget, err)
	}
	rc := &routeContext{in: in, candidates: candidates}
	if err := rc.load(ctx, db); err != nil {
		if ctx.Err() != nil {
			return rc.fallback(CauseTimeout, "reading the routing context: "+err.Error()), nil
		}
		return nil, err
	}

	client, err := clientFactory(ctx, db, in.OrgID)
	switch {
	case ctx.Err() != nil:
		return rc.fallback(CauseTimeout, fmt.Sprintf("no AI provider read in %s", budget)), nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		return rc.fallback(CauseNoProvider, "the organization has no AI provider for the session analyzer"), nil
	case err != nil:
		return rc.fallback(CauseProviderError, err.Error()), nil
	}

	resp, err := client.Chat(ctx, rc.chatRequest())
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			return rc.fallback(CauseTimeout, fmt.Sprintf("no answer in %s", budget)), nil
		}
		return rc.fallback(CauseProviderError, err.Error()), nil
	}
	return rc.decide(resp), nil
}

// toolFields are the route_approval fields; every one is required.
var toolFields = []string{"groups", "notify_users", "channels", "reason"}

// toolCallArgs is the route_approval input.
type toolCallArgs struct {
	Groups      []string `json:"groups"`
	NotifyUsers []string `json:"notify_users"`
	Channels    []struct {
		Integration string `json:"integration"`
		Channel     string `json:"channel"`
	} `json:"channels"`
	Reason string `json:"reason"`
}

// decide validates the model's answer and builds the decision.
func (rc *routeContext) decide(resp *laia.ChatResponse) *RouteDecision {
	switch {
	case resp == nil:
		return rc.fallback(CauseNoToolCall, "the provider returned no answer")
	case strings.TrimSpace(resp.Refusal) != "":
		return rc.fallback(CauseRefusal, "the model refused: "+resp.Refusal)
	case len(resp.ToolCalls) == 0 && strings.TrimSpace(resp.Content) != "":
		return rc.fallback(CauseRefusal, "the model answered in prose: "+resp.Content)
	case len(resp.ToolCalls) == 0:
		return rc.fallback(CauseNoToolCall, "the model called no tool")
	case len(resp.ToolCalls) > 1:
		return rc.fallback(CauseNoToolCall, fmt.Sprintf("the model called %d tools, not one", len(resp.ToolCalls)))
	}
	call := resp.ToolCalls[0]
	if call.Name != toolName {
		return rc.fallback(CauseInvalidDecision, fmt.Sprintf("the model called the unknown tool %q", call.Name))
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(call.Arguments), &fields); err != nil {
		return rc.fallback(CauseInvalidDecision, "the tool input is not a JSON object: "+err.Error())
	}
	for _, f := range toolFields {
		if v, ok := fields[f]; !ok || string(v) == "null" {
			return rc.fallback(CauseInvalidDecision, fmt.Sprintf("the tool input has no %s", f))
		}
	}
	var args toolCallArgs
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return rc.fallback(CauseInvalidDecision, "the tool input does not match the schema: "+err.Error())
	}
	reason := boundText(args.Reason, maxReasonRunes)
	if reason == "" {
		return rc.fallback(CauseInvalidDecision, "the tool input has a blank reason")
	}

	// Groups: candidates only, in candidate order.
	var groups []string
	for _, g := range rc.candidates {
		if slices.Contains(args.Groups, g) {
			groups = append(groups, g)
		}
	}
	if len(groups) == 0 {
		return rc.fallback(CauseNoValidGroup, "no selected group is a candidate")
	}

	// Users: members of a selected group, never the requester.
	var users []string
	for _, id := range args.NotifyUsers {
		m, ok := rc.members[id]
		if !ok || slices.Contains(rc.excluded, id) || slices.Contains(users, id) {
			continue
		}
		if slices.ContainsFunc(m.groups, func(g string) bool { return slices.Contains(groups, g) }) {
			users = append(users, id)
		}
	}

	// Channels: an integrated channel route, and a channel it allows.
	plan := rc.plan(users)
	for _, ch := range args.Channels {
		route := plan.route(ch.Integration)
		if route == nil || route.notifier == nil || route.delivery == DeliveryDM {
			continue
		}
		allowed, ok := route.allowed[ch.Channel]
		if !ok || slices.ContainsFunc(route.channels, func(c models.ReviewRoutingChannel) bool { return c.ID == ch.Channel }) {
			continue
		}
		route.channels = append(route.channels, allowed)
	}

	record := rc.record(models.ReviewRoutingModeAI, groups)
	record.Reason = reason
	d := &RouteDecision{Groups: groups, Record: record}
	if len(rc.routes) > 0 {
		d.plan = plan
	}
	return d
}

// fallback is static routing: every candidate gets the review, and the
// notifications keep today's behavior.
func (rc *routeContext) fallback(cause, detail string) *RouteDecision {
	record := rc.record(models.ReviewRoutingModeStatic, rc.candidates)
	record.FallbackCause = cause
	record.FallbackDetail = boundText(detail, maxDetailRunes)
	log.With("org", rc.in.OrgID, "sidecar", rc.in.SidecarID, "listener", rc.in.ListenerName,
		"cause", cause).Infof("approval routing fell back to static routing: %s", record.FallbackDetail)
	return &RouteDecision{Groups: slices.Clone(rc.candidates), Record: record}
}

// record starts the stored decision.
func (rc *routeContext) record(mode string, groups []string) *models.ReviewRouting {
	return &models.ReviewRouting{
		Mode:         mode,
		Candidates:   slices.Clone(rc.candidates),
		Groups:       slices.Clone(groups),
		Integrations: slices.Clone(rc.integrations),
		DecidedAt:    time.Now().UTC(),
	}
}

// boundText strips control characters, folds whitespace and cuts s to max
// runes.
func boundText(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}

// normalizeNames trims names and drops blanks and repeats, keeping order.
func normalizeNames(names []string) []string {
	var out []string
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n != "" && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// defaultGroups is the group a review goes to when the org has none.
func defaultGroups() []string {
	return []string{types.GroupAdmin}
}

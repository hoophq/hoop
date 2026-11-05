package apisidecar

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	laia "github.com/hoophq/hoop/common/aianalyzer"
	"github.com/hoophq/hoop/gateway/api/openapi"
	reviewapi "github.com/hoophq/hoop/gateway/api/review"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/services/approvalrouting"
	slackservice "github.com/hoophq/hoop/gateway/slack"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	slackplugin "github.com/hoophq/hoop/gateway/transport/plugins/slack"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// routingCaps are the capabilities of a build that reads routing.
const routingCaps = daemon.CapabilityLevelApprovers + "," + daemon.CapabilityApprovalRouting

// fakeLLM answers every routing call with resp or err and records the
// requests.
type fakeLLM struct {
	mu    sync.Mutex
	reqs  []laia.ChatRequest
	resp  *laia.ChatResponse
	err   error
	delay time.Duration
}

func (f *fakeLLM) Chat(ctx context.Context, req laia.ChatRequest) (*laia.ChatResponse, error) {
	time.Sleep(f.delay)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > approvalrouting.Timeout {
		return nil, errors.New("the routing call has no 20s deadline")
	}
	return f.resp, f.err
}

// sentContext decodes the routing context JSON of a request into out.
func sentContext(t *testing.T, req laia.ChatRequest, out any) {
	t.Helper()
	const head = "Routing context, as JSON:\n"
	content := req.Messages[0].Content
	start := strings.Index(content, head)
	require.GreaterOrEqual(t, start, 0, content)
	body := content[start+len(head):]
	if end := strings.Index(body, "\n\n"); end >= 0 {
		body = body[:end]
	}
	require.NoError(t, json.Unmarshal([]byte(body), out))
}

func (f *fakeLLM) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

// lastRequest is the last request as JSON: everything the provider reads.
func (f *fakeLLM) lastRequest(t *testing.T) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.reqs)
	raw, err := json.Marshal(f.reqs[len(f.reqs)-1])
	require.NoError(t, err)
	return string(raw)
}

func useLLM(t *testing.T, f *fakeLLM) {
	t.Cleanup(approvalrouting.SetClientFactory(clientOf(f)))
}

// routeCall is a route_approval tool call.
func routeCall(t *testing.T, args map[string]any) *laia.ChatResponse {
	t.Helper()
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	return &laia.ChatResponse{ToolCalls: []laia.ToolCall{{ID: "call-1", Name: "route_approval", Arguments: string(raw)}}}
}

// fakeSlack records the destination of every chat.postMessage.
type fakeSlack struct {
	mu    sync.Mutex
	posts []string
}

func (f *fakeSlack) destinations() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.posts...)
}

// useSlack registers a Slack integration for the test org with defaultChannel.
func useSlack(t *testing.T, defaultChannel string) *fakeSlack {
	t.Helper()
	f := &fakeSlack{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.posts = append(f.posts, r.FormValue("channel"))
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"channel":%q,"ts":"1.0"}`, r.FormValue("channel"))
	}))
	t.Cleanup(srv.Close)
	svc := slackservice.NewWithAPIClient(slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/")), "T1", defaultChannel)
	slackservice.SetServiceInstance(statusTestOrgID, svc)
	t.Cleanup(func() { slackservice.RemoveServiceInstance(statusTestOrgID) })
	return f
}

type seededUser struct{ ID, Name, Email string }

// seedUser stores an org user in groups. Names and emails are distinctive,
// so a test can look for them in the routing request.
func seedUser(t *testing.T, tag, status, slackID string, groups ...string) seededUser {
	t.Helper()
	u := seededUser{ID: uuid.NewString(), Name: "Name" + tag + "Quixotic", Email: "mail" + tag + "@secret-routing.example"}
	var slackValue any
	if slackID != "" {
		slackValue = slackID
	}
	require.NoError(t, models.DB.Exec(`INSERT INTO private.users (id, org_id, subject, email, name, status, slack_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, u.ID, statusTestOrgID, "sub-"+u.ID, u.Email, u.Name, status, slackValue).Error)
	for _, g := range groups {
		require.NoError(t, models.DB.Exec(`INSERT INTO private.user_groups (org_id, user_id, name) VALUES (?, ?, ?)`,
			statusTestOrgID, u.ID, g).Error)
	}
	return u
}

// routedSpec holds high for AI routing between payments and sre, with notify
// as given ("" for none).
func routedSpec(ruleName, notify string) string {
	routing := `"candidates": ["payments", "sre"]`
	if notify != "" {
		routing += `, "notify": ` + notify
	}
	return `{"high": "require_approval", "approval_rule": "` + ruleName + `",
		"approvers": {"high": {"type": "human", "routing": {` + routing + `}}}}`
}

// storedRouting reads the routing the review row holds.
func storedRouting(t *testing.T, sc *models.Sidecar, reviewID string) *models.ReviewRouting {
	t.Helper()
	rev, err := models.GetSidecarReview(models.DB, statusTestOrgID, sc.ID, reviewID)
	require.NoError(t, err)
	return rev.Routing
}

// waitNotified waits until the notify step recorded its outcome.
func waitNotified(t *testing.T, sc *models.Sidecar, reviewID string) *models.ReviewRouting {
	t.Helper()
	var r *models.ReviewRouting
	require.Eventually(t, func() bool {
		r = storedRouting(t, sc, reviewID)
		return r != nil && (len(r.NotifiedUsers) > 0 || len(r.Channels) > 0 || len(r.Unreachable) > 0)
	}, 15*time.Second, 50*time.Millisecond)
	return r
}

// getApproval runs GET /approvals/:id as an admin.
func getApproval(t *testing.T, id string) *openapi.Review {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ctx := storagev2.NewContext(uuid.NewString(), statusTestOrgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", []string{types.GroupAdmin})
	c.Set(storagev2.ContextKey, ctx)
	c.Params = gin.Params{{Key: "id", Value: id}}
	c.Request = httptest.NewRequest(http.MethodGet, "/api/approvals/"+id, nil)
	reviewapi.NewHandler(func(orgID, sid, owner, status, reason, rejectedBy string) {}).GetByIdOrSid(c)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out openapi.Review
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return &out
}

func TestSlackMaxButtonsMatchesThePlugin(t *testing.T) {
	assert.Equal(t, slackplugin.SlackMaxButtons, approvalrouting.SlackMaxButtons)
}

// The AI selects groups, users and channels. Only candidates, members of a
// selected group and allowed channels survive, and the review records it.
func TestARoutedLevelFilesToTheGroupsTheAISelects(t *testing.T) {
	startStatusTestDB(t)
	slackFake := useSlack(t, "C-DEFAULT")
	sc := seedLevelSidecar(t, "routed", "routed-rule",
		routedSpec("routed-rule", `{"slack": {"delivery": "both", "channels_candidates": ["C-PAY", "C-SRE"]}}`))
	alice := seedUser(t, "Alice", "active", "U-ALICE", "sre")
	bob := seedUser(t, "Bob", "active", "", "sre")
	carol := seedUser(t, "Carol", "active", "U-CAROL", "payments")
	dave := seedUser(t, "Dave", "inactive", "U-DAVE", "sre")

	llm := &fakeLLM{resp: routeCall(t, map[string]any{
		"groups":       []string{"sre", "not-a-candidate"},
		"notify_users": []string{alice.ID, bob.ID, carol.ID, dave.ID, sc.ID, "u-unknown"},
		"channels": []map[string]string{
			{"integration": "slack", "channel": "C-PAY"},
			{"integration": "slack", "channel": "C-OTHER"},
			{"integration": "teams", "channel": "C-SRE"},
		},
		"reason": "The sre group\x07 approved\nsimilar statements fast. " + strings.Repeat("x", 600),
	})}
	useLLM(t, llm)

	// The statement carries a name and an email; the model reads its shape.
	const statement = "UPDATE accounts SET owner = 'NameZedQuixotic' WHERE email = 'zed@secret-routing.example'"
	rec := fileLevel(sc, statement, "high")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	filed := decodeReviewResponse(t, rec)
	assert.Equal(t, []string{"sre"}, reviewGroupNames(filed.Review), "only the selected candidate gets the review")

	r := waitNotified(t, sc, filed.Review.ID)
	assert.Equal(t, models.ReviewRoutingModeAI, r.Mode)
	assert.Empty(t, r.FallbackCause)
	assert.Equal(t, []string{"payments", "sre"}, r.Candidates)
	assert.Equal(t, []string{"sre"}, r.Groups)
	assert.Len(t, []rune(r.Reason), 500)
	assert.True(t, strings.HasPrefix(r.Reason, "The sre group approved similar statements fast."), r.Reason)
	assert.Equal(t, []models.ReviewRoutingIntegration{{Name: "slack", Delivery: "both", Integrated: true}}, r.Integrations)
	// carol is not in sre, dave is inactive, the sidecar is the requester.
	assert.Equal(t, []models.ReviewRoutingUser{{ID: alice.ID, Integrations: []string{"slack"}}}, r.NotifiedUsers)
	assert.Equal(t, []models.ReviewRoutingUnreachable{{UserID: bob.ID, Integration: "slack", Cause: "no slack id"}}, r.Unreachable)
	assert.Equal(t, []models.ReviewRoutingChannel{{Integration: "slack", ID: "C-PAY"}}, r.Channels)
	// The DM goes to the slack_id; the default channel gets nothing.
	assert.ElementsMatch(t, []string{"C-PAY", "U-ALICE"}, slackFake.destinations())

	// The LLM read opaque ids only.
	req := llm.lastRequest(t)
	for _, u := range []seededUser{alice, bob, carol, dave} {
		assert.NotContains(t, req, u.Name)
		assert.NotContains(t, req, u.Email)
	}
	assert.NotContains(t, req, "secret-routing", "no email, from a user or the statement")
	assert.NotContains(t, req, "NameZedQuixotic", "no literal of the statement")
	assert.Contains(t, req, "update accounts set owner = ? where email = ?", "the statement shape is sent")
	assert.Contains(t, req, alice.ID)
	assert.Contains(t, req, carol.ID)
	assert.NotContains(t, req, dave.ID, "an inactive user is no member")
	assert.Contains(t, req, "Report your decision by calling the route_approval tool exactly once.")
	assert.Contains(t, req, "Do not quote a literal value from the statement.")
	assert.Contains(t, req, "untrusted data")

	// Only the routed group approves.
	assert.Equal(t, http.StatusNotFound, putReview(t, filed.Review.ID, `{"status":"approved"}`, []string{"payments"}).Code)
	got := getApproval(t, filed.Review.ID)
	require.NotNil(t, got.Routing)
	require.Len(t, got.Routing.NotifiedUsers, 1)
	assert.Equal(t, alice.Name, got.Routing.NotifiedUsers[0].Name, "the API resolves the name for display")
	assert.Equal(t, alice.Email, got.Routing.NotifiedUsers[0].Email)
	require.Len(t, got.Routing.Unreachable, 1)
	assert.Equal(t, bob.Name, got.Routing.Unreachable[0].Name)
	assert.Equal(t, http.StatusOK, putReview(t, filed.Review.ID, `{"status":"approved"}`, []string{"sre"}).Code)

	// A retry of the same bytes answers from the live review and does not
	// route again.
	retry := decodeReviewResponse(t, fileLevel(sc, statement, "high"))
	assert.Equal(t, filed.Review.ID, retry.Review.ID)
	assert.Equal(t, 1, llm.calls())
}

// The requester is never selected, and the model never sees them as a member.
func TestTheRequesterIsNeverRouted(t *testing.T) {
	startStatusTestDB(t)
	requester := seedUser(t, "Req", "active", "U-REQ", "sre")
	other := seedUser(t, "Other", "active", "U-OTHER", "sre")
	llm := &fakeLLM{resp: routeCall(t, map[string]any{
		"groups": []string{"sre"}, "notify_users": []string{requester.ID, other.ID},
		"channels": []any{}, "reason": "r"})}
	useLLM(t, llm)

	d, err := approvalrouting.RouteReview(context.Background(), models.DB, approvalrouting.RouteInput{
		OrgID: statusTestOrgID, RequesterID: requester.ID, RequesterGroups: []string{"sre"},
		ListenerName: "appdb", Statement: "DELETE FROM t",
		Routing: daemon.ApprovalRouting{Candidates: []string{"sre"},
			Notify: map[string]daemon.NotifyRoute{"slack": {Delivery: "dm"}}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{other.ID}, d.Users())
	var sent struct {
		Users []struct{ ID string } `json:"users"`
	}
	sentContext(t, llm.reqs[0], &sent)
	for _, u := range sent.Users {
		assert.NotEqual(t, requester.ID, u.ID)
	}
}

// A channel on a dm integration is dropped, and a key that is not
// integrated is recorded as such: its selected users are unreachable.
func TestAChannelOnADMIntegrationIsDroppedAndTeamsIsNotIntegrated(t *testing.T) {
	startStatusTestDB(t)
	slackFake := useSlack(t, "C-DEFAULT")
	sc := seedLevelSidecar(t, "dm-only", "dm-rule", routedSpec("dm-rule",
		`{"slack": {"delivery": "dm"}, "teams": {"delivery": "both", "channels_candidates": ["19:abc@thread.tacv2"]}}`))
	alice := seedUser(t, "Alice", "active", "U-ALICE", "sre")
	llm := &fakeLLM{resp: routeCall(t, map[string]any{
		"groups": []string{"sre"}, "notify_users": []string{alice.ID},
		"channels": []map[string]string{{"integration": "slack", "channel": "C-DEFAULT"},
			{"integration": "teams", "channel": "19:abc@thread.tacv2"}},
		"reason": "r"})}
	useLLM(t, llm)

	filed := decodeReviewResponse(t, fileLevel(sc, "DELETE FROM payments WHERE id = 7", "high"))
	r := waitNotified(t, sc, filed.Review.ID)
	assert.Empty(t, r.Channels)
	assert.Equal(t, []models.ReviewRoutingIntegration{
		{Name: "slack", Delivery: "dm", Integrated: true},
		{Name: "teams", Delivery: "both", Integrated: false}}, r.Integrations)
	assert.Equal(t, []models.ReviewRoutingUser{{ID: alice.ID, Integrations: []string{"slack"}}}, r.NotifiedUsers)
	assert.Equal(t, []models.ReviewRoutingUnreachable{
		{UserID: alice.ID, Integration: "teams", Cause: "teams is not integrated"}}, r.Unreachable)
	assert.Equal(t, []string{"U-ALICE"}, slackFake.destinations())
	assert.NotContains(t, llm.lastRequest(t), "19:abc@thread.tacv2", "a channel of an integration that is not set up is not offered")
}

// Each failure falls back to static routing: every candidate gets the
// review, and the cause is recorded.
func TestEveryFallbackCauseFilesToEveryCandidate(t *testing.T) {
	startStatusTestDB(t)
	sc := seedLevelSidecar(t, "fallback", "fallback-rule", routedSpec("fallback-rule", ""))
	for i, tc := range []struct {
		cause   string
		factory approvalrouting.ClientFactory
		detail  string
	}{
		{cause: approvalrouting.CauseNoProvider, factory: failingFactory(gorm.ErrRecordNotFound)},
		{cause: approvalrouting.CauseProviderError, factory: failingFactory(errors.New("bad key"))},
		{cause: approvalrouting.CauseTimeout, factory: clientOf(&fakeLLM{err: context.DeadlineExceeded})},
		{cause: approvalrouting.CauseProviderError, factory: clientOf(&fakeLLM{err: errors.New("503")})},
		{cause: approvalrouting.CauseNoToolCall, factory: clientOf(&fakeLLM{resp: &laia.ChatResponse{}})},
		{cause: approvalrouting.CauseNoToolCall, factory: clientOf(&fakeLLM{resp: &laia.ChatResponse{ToolCalls: []laia.ToolCall{
			{Name: "route_approval", Arguments: `{}`}, {Name: "route_approval", Arguments: `{}`}}}})},
		{cause: approvalrouting.CauseRefusal, factory: clientOf(&fakeLLM{resp: &laia.ChatResponse{Content: "I cannot route this."}})},
		// An explicit provider refusal, with no content and no tool call.
		{cause: approvalrouting.CauseRefusal, detail: "the model stopped with stop_reason refusal",
			factory: clientOf(&fakeLLM{resp: &laia.ChatResponse{Refusal: "the model stopped with stop_reason refusal"}})},
		{cause: approvalrouting.CauseInvalidDecision, factory: clientOf(&fakeLLM{resp: &laia.ChatResponse{ToolCalls: []laia.ToolCall{
			{Name: "route_approval", Arguments: `{not json`}}}})},
		// Every field of the schema is required.
		{cause: approvalrouting.CauseInvalidDecision, detail: "notify_users", factory: clientOf(&fakeLLM{
			resp: routeCall(t, map[string]any{"groups": []string{"sre"}})})},
		{cause: approvalrouting.CauseInvalidDecision, detail: "reason", factory: clientOf(&fakeLLM{
			resp: routeCall(t, map[string]any{"groups": []string{"sre"}, "notify_users": []string{}, "channels": []any{}})})},
		{cause: approvalrouting.CauseInvalidDecision, detail: "channels", factory: clientOf(&fakeLLM{
			resp: routeCall(t, map[string]any{"groups": []string{"sre"}, "notify_users": []string{}, "channels": nil, "reason": "r"})})},
		{cause: approvalrouting.CauseInvalidDecision, detail: "blank reason", factory: clientOf(&fakeLLM{
			resp: routeCall(t, map[string]any{"groups": []string{"sre"}, "notify_users": []string{}, "channels": []any{},
				"reason": " \x07\n "})})},
		{cause: approvalrouting.CauseNoValidGroup, factory: clientOf(&fakeLLM{resp: routeCall(t, map[string]any{
			"groups": []string{"nope"}, "notify_users": []string{}, "channels": []any{}, "reason": "r"})})},
	} {
		restore := approvalrouting.SetClientFactory(tc.factory)
		rec := fileLevel(sc, fmt.Sprintf("DELETE FROM payments WHERE id = %d", 100+i), "high")
		restore()
		require.Equal(t, http.StatusCreated, rec.Code, "%s: %s", tc.cause, rec.Body.String())
		filed := decodeReviewResponse(t, rec)
		assert.Equal(t, []string{"payments", "sre"}, reviewGroupNames(filed.Review), tc.cause)
		r := storedRouting(t, sc, filed.Review.ID)
		require.NotNil(t, r, tc.cause)
		assert.Equal(t, models.ReviewRoutingModeStatic, r.Mode, tc.cause)
		assert.Equal(t, tc.cause, r.FallbackCause)
		assert.NotEmpty(t, r.FallbackDetail, tc.cause)
		assert.LessOrEqual(t, len([]rune(r.FallbackDetail)), 300)
		assert.Contains(t, r.FallbackDetail, tc.detail, tc.cause)
		assert.Equal(t, []string{"payments", "sre"}, r.Groups, tc.cause)
	}
}

func clientOf(f *fakeLLM) approvalrouting.ClientFactory {
	return func(context.Context, *gorm.DB, string) (laia.LLMClient, error) { return f, nil }
}

func failingFactory(err error) approvalrouting.ClientFactory {
	return func(context.Context, *gorm.DB, string) (laia.LLMClient, error) { return nil, err }
}

// Without notify the review is posted as before routing: to the listener's
// channels or the default channel, with the routed groups' buttons.
func TestRoutingWithoutNotifyKeepsTheSlackChannelPost(t *testing.T) {
	startStatusTestDB(t)
	slackFake := useSlack(t, "C-DEFAULT")
	sc := seedLevelSidecar(t, "no-notify", "no-notify-rule", routedSpec("no-notify-rule", ""))
	alice := seedUser(t, "Alice", "active", "U-ALICE", "sre")
	useLLM(t, &fakeLLM{resp: routeCall(t, map[string]any{
		"groups": []string{"sre"}, "notify_users": []string{alice.ID}, "channels": []any{}, "reason": "r"})})

	filed := decodeReviewResponse(t, fileLevel(sc, "DELETE FROM payments WHERE id = 9", "high"))
	assert.Equal(t, []string{"sre"}, reviewGroupNames(filed.Review))
	require.Eventually(t, func() bool { return len(slackFake.destinations()) == 1 }, 10*time.Second, 50*time.Millisecond)
	assert.Equal(t, []string{"C-DEFAULT"}, slackFake.destinations(), "no direct message without notify")
	r := storedRouting(t, sc, filed.Review.ID)
	require.NotNil(t, r)
	assert.Equal(t, models.ReviewRoutingModeAI, r.Mode)
	assert.Empty(t, r.NotifiedUsers)
}

// routing: {} is AI routing over every group of the org. The plane keeps the
// empty object through compose and serve, and refuses it to a build that
// cannot read it instead of downgrading it.
func TestAnEmptyRoutingRoutesOverEveryGroupAndIsNeverDowngraded(t *testing.T) {
	startStatusTestDB(t)
	spec := `{"high": "require_approval", "approval_rule": "empty-rule",
		"approvers": {"high": {"type": "human", "routing": {}}}}`
	sc := seedLevelSidecar(t, "empty-routing", "empty-rule", spec)
	seedUser(t, "A", "active", "", "sre")
	seedUser(t, "B", "active", "", "dba", "payments")
	llm := &fakeLLM{resp: routeCall(t, map[string]any{
		"groups": []string{"dba"}, "notify_users": []string{}, "channels": []any{}, "reason": "r"})}
	useLLM(t, llm)

	w := handshake(t, sc, routingCaps)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"routing":{}`)
	for _, caps := range []string{"", daemon.CapabilityLevelApprovers} {
		w = handshake(t, sc, caps)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code, "caps %q: %s", caps, w.Body.String())
		assert.Contains(t, w.Body.String(), daemon.CapabilityApprovalRouting, "caps %q", caps)
	}

	filed := decodeReviewResponse(t, fileLevel(sc, "DELETE FROM payments WHERE id = 11", "high"))
	assert.Equal(t, []string{"dba"}, reviewGroupNames(filed.Review))
	r := storedRouting(t, sc, filed.Review.ID)
	require.NotNil(t, r)
	assert.Equal(t, []string{"dba", "payments", "sre"}, r.Candidates, "empty candidates: every group of the org")

	// The fallback files to every group too.
	restore := approvalrouting.SetClientFactory(failingFactory(gorm.ErrRecordNotFound))
	defer restore()
	filed = decodeReviewResponse(t, fileLevel(sc, "DELETE FROM payments WHERE id = 12", "high"))
	assert.Equal(t, []string{"dba", "payments", "sre"}, reviewGroupNames(filed.Review))
}

// seedHistoryReview stores a decided review of listener on sc, created at
// createdAt, whose group was decided by owner (nil: nobody answered).
func seedHistoryReview(t *testing.T, sc *models.Sidecar, listener, group string, owner *string,
	groupStatus, status models.ReviewStatusType, createdAt time.Time) {
	t.Helper()
	sessionID := uuid.NewString()
	statement := "DELETE FROM t WHERE id = '" + sessionID + "'"
	var reviewedAt *time.Time
	if owner != nil {
		at := createdAt.Add(42 * time.Second)
		reviewedAt = &at
	}
	rev := &models.Review{
		ID: uuid.NewString(), OrgID: statusTestOrgID, Type: models.ReviewTypeOneTime,
		Status: models.ReviewStatusPending, SessionID: sessionID,
		SidecarID:     nullString(sc.ID),
		ListenerName:  nullString(listener),
		StatementHash: nullString(models.HashStatement([]byte(statement))),
		RiskLevel:     nullString("high"),
		OwnerID:       sc.ID, OwnerEmail: reviewOwnerEmail, CreatedAt: createdAt,
		ReviewGroups: []models.ReviewGroups{{ID: uuid.NewString(), OrgID: statusTestOrgID, GroupName: group,
			Status: groupStatus, OwnerID: owner, ReviewedAt: reviewedAt}},
	}
	sess := models.Session{ID: sessionID, OrgID: statusTestOrgID, BlobInput: models.BlobInputType(statement),
		ConnectionType: reviewConnectionType, Verb: "exec", Status: "done", UserID: sc.ID, UserName: sc.Name,
		UserEmail: reviewOwnerEmail, CreatedAt: createdAt}
	_, err := models.CreateSidecarReview(models.DB, sess, rev, statement)
	require.NoError(t, err)
	require.NoError(t, models.UpdateReviewStatus(statusTestOrgID, rev.ID, status))
}

// The history is this sidecar and listener only: the last 100 decided
// reviews of the last 90 days.
func TestTheHistoryIsThisListenersLast100DecidedReviews(t *testing.T) {
	startStatusTestDB(t)
	sc := seedLevelSidecar(t, "history", "history-rule", routedSpec("history-rule", ""))
	other := seedStatusSidecar(t, "other-sidecar")
	alice := seedUser(t, "Alice", "active", "", "sre")
	// A local-auth reviewer: review_groups.owner_id holds the subject, which
	// is the email.
	carol := seedUser(t, "Carol", "active", "", "sre")
	require.NoError(t, models.DB.Exec(`UPDATE private.users SET subject = email WHERE id = ?`, carol.ID).Error)
	ghost := "ghost@secret-routing.example"
	now := time.Now().UTC()
	for i := range 103 {
		seedHistoryReview(t, sc, "appdb", "sre", &alice.ID, models.ReviewStatusApproved,
			models.ReviewStatusApproved, now.Add(-time.Duration(i+1)*time.Hour))
	}
	seedHistoryReview(t, sc, "appdb", "old-group", &alice.ID, models.ReviewStatusApproved,
		models.ReviewStatusApproved, now.Add(-95*24*time.Hour))
	seedHistoryReview(t, sc, "otherdb", "other-listener-group", &alice.ID, models.ReviewStatusApproved,
		models.ReviewStatusApproved, now.Add(-time.Minute))
	seedHistoryReview(t, other, "appdb", "other-sidecar-group", &alice.ID, models.ReviewStatusApproved,
		models.ReviewStatusApproved, now.Add(-time.Minute))
	seedHistoryReview(t, sc, "appdb", "expired-group", nil, models.ReviewStatusPending,
		models.ReviewStatusExpired, now.Add(-time.Minute))
	seedHistoryReview(t, sc, "appdb", "carol-group", &carol.Email, models.ReviewStatusRejected,
		models.ReviewStatusRejected, now.Add(-30*time.Second))
	seedHistoryReview(t, sc, "appdb", "ghost-group", &ghost, models.ReviewStatusApproved,
		models.ReviewStatusApproved, now.Add(-20*time.Second))

	llm := &fakeLLM{resp: routeCall(t, map[string]any{
		"groups": []string{"sre"}, "notify_users": []string{}, "channels": []any{}, "reason": "r"})}
	useLLM(t, llm)
	rec := fileLevel(sc, "DELETE FROM payments WHERE id = 13", "high")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	content := llm.reqs[0].Messages[0].Content
	var sent struct {
		History []struct {
			ReviewerID string `json:"reviewer_id"`
			Group      string `json:"group"`
			Decision   string `json:"decision"`
			Seconds    *int64 `json:"response_time_seconds"`
			RiskLevel  string `json:"risk_level"`
		} `json:"approval_history"`
	}
	sentContext(t, llm.reqs[0], &sent)
	require.Len(t, sent.History, 100)
	assert.Equal(t, "ghost-group", sent.History[0].Group, "newest first")
	assert.Empty(t, sent.History[0].ReviewerID, "a subject with no user keeps the entry without an id")
	assert.Equal(t, "approved", sent.History[0].Decision)
	assert.Equal(t, "carol-group", sent.History[1].Group)
	assert.Equal(t, carol.ID, sent.History[1].ReviewerID, "the subject maps to the user id")
	assert.Equal(t, "rejected", sent.History[1].Decision)
	assert.Equal(t, "expired-group", sent.History[2].Group)
	assert.Equal(t, "no_response", sent.History[2].Decision)
	assert.Empty(t, sent.History[2].ReviewerID)
	assert.Equal(t, alice.ID, sent.History[3].ReviewerID)
	assert.Equal(t, "approved", sent.History[3].Decision)
	require.NotNil(t, sent.History[3].Seconds)
	assert.Equal(t, int64(42), *sent.History[3].Seconds)
	assert.Equal(t, "high", sent.History[3].RiskLevel)
	assert.NotContains(t, content, carol.Email)
	assert.NotContains(t, content, "secret-routing")
	for _, excluded := range []string{"old-group", "other-listener-group", "other-sidecar-group"} {
		assert.NotContains(t, content, excluded)
	}
}

// GET /approvals/routing/integrations lists the Slack channels of every
// listener and the default channel; Teams is not integrated.
func TestTheRoutingIntegrations(t *testing.T) {
	startStatusTestDB(t)
	useSlack(t, "C-DEFAULT")
	sc := seedStatusSidecar(t, "integrations")
	require.NoError(t, models.SetSidecarSlackChannels(models.DB, statusTestOrgID, sc.ID, []models.SidecarSlackChannels{
		{ListenerName: "a", Channels: []string{"C-A", "C-DEFAULT"}}, {ListenerName: "b", Channels: []string{"C-B"}}}))

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set(storagev2.ContextKey, storagev2.NewContext(uuid.NewString(), statusTestOrgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", []string{types.GroupAdmin}))
	c.Request = httptest.NewRequest(http.MethodGet, "/api/approvals/routing/integrations", nil)
	reviewapi.GetRoutingIntegrations(c)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got openapi.ApprovalRoutingIntegrations
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.True(t, got.Slack.Integrated)
	assert.ElementsMatch(t, []openapi.ApprovalRoutingChannel{
		{ID: "C-A", Name: "C-A"}, {ID: "C-DEFAULT", Name: "C-DEFAULT"}, {ID: "C-B", Name: "C-B"}}, got.Slack.Channels)
	assert.False(t, got.Teams.Integrated)
	assert.Empty(t, got.Teams.Channels)
}

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

// The principal names the requester: the member it names is never selected
// nor notified, even when the model names them, and the model reads the
// member's opaque id as the requester.
func TestThePrincipalIsNeverRoutedNorNotified(t *testing.T) {
	startStatusTestDB(t)
	slackFake := useSlack(t, "C-DEFAULT")
	sc := seedLevelSidecar(t, "principal", "principal-rule", routedSpec("principal-rule", `{"slack": {"delivery": "dm"}}`))
	requester := seedUser(t, "Req", "active", "U-REQ", "sre", "payments")
	other := seedUser(t, "Other", "active", "U-OTHER", "sre")
	llm := &fakeLLM{resp: routeCall(t, map[string]any{
		"groups": []string{"sre"}, "notify_users": []string{requester.ID, other.ID},
		"channels": []any{}, "reason": "r"})}
	useLLM(t, llm)

	// The email as the sidecar saw it, in another case.
	rec := postLevelBody(sc, "DELETE FROM payments WHERE id = 21", map[string]any{
		"risk_level": "high", "principal": strings.ToUpper(requester.Email)})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	filed := decodeReviewResponse(t, rec)
	r := waitNotified(t, sc, filed.Review.ID)
	assert.Equal(t, []models.ReviewRoutingUser{{ID: other.ID, Integrations: []string{"slack"}}}, r.NotifiedUsers)
	assert.Empty(t, r.Unreachable)
	assert.Equal(t, []string{"U-OTHER"}, slackFake.destinations())

	var sent struct {
		Request struct {
			RequesterID     string   `json:"requester_id"`
			RequesterGroups []string `json:"requester_groups"`
		} `json:"request"`
		Users []struct{ ID string } `json:"users"`
	}
	sentContext(t, llm.reqs[0], &sent)
	assert.Equal(t, requester.ID, sent.Request.RequesterID)
	assert.Equal(t, []string{"payments", "sre"}, sent.Request.RequesterGroups)
	for _, u := range sent.Users {
		assert.NotEqual(t, requester.ID, u.ID)
	}
	assert.NotContains(t, llm.lastRequest(t), requester.Email)

	// A principal longer than 256 characters is refused.
	rec = postLevelBody(sc, "DELETE FROM payments WHERE id = 22", map[string]any{
		"risk_level": "high", "principal": strings.Repeat("p", 257)})
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
}

// Notify resolves the principal again: a user it names by then gets no
// direct message, even though the decision selected them.
func TestNotifyDropsAUserThePrincipalNamesAtPostTime(t *testing.T) {
	startStatusTestDB(t)
	slackFake := useSlack(t, "C-DEFAULT")
	sc := seedLevelSidecar(t, "late", "late-rule", routedSpec("late-rule", `{"slack": {"delivery": "dm"}}`))
	alice := seedUser(t, "Alice", "active", "U-ALICE", "sre")
	bob := seedUser(t, "Bob", "active", "U-BOB", "sre")
	useLLM(t, &fakeLLM{resp: routeCall(t, map[string]any{
		"groups": []string{"sre"}, "notify_users": []string{alice.ID, bob.ID}, "channels": []any{}, "reason": "r"})})

	const principal = "late-principal@example.com"
	d, err := approvalrouting.RouteReview(context.Background(), models.DB, approvalrouting.RouteInput{
		OrgID: statusTestOrgID, Principal: principal, SidecarID: sc.ID, ListenerName: "appdb",
		Routing: daemon.ApprovalRouting{Candidates: []string{"sre"},
			Notify: map[string]daemon.NotifyRoute{"slack": {Delivery: "dm"}}}})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{alice.ID, bob.ID}, d.Users())

	require.NoError(t, models.DB.Exec(`UPDATE private.users SET email = ? WHERE id = ?`, principal, alice.ID).Error)
	rev := seedStatusReview(t, sc, models.ReviewStatusPending)
	approvalrouting.Notify(context.Background(), models.DB, statusTestOrgID, d,
		approvalrouting.ReviewCard{ReviewID: rev.ID, SessionID: rev.SessionID, Groups: []string{"sre"}})
	assert.Equal(t, []string{"U-BOB"}, slackFake.destinations())
	r := storedRouting(t, sc, rev.ID)
	require.NotNil(t, r)
	assert.Equal(t, []models.ReviewRoutingUser{{ID: bob.ID, Integrations: []string{"slack"}}}, r.NotifiedUsers)
}

// Routing time counts inside the hold: the deadline runs from the filing,
// not from the end of routing.
func TestTheDeadlineCountsFromTheFilingNotFromRouting(t *testing.T) {
	startStatusTestDB(t)
	org := uuid.MustParse(statusTestOrgID)
	sc := seedLevelSidecar(t, "deadline", "deadline-rule", routedSpec("deadline-rule", ""))
	pending := 900
	require.NoError(t, services.ApplyAnalyzerApprovalTTLs(models.DB, org, "deadline-rule", &pending, nil))
	useLLM(t, &fakeLLM{delay: 2 * time.Second, resp: routeCall(t, map[string]any{
		"groups": []string{"sre"}, "notify_users": []string{}, "channels": []any{}, "reason": "r"})})

	start := time.Now().UTC()
	rec := fileLevel(sc, "DELETE FROM payments WHERE id = 31", "high")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	filed := decodeReviewResponse(t, rec)
	require.NotNil(t, filed.Review.ExpiresAt)
	want := start.Add(15 * time.Minute)
	assert.False(t, filed.Review.ExpiresAt.Before(want.Add(-time.Second)), "expires_at %s, filing %s", filed.Review.ExpiresAt, start)
	assert.True(t, filed.Review.ExpiresAt.Before(want.Add(time.Second)),
		"the 2s of routing must not move the deadline: expires_at %s, filing %s", filed.Review.ExpiresAt, start)
}

// blockQueries holds every query match selects until its context ends, as a
// slow database would. It counts the queries counted selects.
func blockQueries(t *testing.T, match, counted func(sql, table string) bool) *int {
	t.Helper()
	var mu sync.Mutex
	count := new(int)
	hook := func(db *gorm.DB) {
		sql, table := db.Statement.SQL.String(), db.Statement.Table
		if counted != nil && counted(sql, table) {
			mu.Lock()
			*count++
			mu.Unlock()
		}
		done := db.Statement.Context.Done()
		if done == nil || !match(sql, table) {
			return
		}
		<-done
		_ = db.AddError(db.Statement.Context.Err())
	}
	name := "test:block-" + uuid.NewString()
	require.NoError(t, models.DB.Callback().Query().Before("gorm:query").Register(name, hook))
	require.NoError(t, models.DB.Callback().Row().Before("gorm:row").Register(name, hook))
	t.Cleanup(func() {
		_ = models.DB.Callback().Query().Remove(name)
		_ = models.DB.Callback().Row().Remove(name)
	})
	return count
}

const candidatesQuery = "SELECT DISTINCT name FROM private.user_groups"

// The provider lookup runs inside the routing limit: a slow one falls back
// with timeout, in about the limit.
func TestASlowProviderLookupFallsBackWithinTheLimit(t *testing.T) {
	startStatusTestDB(t)
	t.Cleanup(approvalrouting.SetTimeout(500 * time.Millisecond))
	// gorm keeps the schema apart: the table of private.ai_providers reads ai_providers.
	blockQueries(t, func(_, table string) bool { return table == "ai_providers" }, nil)

	start := time.Now()
	d, err := approvalrouting.RouteReview(context.Background(), models.DB, approvalrouting.RouteInput{
		OrgID: statusTestOrgID, ListenerName: "appdb", Routing: daemon.ApprovalRouting{Candidates: []string{"sre"}}})
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 3*time.Second)
	assert.Equal(t, approvalrouting.CauseTimeout, d.Record.FallbackCause)
	assert.Equal(t, []string{"sre"}, d.Groups)
}

// A context read slower than the limit, after the candidates were read,
// falls back to those candidates without reading them again.
func TestASlowContextFallsBackToTheCandidatesReadFirst(t *testing.T) {
	startStatusTestDB(t)
	t.Cleanup(approvalrouting.SetTimeout(500 * time.Millisecond))
	seedUser(t, "A", "active", "", "sre")
	seedUser(t, "B", "active", "", "dba")
	useLLM(t, &fakeLLM{})
	candidateReads := blockQueries(t,
		func(sql, _ string) bool {
			return strings.Contains(sql, "JOIN private.user_groups ug ON ug.user_id = u.id")
		},
		func(sql, _ string) bool { return strings.Contains(sql, candidatesQuery) })

	start := time.Now()
	d, err := approvalrouting.RouteReview(context.Background(), models.DB, approvalrouting.RouteInput{
		OrgID: statusTestOrgID, ListenerName: "appdb", Routing: daemon.ApprovalRouting{}})
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 3*time.Second)
	assert.Equal(t, approvalrouting.CauseTimeout, d.Record.FallbackCause)
	assert.Equal(t, []string{"dba", "sre"}, d.Groups)
	assert.Equal(t, 1, *candidateReads, "the fallback reads the candidates no second time")
}

// Candidates that cannot be read inside the limit are an error, in about
// the limit: the filing answers 500 instead of waiting.
func TestCandidatesSlowerThanTheLimitAreAnError(t *testing.T) {
	startStatusTestDB(t)
	t.Cleanup(approvalrouting.SetTimeout(500 * time.Millisecond))
	blockQueries(t, func(sql, _ string) bool { return strings.Contains(sql, candidatesQuery) }, nil)

	start := time.Now()
	_, err := approvalrouting.RouteReview(context.Background(), models.DB, approvalrouting.RouteInput{
		OrgID: statusTestOrgID, ListenerName: "appdb", Routing: daemon.ApprovalRouting{}})
	require.Error(t, err)
	assert.Less(t, time.Since(start), 3*time.Second)
}

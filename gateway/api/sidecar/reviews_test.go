package apisidecar

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/aws/smithy-go/ptr"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	// With a bare origin the two url accessors return the same string, so the
	// WebappURL assertion below would hold whichever one the code calls. The
	// path prefix is what makes it mean something.
	os.Setenv("API_URL", "http://localhost:8009/hoop")
	if err := appconfig.Load(); err != nil {
		panic(err)
	}
	os.Exit(pglitetest.Main(m))
}

// approvalRule is the policy every test here files against: two reviewer
// groups and a minimum of one, so a drift in either half is visible.
func approvalRule() *models.AccessRequestRule {
	return &models.AccessRequestRule{
		Name:                "payments-approvers",
		AccessType:          models.AccessTypeSidecar,
		ReviewersGroups:     []string{"dba", "sre"},
		ForceApprovalGroups: []string{"security"},
		MinApprovals:        ptr.Int(1),
	}
}

// sidecarWithListener builds the stored configuration the authorization reads.
// It is the sidecar row the middleware loads, not anything the request sent.
func sidecarWithListener(listenerName, ruleName string) *models.Sidecar {
	sc := &models.Sidecar{ID: "sidecar-1", OrgID: "org-1", Name: "payments-sidecar"}
	listener := daemon.ListenerConfig{Name: listenerName}
	if ruleName != "" {
		listener.Analyzer = &daemon.LaneAnalyzerConfig{ApprovalRule: ruleName}
	}
	sc.Configuration = models.SidecarConfiguration{Listeners: []daemon.ListenerConfig{listener}}
	return sc
}

// testStatementHash is what the handler derives for the statement under test.
// Built through hashStatement rather than written out, so the fixture cannot
// drift from what the handler would actually store.
var testStatementHash = models.HashStatement([]byte("DELETE FROM users WHERE id = 1;"))

func testPolicy(t *testing.T, rule *models.AccessRequestRule) *services.ReviewPolicy {
	t.Helper()
	policy, err := services.ReviewPolicyFromRule("org-1", rule)
	assert.NoError(t, err)
	return policy
}

// The rule is the whole approval policy and nothing downstream restates it.
// Without MinApprovals reviewsCountNeeded falls back to the number of group
// rows, so the review would quietly need every reviewer group.
func TestNewSidecarReviewCarriesTheWholePolicy(t *testing.T) {
	sc := sidecarWithListener("appdb", "payments-approvers")
	rule := approvalRule()

	rev := newSidecarReview(sc, "appdb", "session-1", testStatementHash, rule, testPolicy(t, rule), time.Now().UTC())

	assert.NotNil(t, rev.MinApprovals, "no minimum means every group must approve")
	assert.Equal(t, 1, *rev.MinApprovals, "the rule's minimum, not a fixed one")
	assert.Len(t, rev.ReviewGroups, 2)
	assert.ElementsMatch(t, []string{"dba", "sre"}, groupNames(rev.ReviewGroups),
		"the review is approved by the rule's groups, not by a fixed pair of roles")
	assert.Equal(t, []string{"security"}, []string(rev.ForceApprovalGroups))

	assert.NotNil(t, rev.AccessRequestRuleName, "the review records which rule held the statement")
	assert.Equal(t, "payments-approvers", *rev.AccessRequestRuleName)

	assert.Equal(t, "sidecar-1", rev.SidecarID.String)
	assert.Equal(t, "appdb", rev.ListenerName.String)
	assert.Empty(t, rev.ConnectionName, "a sidecar review resolves no connection")
	assert.Equal(t, reviewOwnerEmail, rev.OwnerEmail)
	assert.Equal(t, models.ReviewStatusPending, rev.Status)
	assert.Equal(t, models.ReviewTypeOneTime, rev.Type)
}

// AccessRequestRuleName is load bearing beyond bookkeeping: doIndividualReview
// only reads MinApprovals when the review carries a rule name or has no
// connection, and review.go only reads ForceApprovalGroups from the review
// when the rule name is set.
func TestNewSidecarReviewNamesTheRuleSoTheMinimumIsRead(t *testing.T) {
	sc := sidecarWithListener("appdb", "payments-approvers")
	rule := approvalRule()

	rev := newSidecarReview(sc, "appdb", "session-1", testStatementHash, rule, testPolicy(t, rule), time.Now().UTC())

	assert.NotNil(t, rev.AccessRequestRuleName,
		"without the rule name the review needs every group and ignores its force approval list")
}

// Without the hash on the row the partial unique index covers nothing, so every
// retry files a fresh review and the approval is never reachable.
func TestNewSidecarReviewCarriesTheStatementHash(t *testing.T) {
	sc := sidecarWithListener("appdb", "payments-approvers")
	rule := approvalRule()

	rev := newSidecarReview(sc, "appdb", "session-1", testStatementHash, rule, testPolicy(t, rule), time.Now().UTC())

	assert.True(t, rev.StatementHash.Valid, "a NULL hash is excluded from the unique index")
	assert.Equal(t, testStatementHash, rev.StatementHash.String)
}

// Authorization comes from the sidecar's stored configuration, never from the
// body: a request could otherwise name any rule in the organization and pick
// its own approvers.
func TestListenerNamesApprovalRule(t *testing.T) {
	for _, tc := range []struct {
		name         string
		sidecar      *models.Sidecar
		listenerName string
		ruleName     string
		want         bool
	}{
		{
			name:         "the listener names this rule",
			sidecar:      sidecarWithListener("appdb", "payments-approvers"),
			listenerName: "appdb",
			ruleName:     "payments-approvers",
			want:         true,
		},
		{
			name:         "the listener names another rule",
			sidecar:      sidecarWithListener("appdb", "reporting-approvers"),
			listenerName: "appdb",
			ruleName:     "payments-approvers",
		},
		{
			name:         "the stored configuration has no such listener",
			sidecar:      sidecarWithListener("appdb", "payments-approvers"),
			listenerName: "reporting",
			ruleName:     "payments-approvers",
		},
		{
			name:         "the listener has no analyzer block",
			sidecar:      sidecarWithListener("appdb", ""),
			listenerName: "appdb",
			ruleName:     "payments-approvers",
		},
		{
			name:         "the stored configuration has no listeners at all",
			sidecar:      &models.Sidecar{ID: "sidecar-1", OrgID: "org-1"},
			listenerName: "appdb",
			ruleName:     "payments-approvers",
		},
		{
			name:         "an empty rule name authorizes nothing",
			sidecar:      sidecarWithListener("appdb", ""),
			listenerName: "appdb",
			ruleName:     "",
		},
		{
			name:         "an empty listener name authorizes nothing",
			sidecar:      sidecarWithListener("", "payments-approvers"),
			listenerName: "",
			ruleName:     "payments-approvers",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := listenerNamesApprovalRule(tc.sidecar.Configuration.Listeners, tc.listenerName, tc.ruleName)
			assert.Equal(t, tc.want, got)
		})
	}
}

// An unauthorized listener is refused before the rule is loaded, so the
// refusal costs no query and the message is the same whether or not the rule
// exists. Reaching the database would need Postgres; this pins the branch that
// answers 422 without one.
func TestAuthorizedApprovalRuleRefusesAnUnauthorizedListener(t *testing.T) {
	sc := sidecarWithListener("appdb", "payments-approvers")

	// A nil database is safe and deliberate here: the listener check runs
	// first, so an unauthorized listener never reaches a query.
	rule, err := authorizedApprovalRule(nil, sc, "reporting", "payments-approvers")

	assert.Nil(t, rule)
	var refusal ruleNotAuthorized
	assert.ErrorAs(t, err, &refusal)
	// The whole message, not a prefix: it is what the sidecar logs, and an
	// internal marker appended to it would read as part of the reason.
	assert.Equal(t, `listener "reporting" is not configured to use approval rule "payments-approvers"`, err.Error())
}

// sidecarWithListeners builds a stored configuration holding several lanes, so
// the duplicate-name case can be asserted.
func sidecarWithListeners(listeners ...daemon.ListenerConfig) *models.Sidecar {
	return &models.Sidecar{
		ID: "sidecar-1", OrgID: "org-1", Name: "payments-sidecar",
		Configuration: models.SidecarConfiguration{Listeners: listeners},
	}
}

func lane(name, ruleName string) daemon.ListenerConfig {
	return daemon.ListenerConfig{
		Name:     name,
		Analyzer: &daemon.LaneAnalyzerConfig{ApprovalRule: ruleName},
	}
}

// Listener names are not unique: daemon validation keys uniqueness on the
// listen address. Two lanes sharing a name may name different rules, and the
// request says which listener but not which lane, so matching either one would
// let the sidecar choose the looser of the two policies.
func TestListenerNamesApprovalRuleFailsClosedOnDuplicateNames(t *testing.T) {
	sc := sidecarWithListeners(lane("appdb", "lax-approvers"), lane("appdb", "strict-approvers"))

	assert.False(t, listenerNamesApprovalRule(sc.Configuration.Listeners, "appdb", "lax-approvers"),
		"the first lane must not authorize a statement the second lane may have held")
	assert.False(t, listenerNamesApprovalRule(sc.Configuration.Listeners, "appdb", "strict-approvers"),
		"neither direction authorizes while the name is ambiguous")

	// A second lane under another name changes nothing: the match is unique.
	sc = sidecarWithListeners(lane("appdb", "lax-approvers"), lane("reporting", "strict-approvers"))
	assert.True(t, listenerNamesApprovalRule(sc.Configuration.Listeners, "appdb", "lax-approvers"))
	assert.True(t, listenerNamesApprovalRule(sc.Configuration.Listeners, "reporting", "strict-approvers"))
}

// The control plane stores a sidecar rule without checking these fields, so a
// review filed against one would be unsettleable or would enforce a policy
// other than the one it records.
func TestApprovableSidecarRule(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rule    *models.AccessRequestRule
		wantErr string
	}{
		{
			name: "a well formed rule passes",
			rule: approvalRule(),
		},
		{
			name: "a repeated group lets one person approve twice",
			rule: &models.AccessRequestRule{
				Name:            "payments-approvers",
				ReviewersGroups: []string{"dba", "dba"},
				MinApprovals:    ptr.Int(2),
			},
			wantErr: "repeats the reviewers_groups entry",
		},
		{
			name: "a blank group can never be matched",
			rule: &models.AccessRequestRule{
				Name:            "payments-approvers",
				ReviewersGroups: []string{"dba", "  "},
			},
			wantErr: "blank entry in reviewers_groups",
		},
		{
			name: "a minimum below one is ignored at approval time",
			rule: &models.AccessRequestRule{
				Name:            "payments-approvers",
				ReviewersGroups: []string{"dba", "sre"},
				MinApprovals:    ptr.Int(0),
			},
			wantErr: "below 1",
		},
		{
			name: "a minimum above the group count is clamped at approval time",
			rule: &models.AccessRequestRule{
				Name:            "payments-approvers",
				ReviewersGroups: []string{"dba", "sre"},
				MinApprovals:    ptr.Int(3),
			},
			wantErr: "more than its 2 reviewers_groups",
		},
		{
			name: "all_groups_must_approve makes the minimum irrelevant",
			rule: &models.AccessRequestRule{
				Name:                 "payments-approvers",
				ReviewersGroups:      []string{"dba", "sre"},
				AllGroupsMustApprove: true,
				MinApprovals:         ptr.Int(0),
			},
		},
		// A stored 0 would expire every review at once; the write paths store nil.
		{name: "a stored pending limit of 0", rule: ruleWithTTLs(ptr.Int(0), nil), wantErr: "pending_ttl_sec must be between"},
		{name: "a pending limit under a minute", rule: ruleWithTTLs(ptr.Int(59), nil), wantErr: "pending_ttl_sec must be between"},
		{name: "a pending limit over a week", rule: ruleWithTTLs(ptr.Int(604801), nil), wantErr: "pending_ttl_sec must be between"},
		{name: "a stored approval limit of 0", rule: ruleWithTTLs(nil, ptr.Int(0)), wantErr: "approval_ttl_sec must be between"},
		{name: "an approval limit over a week", rule: ruleWithTTLs(nil, ptr.Int(604801)), wantErr: "approval_ttl_sec must be between"},
		{name: "no limits pass", rule: ruleWithTTLs(nil, nil)},
		{name: "the bounds pass", rule: ruleWithTTLs(ptr.Int(60), ptr.Int(604800))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := approvableSidecarRule(tc.rule)
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func ruleWithTTLs(pending, approval *int) *models.AccessRequestRule {
	rule := approvalRule()
	rule.PendingTTLSec = pending
	rule.ApprovalTTLSec = approval
	return rule
}

// The limits are fixed at filing like the rest of the policy, so a rule edit
// moves no live review.
func TestNewSidecarReviewSnapshotsTheTTLs(t *testing.T) {
	sc := sidecarWithListener("appdb", "payments-approvers")
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

	rule := ruleWithTTLs(ptr.Int(900), ptr.Int(600))
	rev := newSidecarReview(sc, "appdb", "session-1", testStatementHash, rule, testPolicy(t, rule), now)
	require.NotNil(t, rev.ExpiresAt)
	assert.Equal(t, now.Add(15*time.Minute), *rev.ExpiresAt, "the decision deadline counts from filing")
	require.NotNil(t, rev.ApprovalTTLSec, "the approval clock starts at the approval, so the limit is kept")
	assert.Equal(t, 600, *rev.ApprovalTTLSec)

	*rule.ApprovalTTLSec = 60
	assert.Equal(t, 600, *rev.ApprovalTTLSec, "a copy, not the rule's pointer")

	rule = ruleWithTTLs(nil, ptr.Int(600))
	rev = newSidecarReview(sc, "appdb", "session-1", testStatementHash, rule, testPolicy(t, rule), now)
	assert.Nil(t, rev.ExpiresAt, "no pending limit is no decision deadline")
	require.NotNil(t, rev.ApprovalTTLSec)

	rule = approvalRule()
	rev = newSidecarReview(sc, "appdb", "session-1", testStatementHash, rule, testPolicy(t, rule), now)
	assert.Nil(t, rev.ExpiresAt)
	assert.Nil(t, rev.ApprovalTTLSec)
}

func groupNames(groups []models.ReviewGroups) []string {
	names := make([]string, 0, len(groups))
	for _, rg := range groups {
		names = append(names, rg.GroupName)
	}
	return names
}

// A sidecar review has no connection. Sending the fields empty would invite a
// client to read meaning into an empty string, so they are absent instead.
func TestToOpenApiSidecarReviewOmitsConnectionFields(t *testing.T) {
	rev := &models.Review{
		ID:           "review-1",
		SessionID:    "session-1",
		Type:         models.ReviewTypeOneTime,
		Status:       models.ReviewStatusPending,
		SidecarID:    sql.NullString{String: "sidecar-1", Valid: true},
		ListenerName: sql.NullString{String: "appdb", Valid: true},

		ReviewGroups:          testPolicy(t, approvalRule()).Groups,
		AccessRequestRuleName: ptr.String("payments-approvers"),
		ForceApprovalGroups:   []string{"security"},
	}

	got := toOpenApiSidecarReview(rev)

	assert.Equal(t, "review-1", got.ID)
	assert.Equal(t, "session-1", got.Session)
	assert.Equal(t, "sidecar-1", *got.SidecarID)
	assert.Equal(t, "appdb", *got.ListenerName)
	assert.Len(t, got.ReviewGroupsData, 2)
	assert.Equal(t, models.ReviewStatusPending.Str(), string(got.Status))

	// openapi.Review carries no connection field at all, so the absence is
	// structural. This pins the response the sidecar actually reads.
	assert.Nil(t, got.RevokeAt, "a sidecar review is granted no access window")
	assert.Nil(t, got.TimeWindow)

	// The policy the review was filed under. A sidecar sends the rule name and
	// reads it back, so a review filed against a rule it did not ask for is
	// visible rather than silent.
	assert.Equal(t, "payments-approvers", *got.AccessRequestRuleName)
	assert.Equal(t, []string{"security"}, got.ForceApprovalGroups)
}

// Without the middleware there is no sidecar, and the handler must say so
// rather than read a nil row: the difference between 401 and a panic.
func TestPostReviewRefusesARequestThatSkippedTheMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/sidecars/reviews",
		strings.NewReader(`{"listener_name":"appdb","payload":"c2VsZWN0IDE7","approval_rule":"payments-approvers"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	PostReview(c)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "access denied")
}

// A statement that is not valid base64 never reaches the database, so this
// refuses before any write. The handler hashes and renders exactly the decoded
// bytes, so there is nothing else it could show a reviewer.
func TestPostReviewRefusesAPayloadThatIsNotBase64(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	// The same key SidecarAuthMiddleware sets, so the handler gets past its
	// first guard without a middleware or a database.
	c.Set("sidecar-auth", &models.Sidecar{ID: "sidecar-1", OrgID: "org-1", Name: "sc-a"})
	c.Request = httptest.NewRequest(http.MethodPost, "/api/sidecars/reviews",
		strings.NewReader(`{"listener_name":"appdb","payload":"!!!not-base64!!!","approval_rule":"payments-approvers"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	PostReview(c)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "not valid base64")
}

// The statement is stored twice, as the session blob and the review blob, so a
// token holder could otherwise fill the database one request at a time. The cap
// is on the raw bytes; a binary statement is stored at up to 4x in display form.
func TestPostReviewRefusesAStatementOverTheCap(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set("sidecar-auth", &models.Sidecar{ID: "sidecar-1", OrgID: "org-1", Name: "sc-a"})
	oversized := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", maxStatementBytes+1)))
	c.Request = httptest.NewRequest(http.MethodPost, "/api/sidecars/reviews",
		strings.NewReader(`{"listener_name":"appdb","payload":"`+oversized+`","approval_rule":"payments-approvers"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	PostReview(c)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assert.Contains(t, rec.Body.String(), "larger than")
}

// The claim reads by id, so it needs the sidecar the token named before it
// can scope the lookup. Without one it refuses rather than read a nil row.
func TestClaimReviewRefusesARequestThatSkippedTheMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Params = gin.Params{{Key: "id", Value: "9f97c0de-0000-0000-0000-000000000001"}}
	c.Request = httptest.NewRequest(http.MethodPost,
		"/api/sidecars/reviews/9f97c0de-0000-0000-0000-000000000001/claim", nil)

	ClaimReview(c)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// A malformed id is answered as not found before the query: the column is a
// uuid, and the database error would otherwise surface as a 500.
func TestClaimReviewAnswersAMalformedIDAsNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set("sidecar-auth", &models.Sidecar{ID: "sidecar-1", OrgID: "org-1", Name: "sc-a"})
	c.Params = gin.Params{{Key: "id", Value: "not-a-uuid"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/sidecars/reviews/not-a-uuid/claim", nil)

	ClaimReview(c)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetReviewRefusesARequestThatSkippedTheMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Params = gin.Params{{Key: "id", Value: "9f97c0de-0000-0000-0000-000000000001"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/api/sidecars/reviews/9f97c0de-0000-0000-0000-000000000001", nil)

	GetReview(c)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestListReviewsRefusesARequestThatSkippedTheMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/sidecars/reviews", nil)

	ListReviews(c)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestGetReviewAnswersAMalformedIDAsNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set("sidecar-auth", &models.Sidecar{ID: "sidecar-1", OrgID: "org-1", Name: "sc-a"})
	c.Params = gin.Params{{Key: "id", Value: "not-a-uuid"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/api/sidecars/reviews/not-a-uuid", nil)

	GetReview(c)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	// The sidecar reads a JSON 404 as "not found" and any other 404 as a
	// plane too old to have this route (daemon.planeNotFound).
	assert.JSONEq(t, `{"message":"review not found"}`, rec.Body.String())
}

func TestToSidecarReviewStatusOmitsStatementAndReviewers(t *testing.T) {
	decided := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	rev := &models.Review{
		ID:                    "review-1",
		SessionID:             "session-1",
		Status:                models.ReviewStatusRejected,
		ListenerName:          sql.NullString{String: "appdb", Valid: true},
		StatementHash:         sql.NullString{String: "abc", Valid: true},
		OwnerEmail:            reviewOwnerEmail,
		AccessRequestRuleName: ptr.String("payments-approvers"),
		RejectionReason:       ptr.String("not now"),
		ReviewGroups: []models.ReviewGroups{{
			GroupName:  "dba",
			Status:     models.ReviewStatusRejected,
			OwnerEmail: ptr.String("alice@example.com"),
			ReviewedAt: &decided,
		}},
	}

	body, err := json.Marshal(toSidecarReviewStatus(rev))
	assert.NoError(t, err)

	assert.JSONEq(t, `{
		"id": "review-1",
		"status": "REJECTED",
		"listener_name": "appdb",
		"approval_rule": "payments-approvers",
		"created_at": "0001-01-01T00:00:00Z",
		"decided_at": "2026-09-28T12:00:00Z",
		"rejection_reason": "not now"
	}`, string(body))
}

// The sidecar reads the deadline of a live or expired review, and nothing on
// a settled one. With no limit the key is absent.
func TestToSidecarReviewStatusCarriesTheDeadline(t *testing.T) {
	deadline := time.Date(2026, 9, 28, 12, 15, 0, 0, time.UTC)
	for _, tc := range []struct {
		status    models.ReviewStatusType
		expiresAt *time.Time
		want      bool
	}{
		{models.ReviewStatusPending, &deadline, true},
		{models.ReviewStatusApproved, &deadline, true},
		{models.ReviewStatusExpired, &deadline, true},
		{models.ReviewStatusPending, nil, false},
		{models.ReviewStatusRejected, &deadline, false},
		{models.ReviewStatusRevoked, &deadline, false},
		{models.ReviewStatusExecuted, &deadline, false},
	} {
		name := string(tc.status)
		if tc.expiresAt == nil {
			name += " with no limit"
		}
		t.Run(name, func(t *testing.T) {
			rev := &models.Review{
				ID:           "review-1",
				Status:       tc.status,
				ListenerName: sql.NullString{String: "appdb", Valid: true},
				ExpiresAt:    tc.expiresAt,
			}
			body, err := json.Marshal(toSidecarReviewStatus(rev))
			require.NoError(t, err)
			var got map[string]any
			require.NoError(t, json.Unmarshal(body, &got))
			if !tc.want {
				assert.NotContains(t, got, "expires_at")
				return
			}
			assert.Equal(t, "2026-09-28T12:15:00Z", got["expires_at"])
		})
	}
}

// The POST and claim answers carry the same deadline as the status read.
func TestToOpenApiSidecarReviewCarriesTheDeadline(t *testing.T) {
	deadline := time.Date(2026, 9, 28, 12, 15, 0, 0, time.UTC)
	rev := &models.Review{
		ID:           "review-1",
		Status:       models.ReviewStatusPending,
		SidecarID:    sql.NullString{String: "sidecar-1", Valid: true},
		ListenerName: sql.NullString{String: "appdb", Valid: true},
		ExpiresAt:    &deadline,
	}
	ttl := 600
	rev.ApprovalTTLSec = &ttl
	got := toOpenApiSidecarReview(rev)
	require.NotNil(t, got.ExpiresAt)
	assert.Equal(t, deadline, *got.ExpiresAt)
	require.NotNil(t, got.ApprovalTTLSec)
	assert.Equal(t, 600, *got.ApprovalTTLSec)

	rev.Status = models.ReviewStatusExecuted
	assert.Nil(t, toOpenApiSidecarReview(rev).ExpiresAt, "a spent approval has no deadline")

	rev.Status = models.ReviewStatusPending
	rev.ExpiresAt, rev.ApprovalTTLSec = nil, nil
	body, err := json.Marshal(toOpenApiSidecarReview(rev))
	require.NoError(t, err)
	assert.NotContains(t, string(body), "expires_at", "with no limit the answer is the one an older plane gives")
	assert.NotContains(t, string(body), "approval_ttl_sec")
}

func TestReviewDecidedAt(t *testing.T) {
	early := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	late := early.Add(time.Minute)
	group := func(status models.ReviewStatusType, at *time.Time) models.ReviewGroups {
		return models.ReviewGroups{Status: status, ReviewedAt: at}
	}

	tests := []struct {
		name   string
		status models.ReviewStatusType
		groups []models.ReviewGroups
		want   *time.Time
	}{
		{"pending has no decision", models.ReviewStatusPending,
			[]models.ReviewGroups{group(models.ReviewStatusApproved, &early)}, nil},
		{"latest decided group wins", models.ReviewStatusApproved,
			[]models.ReviewGroups{group(models.ReviewStatusApproved, &early), group(models.ReviewStatusApproved, &late)}, &late},
		{"pending groups are ignored", models.ReviewStatusRejected,
			[]models.ReviewGroups{group(models.ReviewStatusRejected, &early), group(models.ReviewStatusPending, &late)}, &early},
		{"no group timestamps", models.ReviewStatusExecuted,
			[]models.ReviewGroups{group(models.ReviewStatusApproved, nil)}, nil},
		{"a revocation after the approval is the decision", models.ReviewStatusRevoked,
			[]models.ReviewGroups{group(models.ReviewStatusApproved, &early), group(models.ReviewStatusRevoked, &late)}, &late},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reviewDecidedAt(&models.Review{Status: tt.status, ReviewGroups: tt.groups})
			assert.Equal(t, tt.want, got)
		})
	}
}

// What a reviewer ends up reading. The message renders Name, Email, Connection
// and Type whether or not they mean anything for a sidecar review, so each one
// carries something true rather than an empty label.
func TestNewSlackReviewRequest(t *testing.T) {
	sc := sidecarWithListener("appdb", "payments-approvers")
	rule := approvalRule()
	rev := newSidecarReview(sc, "appdb", "session-1", testStatementHash, rule, testPolicy(t, rule), time.Now().UTC())
	statement := "DELETE FROM users WHERE id = 42;"

	req := newSlackReviewRequest(sc, rev, "appdb", statement)

	// Without these two a click cannot find the review: the id becomes the
	// message metadata and the prefix of every button id.
	assert.Equal(t, rev.ID, req.ID)
	assert.Equal(t, rev.SessionID, req.SessionID)

	assert.Equal(t, "payments-sidecar", req.Name, "which environment asked")
	assert.Equal(t, "appdb", req.Connection, "which listener it arrived on")
	assert.Equal(t, reviewSlackType, req.ConnectionType)
	assert.Equal(t, reviewOwnerEmail, req.Email)
	assert.Equal(t, statement, req.Script, "the statement is what is being approved")

	assert.ElementsMatch(t, []string{"dba", "sre"}, req.ApprovalGroups,
		"one button pair per group the rule names, not per fixed role")
	assert.ElementsMatch(t, req.ApprovalGroups, req.UserGroups,
		"Groups renders unconditionally, so it says who may act rather than nothing")

	// The line renders unconditionally, so an empty value would show a broken
	// link. It opens the review the message is about.
	assert.NotEmpty(t, req.WebappURL, "an empty url renders as a dead More details link")
	assert.Equal(t, appconfig.Get().FullApiURL()+"/reviews/"+rev.SessionID, req.WebappURL)
	assert.True(t, strings.HasPrefix(req.WebappURL, "http://localhost:8009/hoop/"),
		"ApiURL drops a configured path prefix and lands the approver outside the app")

	assert.Empty(t, req.SlackChannels, "notifySlack sets the channels from the listener")
	assert.Nil(t, req.SessionTime, "a sidecar review grants no access window")
	assert.Nil(t, req.ExpiresAt, "a rule with no limit shows no deadline")

	rule = ruleWithTTLs(ptr.Int(900), nil)
	rev = newSidecarReview(sc, "appdb", "session-1", testStatementHash, rule, testPolicy(t, rule), time.Now().UTC())
	req = newSlackReviewRequest(sc, rev, "appdb", statement)
	require.NotNil(t, req.ExpiresAt, "the approvers see when the review expires")
	assert.Equal(t, *rev.ExpiresAt, *req.ExpiresAt)
}

func TestSlackChannelsResponse(t *testing.T) {
	assert.Equal(t, []string{"C1", "C2"}, normalizeChannels([]string{" C1 ", "", "C2", "C1"}))
	assert.Equal(t, []string{}, normalizeChannels(nil))

	out := toOpenAPISlackChannels([]models.SidecarSlackChannels{
		{ListenerName: "pg", Channels: []string{"C-PG"}},
	})
	assert.Len(t, out.Listeners, 1)
	assert.Equal(t, "pg", out.Listeners[0].Name)
	assert.Equal(t, []string{"C-PG"}, out.Listeners[0].Channels)

	empty := toOpenAPISlackChannels(nil)
	assert.NotNil(t, empty.Listeners)
}

func TestSlackChannelRows(t *testing.T) {
	sc := &models.Sidecar{Name: "payments"}
	sc.Configuration.Listeners = []daemon.ListenerConfig{{Name: "pg"}, {Name: "mysql"}, {Name: ""}}

	rows, msg := slackChannelRows(sc, openapi.SidecarSlackChannels{
		Listeners: []openapi.SidecarListenerSlackChannels{{Name: "pg", Channels: []string{" C-PG "}}},
	})
	assert.Empty(t, msg)
	assert.Len(t, rows, 1)
	assert.Equal(t, "pg", rows[0].ListenerName)
	assert.Equal(t, []string{"C-PG"}, []string(rows[0].Channels))

	_, msg = slackChannelRows(sc, openapi.SidecarSlackChannels{
		Listeners: []openapi.SidecarListenerSlackChannels{{Name: "redis", Channels: []string{"C1"}}},
	})
	assert.Contains(t, msg, `no listener named "redis"`)

	_, msg = slackChannelRows(sc, openapi.SidecarSlackChannels{
		Listeners: []openapi.SidecarListenerSlackChannels{{Name: "pg"}, {Name: " pg "}},
	})
	assert.Contains(t, msg, "repeated")

	// An unnamed listener cannot be addressed.
	_, msg = slackChannelRows(sc, openapi.SidecarSlackChannels{
		Listeners: []openapi.SidecarListenerSlackChannels{{Name: ""}},
	})
	assert.NotEmpty(t, msg)

	// A listener stored before the limit, with a name no index holds.
	over := strings.Repeat("l", models.MaxSidecarListenerNameBytes+1)
	sc.Configuration.Listeners = append(sc.Configuration.Listeners, daemon.ListenerConfig{Name: over})
	_, msg = slackChannelRows(sc, openapi.SidecarSlackChannels{
		Listeners: []openapi.SidecarListenerSlackChannels{{Name: over, Channels: []string{"C1"}}},
	})
	assert.Contains(t, msg, "listener name is 1025 bytes, over 1024")
}

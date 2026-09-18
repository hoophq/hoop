package apisidecar

import (
	"database/sql"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/aws/smithy-go/ptr"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/stretchr/testify/assert"
)

// The handler refuses outside the control plane, so a test reaching past that
// guard has to run as one. appconfig.Load is one-shot (it returns early once
// loaded), so a test binary gets a single mode and the gateway-mode refusal is
// covered by booting a real gateway rather than from here.
func TestMain(m *testing.M) {
	// With a bare origin the two url accessors return the same string, so the
	// WebappURL assertion below would hold whichever one the code calls. The
	// path prefix is what makes it mean something.
	os.Setenv("API_URL", "http://localhost:8009/hoop")
	if err := appconfig.Load(appconfig.AppModeControlPlane); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
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
			got := listenerNamesApprovalRule(tc.sidecar, tc.listenerName, tc.ruleName)
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

	assert.False(t, listenerNamesApprovalRule(sc, "appdb", "lax-approvers"),
		"the first lane must not authorize a statement the second lane may have held")
	assert.False(t, listenerNamesApprovalRule(sc, "appdb", "strict-approvers"),
		"neither direction authorizes while the name is ambiguous")

	// A second lane under another name changes nothing: the match is unique.
	sc = sidecarWithListeners(lane("appdb", "lax-approvers"), lane("reporting", "strict-approvers"))
	assert.True(t, listenerNamesApprovalRule(sc, "appdb", "lax-approvers"))
	assert.True(t, listenerNamesApprovalRule(sc, "reporting", "strict-approvers"))
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
// refuses before any write. The reviewer would otherwise be shown whatever
// arrived, because nothing further down this path decodes anything.
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
// token holder could otherwise fill the database one request at a time.
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

	assert.Empty(t, req.SlackChannels, "a sidecar review has no connection, so the org default is the only destination")
	assert.Nil(t, req.SessionTime, "a sidecar review grants no access window")
}

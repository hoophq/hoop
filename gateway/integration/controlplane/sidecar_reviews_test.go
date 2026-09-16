//go:build integration

package controlplane

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/aws/smithy-go/ptr"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sidecarTokenHeader is what SidecarAuthMiddleware reads. The constant is
// unexported on the gateway side and duplicated on the sidecar side, so the
// literal is the contract.
const sidecarTokenHeader = "hoop-sidecar-token"

// fixture is one sidecar, one listener and one approval rule, all unique to the
// calling test. The suite shares a database, so every test mints its own names
// and its own statement rather than racing the others through one row.
type fixture struct {
	token        string
	sidecarID    string
	listenerName string
	ruleName     string
}

// newFixture stores the sidecar and the rule that authorize a review.
//
// The token is any string: the middleware looks the sidecar up by
// models.HashAPIKey(token), so nothing here needs the real key generator.
func newFixture(t *testing.T) fixture {
	t.Helper()

	suffix := uuid.NewString()[:8]
	f := fixture{
		token:        "hsc_" + uuid.NewString(),
		sidecarID:    uuid.NewString(),
		listenerName: "appdb-" + suffix,
		ruleName:     "approvers-" + suffix,
	}

	orgUUID, err := uuid.Parse(gw.OrgID)
	require.NoError(t, err)

	// Two groups and a minimum of one, so the review carries a policy that is
	// not the degenerate "every group" case.
	//
	// The empty arrays are required, not tidiness: connection_names,
	// approval_required_groups and force_approval_groups are all NOT NULL
	// (migration 000062). A sidecar rule targets no connection, so its list is
	// empty rather than absent.
	err = models.CreateAccessRequestRule(models.DB, &models.AccessRequestRule{
		OrgID:                  orgUUID,
		Name:                   f.ruleName,
		AccessType:             models.AccessTypeSidecar,
		ConnectionNames:        pq.StringArray{},
		ApprovalRequiredGroups: pq.StringArray{},
		ReviewersGroups:        pq.StringArray{"dba", "sre"},
		ForceApprovalGroups:    pq.StringArray{},
		MinApprovals:           ptr.Int(1),
	})
	require.NoError(t, err)

	// The stored configuration is what authorizes the review. A listener that
	// did not name this rule would be refused with 422 before any of the
	// behaviour under test ran.
	err = models.CreateSidecar(models.DB, &models.Sidecar{
		ID:      f.sidecarID,
		OrgID:   gw.OrgID,
		Name:    "sidecar-" + suffix,
		KeyHash: models.HashAPIKey(f.token),
		Configuration: models.SidecarConfiguration{
			Listeners: []daemon.ListenerConfig{{
				Name:     f.listenerName,
				Analyzer: &daemon.LaneAnalyzerConfig{ApprovalRule: f.ruleName},
			}},
		},
		CreatedBy: "integration-test",
	})
	require.NoError(t, err)

	return f
}

// postReview calls the endpoint under test the way a sidecar does: the token in
// its own header, the statement base64 encoded.
func (f fixture) postReview(t *testing.T, statement string) (int, openapi.SidecarReviewResponse) {
	t.Helper()

	body, err := json.Marshal(openapi.SidecarReviewRequest{
		ListenerName: f.listenerName,
		Payload:      base64.StdEncoding.EncodeToString([]byte(statement)),
		ApprovalRule: f.ruleName,
	})
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		gw.HTTP.BaseURL+"/api/sidecars/reviews", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(sidecarTokenHeader, f.token)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var out openapi.SidecarReviewResponse
	if resp.StatusCode < 300 {
		require.NoError(t, json.Unmarshal(raw, &out),
			"decoding %s: %s", resp.Status, raw)
	}
	return resp.StatusCode, out
}

// liveReviewCount counts the reviews this fixture has that an approver could
// still act on, which is what "no duplicate review" means in practice.
func (f fixture) liveReviewCount(t *testing.T) int {
	t.Helper()
	var n int64
	err := models.DB.Raw(`
	SELECT count(*) FROM private.reviews
	WHERE org_id = ? AND sidecar_id = ? AND status <> 'EXECUTED'`,
		gw.OrgID, f.sidecarID).Scan(&n).Error
	require.NoError(t, err)
	return int(n)
}

// approve puts the review in the state a human approval leaves it in. The
// approval path itself (DoReview) is gateway API surface with its own tests;
// what this suite covers is what the control plane does with the result.
func approve(t *testing.T, reviewID string) {
	t.Helper()
	res := models.DB.Exec(`UPDATE private.reviews SET status = 'APPROVED' WHERE id = ?`, reviewID)
	require.NoError(t, res.Error)
	require.EqualValues(t, 1, res.RowsAffected)
}

func setStatus(t *testing.T, reviewID string, status models.ReviewStatusType) {
	t.Helper()
	res := models.DB.Exec(`UPDATE private.reviews SET status = ? WHERE id = ?`, status, reviewID)
	require.NoError(t, res.Error)
	require.EqualValues(t, 1, res.RowsAffected)
}

// A retry must find the review the first request filed. Filing a second one
// would post a second Slack message and, worse, leave the approval a human
// gives on a review no later retry ever asks about.
func TestPostReviewReturnsTheExistingPendingReview(t *testing.T) {
	f := newFixture(t)
	const statement = "DELETE FROM users WHERE id = 1;"

	code, first := f.postReview(t, statement)
	require.Equal(t, http.StatusCreated, code)
	require.NotNil(t, first.Review)
	assert.False(t, first.Forward, "a review nobody has approved releases nothing")
	assert.Equal(t, openapi.ReviewStatusType(models.ReviewStatusPending), first.Review.Status)

	code, second := f.postReview(t, statement)
	require.Equal(t, http.StatusOK, code, "the retry filed instead of matching")
	require.NotNil(t, second.Review)
	assert.Equal(t, first.Review.ID, second.Review.ID, "the retry got a different review")
	assert.False(t, second.Forward)
	assert.Equal(t, 1, f.liveReviewCount(t), "the retry filed a duplicate review")

	// The match carries the same policy as the filing, so a sidecar reading
	// either response sees the same approvers.
	assert.Len(t, second.Review.ReviewGroupsData, 2)
	assert.Equal(t, first.Review.MinApprovals, second.Review.MinApprovals)
}

// The dedup key is the exact bytes. The sidecar's analyzer cache key strips
// literals, and reusing that shape here would let one approval of `id = 1`
// release every `DELETE FROM users WHERE id = ?`.
func TestPostReviewFilesASeparateReviewForADifferentLiteral(t *testing.T) {
	f := newFixture(t)

	code, first := f.postReview(t, "DELETE FROM users WHERE id = 1;")
	require.Equal(t, http.StatusCreated, code)

	code, other := f.postReview(t, "DELETE FROM users WHERE id = 999;")
	require.Equal(t, http.StatusCreated, code, "the second statement matched the first")
	assert.NotEqual(t, first.Review.ID, other.Review.ID,
		"approving one statement would have released the other")
	assert.Equal(t, 2, f.liveReviewCount(t))
}

// The approval is spent when it is used, and the session it belongs to is
// closed by the same act: nothing else will ever end it.
func TestPostReviewConsumesAnApprovedReviewAndClosesItsSession(t *testing.T) {
	f := newFixture(t)
	const statement = "UPDATE accounts SET balance = 0 WHERE id = 7;"

	code, filed := f.postReview(t, statement)
	require.Equal(t, http.StatusCreated, code)
	approve(t, filed.Review.ID)

	code, consumed := f.postReview(t, statement)
	require.Equal(t, http.StatusOK, code)
	assert.True(t, consumed.Forward, "the approved statement was not released")
	assert.Equal(t, filed.Review.ID, consumed.Review.ID)
	assert.Equal(t, openapi.ReviewStatusType(models.ReviewStatusExecuted), consumed.Review.Status)

	var status string
	var endedAt *string
	err := models.DB.Raw(`SELECT status, ended_at::text FROM private.sessions WHERE id = ?`,
		filed.Review.Session).Row().Scan(&status, &endedAt)
	require.NoError(t, err)
	assert.Equal(t, "done", status, "the review's session was left open")
	assert.NotNil(t, endedAt, "done without ended_at, unlike every other close in the codebase")
}

// One approval releases one execution. The retry after a consumed approval is a
// new request for the same bytes and waits for a human again.
func TestPostReviewFilesAgainAfterAnApprovalIsConsumed(t *testing.T) {
	f := newFixture(t)
	const statement = "DROP TABLE audit_log;"

	code, filed := f.postReview(t, statement)
	require.Equal(t, http.StatusCreated, code)
	approve(t, filed.Review.ID)

	code, consumed := f.postReview(t, statement)
	require.Equal(t, http.StatusOK, code)
	require.True(t, consumed.Forward)

	code, again := f.postReview(t, statement)
	require.Equal(t, http.StatusCreated, code, "the consumed review answered a later retry")
	assert.False(t, again.Forward, "a spent approval released a second statement")
	assert.NotEqual(t, filed.Review.ID, again.Review.ID)
	assert.Equal(t, openapi.ReviewStatusType(models.ReviewStatusPending), again.Review.Status)
}

// The real approval path is models.UpdateReview, not the direct status write
// the other tests arrange with. UpdateReview passes a whole Review struct to
// GORM Updates, and a review it loaded through a query that does not select
// statement_hash carries a zero value for it. If that reached the row the
// approved review would drop out of the partial unique index, the retry would
// file a fresh PENDING review, and the approval would be unreachable: the exact
// bug this ticket exists to fix, reintroduced through the approval itself.
func TestApprovingThroughUpdateReviewKeepsTheStatementHash(t *testing.T) {
	f := newFixture(t)
	const statement = "UPDATE flags SET enabled = true WHERE id = 5;"

	code, filed := f.postReview(t, statement)
	require.Equal(t, http.StatusCreated, code)

	rev, err := models.GetReviewByIdOrSid(gw.OrgID, filed.Review.ID)
	require.NoError(t, err)
	rev.Status = models.ReviewStatusApproved
	require.NoError(t, models.UpdateReview(rev))

	var hash *string
	require.NoError(t, models.DB.Raw(
		`SELECT statement_hash FROM private.reviews WHERE id = ?`, filed.Review.ID).
		Row().Scan(&hash))
	require.NotNil(t, hash, "the approval cleared the statement hash")

	// The retry has to find that approval, which is the whole point.
	code, consumed := f.postReview(t, statement)
	require.Equal(t, http.StatusOK, code, "the approved review was not found by the retry")
	assert.True(t, consumed.Forward, "the approval could not be consumed")
	assert.Equal(t, filed.Review.ID, consumed.Review.ID)
}

// A rejection has to stick. If a retry filed a fresh PENDING review the
// rejected statement would go back to a human on every attempt, and a
// persistent client would eventually find an approver who says yes.
func TestPostReviewKeepsDenyingARejectedStatement(t *testing.T) {
	for _, status := range []models.ReviewStatusType{
		models.ReviewStatusRejected,
		models.ReviewStatusRevoked,
	} {
		t.Run(string(status), func(t *testing.T) {
			f := newFixture(t)
			const statement = "GRANT ALL ON schema public TO app;"

			code, filed := f.postReview(t, statement)
			require.Equal(t, http.StatusCreated, code)
			setStatus(t, filed.Review.ID, status)

			code, retry := f.postReview(t, statement)
			require.Equal(t, http.StatusOK, code, "the retry filed a new review")
			assert.False(t, retry.Forward)
			assert.Equal(t, filed.Review.ID, retry.Review.ID)
			assert.Equal(t, openapi.ReviewStatusType(status), retry.Review.Status)
			assert.Equal(t, 1, f.liveReviewCount(t), "a rejected statement was reviewed twice")
		})
	}
}

// Two retries of one approved statement can both read APPROVED before either
// writes. Only one may consume it: the other would run the statement a second
// time, which for a DELETE is the whole problem.
//
// The race is driven against the model rather than the endpoint on purpose. An
// HTTP-level race does not reach it: the first request settles the review to
// EXECUTED before the others read, so they never see APPROVED and never
// contend. This starts every racer from the one state where the contention is
// real, which is the state the conditional UPDATE exists for.
func TestClaimApprovedSidecarReviewConsumesItExactlyOnce(t *testing.T) {
	f := newFixture(t)
	const statement = "DELETE FROM sessions WHERE created_at < now();"

	code, filed := f.postReview(t, statement)
	require.Equal(t, http.StatusCreated, code)
	approve(t, filed.Review.ID)

	const racers = 8
	var mu sync.Mutex
	claims := 0

	var start sync.WaitGroup
	start.Add(1)
	var done sync.WaitGroup
	for range racers {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			claimed, status, err := models.ClaimApprovedSidecarReview(
				models.DB, gw.OrgID, filed.Review.ID)
			assert.NoError(t, err)
			// Winner and loser alike see the settled row, which is exactly
			// why the answer cannot be read off the status.
			assert.Equal(t, models.ReviewStatusExecuted, status)
			if claimed {
				mu.Lock()
				claims++
				mu.Unlock()
			}
		}()
	}
	start.Done()
	done.Wait()

	assert.Equal(t, 1, claims, "the approval was consumed %d times", claims)
}

// The endpoint's half of the same guarantee: once the review is consumed, a
// later retry never forwards on it again.
func TestPostReviewForwardsAnApprovedStatementOnlyOnce(t *testing.T) {
	f := newFixture(t)
	const statement = "DELETE FROM sessions WHERE id = 3;"

	code, filed := f.postReview(t, statement)
	require.Equal(t, http.StatusCreated, code)
	approve(t, filed.Review.ID)

	forwarded := 0
	for range 4 {
		if _, resp := f.postReview(t, statement); resp.Forward {
			forwarded++
		}
	}
	assert.Equal(t, 1, forwarded, "an approved statement was released %d times", forwarded)
}

// Two first requests for one statement race before any review exists. The
// partial unique index decides which one files; the loser must answer from the
// winner's review rather than file a second or fail the request.
func TestPostReviewFilesOneReviewWhenFirstRequestsRace(t *testing.T) {
	f := newFixture(t)
	const statement = "TRUNCATE TABLE events;"

	const racers = 8
	var mu sync.Mutex
	ids := map[string]int{}
	codes := map[int]int{}

	var start sync.WaitGroup
	start.Add(1)
	var done sync.WaitGroup
	for range racers {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			code, resp := f.postReview(t, statement)
			mu.Lock()
			defer mu.Unlock()
			codes[code]++
			if resp.Review != nil {
				ids[resp.Review.ID]++
			}
		}()
	}
	start.Done()
	done.Wait()

	assert.Len(t, ids, 1, "the racers got %d different reviews", len(ids))
	assert.Equal(t, 1, f.liveReviewCount(t), "the race filed more than one review")
	assert.Equal(t, 1, codes[http.StatusCreated], "more than one racer reported filing")
	assert.Equal(t, racers-1, codes[http.StatusOK],
		fmt.Sprintf("racers did not all succeed: %v", codes))
}

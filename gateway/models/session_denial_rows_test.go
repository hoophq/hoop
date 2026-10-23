package models_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A requester who denies their own review adds a review_groups row for their
// own group, flagged added_on_denial. The review read skips that row, so the
// Sessions list must skip it too: a member of the requester's group is not a
// reviewer and gets 404 on the review.
func TestListSessionsSkipsDenialRows(t *testing.T) {
	startTestDB(t)

	for _, u := range []struct{ subject, group string }{
		{"requester", "eng"}, {"teammate", "eng"}, {"dba-member", "dba"},
	} {
		execSQL(t, `INSERT INTO private.users (org_id, subject, email, name, status)
			VALUES (?, ?, ?, ?, 'active')`, testOrgID, u.subject, u.subject+"@hoop.dev", u.subject)
		execSQL(t, `INSERT INTO private.user_groups (org_id, user_id, name)
			SELECT ?, id, ? FROM private.users WHERE subject = ?`, testOrgID, u.group, u.subject)
	}

	sid := uuid.NewString()
	require.NoError(t, models.UpsertSession(models.Session{
		ID: sid, OrgID: testOrgID, UserID: "requester", Connection: "pg-prod",
		ConnectionType: "database", Verb: "exec", Status: "done", CreatedAt: time.Now().UTC(),
	}))
	reviewID := uuid.NewString()
	require.NoError(t, models.CreateReview(&models.Review{
		ID: reviewID, OrgID: testOrgID, SessionID: sid, Type: models.ReviewTypeOneTime,
		Status: models.ReviewStatusRejected, OwnerID: "requester", OwnerEmail: "requester@hoop.dev",
		CreatedAt: time.Now().UTC(),
	}, ""))
	for _, rg := range []struct {
		group  string
		denial bool
	}{{"dba", false}, {"eng", true}} {
		execSQL(t, `INSERT INTO private.review_groups (id, org_id, review_id, group_name, status, added_on_denial)
			VALUES (?, ?, ?, ?, 'REJECTED', ?)`, uuid.NewString(), testOrgID, reviewID, rg.group, rg.denial)
	}

	visible := func(userID string, isAdmin bool, approver *string) bool {
		t.Helper()
		opt := models.NewSessionOption()
		opt.ReviewApproverEmail = approver
		got, err := models.ListSessions(testOrgID, userID, isAdmin, opt)
		require.NoError(t, err)
		for _, s := range got.Items {
			if s.ID == sid {
				return true
			}
		}
		return false
	}
	email := func(s string) *string { v := s + "@hoop.dev"; return &v }

	assert.True(t, visible("requester", false, nil), "the requester sees their own session")
	assert.True(t, visible("dba-member", false, nil), "a reviewer group member sees the session")
	assert.False(t, visible("teammate", false, nil), "a denial row makes no reviewer")

	assert.True(t, visible("admin", true, email("dba-member")), "a reviewer is an approver")
	assert.False(t, visible("admin", true, email("teammate")), "a denial row names no approver")

	sess, err := models.GetSessionByID(testOrgID, sid)
	require.NoError(t, err)
	require.NotNil(t, sess.Review)
	denials := map[string]bool{}
	for _, rg := range sess.Review.ReviewGroups {
		denials[rg.GroupName] = rg.AddedOnDenial
	}
	assert.Equal(t, map[string]bool{"dba": false, "eng": true}, denials,
		"the session read carries the flag, so GET /sessions/:id can skip the row")
}

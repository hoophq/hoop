package models_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/gateway/liveevents"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setReviewNotifications(t *testing.T, on bool) {
	featureflag.Set(testOrgID, featureflag.FlagReviewNotifications, on)
	t.Cleanup(func() { featureflag.Set(testOrgID, featureflag.FlagReviewNotifications, true) })
}

func orgEvents(t *testing.T) <-chan liveevents.Event {
	ch, stop := liveevents.Subscribe(testOrgID)
	t.Cleanup(stop)
	return ch
}

func nextEvent(t *testing.T, ch <-chan liveevents.Event) *liveevents.Event {
	t.Helper()
	select {
	case ev := <-ch:
		return &ev
	case <-time.After(time.Second):
		return nil
	}
}

func connectionReview(t *testing.T, status models.ReviewStatusType) *models.Review {
	t.Helper()
	sid := uuid.NewString()
	require.NoError(t, models.UpsertSession(models.Session{
		ID: sid, OrgID: testOrgID, UserID: "requester", Connection: "pg-prod",
		ConnectionType: "database", Verb: "exec", Status: "open", CreatedAt: time.Now().UTC(),
	}))
	return &models.Review{
		ID: uuid.NewString(), OrgID: testOrgID, SessionID: sid, Type: models.ReviewTypeOneTime,
		Status: status, ConnectionName: "pg-prod", OwnerID: "requester", OwnerEmail: "requester@hoop.dev",
		ReviewGroups: []models.ReviewGroups{{ID: uuid.NewString(), OrgID: testOrgID, GroupName: "dba", Status: status}},
		CreatedAt:    time.Now().UTC(),
	}
}

func seedForceApprovedConnection(t *testing.T) {
	t.Helper()
	seedAdminConnection(t, "pg-prod", "pg-prod")
	execSQL(t, `UPDATE private.connections SET force_approve_groups = '{sre}' WHERE org_id = ? AND name = 'pg-prod'`, testOrgID)
}

func TestANewReviewTellsTheReviewersAndTheConnectionForceApprovers(t *testing.T) {
	startTestDB(t)
	setReviewNotifications(t, true)
	seedForceApprovedConnection(t)
	events := orgEvents(t)

	rev := connectionReview(t, models.ReviewStatusPending)
	require.NoError(t, models.CreateReview(rev, "SELECT 1"))

	ev := nextEvent(t, events)
	require.NotNil(t, ev, "no event")
	assert.Equal(t, liveevents.ReviewCreated, ev.Type)
	assert.Equal(t, rev.ID, ev.ID)
	require.NotNil(t, ev.Audience)
	assert.ElementsMatch(t, []string{"dba", "sre"}, ev.Audience.Groups)
	assert.Equal(t, "requester", ev.Audience.OwnerID)
}

func TestAnAccessRuleReviewUsesTheRuleForceApprovers(t *testing.T) {
	startTestDB(t)
	setReviewNotifications(t, true)
	seedForceApprovedConnection(t)
	events := orgEvents(t)

	rev := connectionReview(t, models.ReviewStatusPending)
	rule := "prod-writes"
	rev.AccessRequestRuleName = &rule
	rev.ForceApprovalGroups = []string{"leads"}
	require.NoError(t, models.CreateReview(rev, ""))

	ev := nextEvent(t, events)
	require.NotNil(t, ev, "no event")
	assert.ElementsMatch(t, []string{"dba", "leads"}, ev.Audience.Groups)
}

func TestNoReviewEventWhenTheFlagIsOff(t *testing.T) {
	startTestDB(t)
	setReviewNotifications(t, false)
	events := orgEvents(t)

	require.NoError(t, models.CreateReview(connectionReview(t, models.ReviewStatusPending), ""))

	assert.Nil(t, nextEvent(t, events))
}

func TestNoReviewEventForAReviewThatDoesNotWait(t *testing.T) {
	startTestDB(t)
	setReviewNotifications(t, true)
	events := orgEvents(t)

	require.NoError(t, models.CreateReview(connectionReview(t, models.ReviewStatusApproved), ""))

	assert.Nil(t, nextEvent(t, events))
}

func TestASidecarReviewTellsItsApproversOnce(t *testing.T) {
	startTestDB(t)
	setReviewNotifications(t, true)
	sc := seedSidecar(t, "notify")
	events := orgEvents(t)
	const statement = "DELETE FROM x;"

	first := seedSidecarReview(t, sc, statement)
	ev := nextEvent(t, events)
	require.NotNil(t, ev, "no event")
	assert.Equal(t, first.ID, ev.ID)
	assert.Equal(t, sc.ID, ev.Audience.OwnerID)

	dup := *first
	dup.ID = uuid.NewString()
	dup.SessionID = uuid.NewString()
	sess := models.Session{
		ID: dup.SessionID, OrgID: testOrgID, BlobInput: models.BlobInputType(statement),
		ConnectionType: "custom", Verb: "exec", Status: "open",
		UserID: sc.ID, UserName: sc.Name, UserEmail: "hoop@hoop.dev", CreatedAt: time.Now().UTC(),
	}
	_, err := models.CreateSidecarReview(models.DB, sess, &dup, statement)
	require.True(t, errors.Is(err, gorm.ErrDuplicatedKey), "err=%v", err)
	assert.Nil(t, nextEvent(t, events), "the retry published again")
}

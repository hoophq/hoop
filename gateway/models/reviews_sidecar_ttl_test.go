package models_test

import (
	"database/sql"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/migrations"
	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

const ttlRule = "payments-approvers"

func timeAt(d time.Duration) *time.Time {
	t := time.Now().UTC().Add(d)
	return &t
}

// setDeadline moves a review's deadline with a raw UPDATE; nil clears it.
func setDeadline(t *testing.T, reviewID string, at *time.Time) {
	t.Helper()
	var v any
	if at != nil {
		v = at.UTC()
	}
	if err := models.DB.Exec(`UPDATE private.reviews SET expires_at = ? WHERE org_id = ? AND id = ?`,
		v, testOrgID, reviewID).Error; err != nil {
		t.Fatalf("set deadline: %v", err)
	}
}

func setApprovalTTL(t *testing.T, reviewID string, sec int) {
	t.Helper()
	if err := models.DB.Exec(`UPDATE private.reviews SET approval_ttl_sec = ? WHERE org_id = ? AND id = ?`,
		sec, testOrgID, reviewID).Error; err != nil {
		t.Fatalf("set approval ttl: %v", err)
	}
}

// storedReview is the row as written, with no read-time status.
type storedReview struct {
	Status        string
	StatementHash sql.NullString
	ExpiresAt     *time.Time
}

func readStored(t *testing.T, reviewID string) storedReview {
	t.Helper()
	var row storedReview
	if err := models.DB.Raw(`SELECT status, statement_hash, expires_at FROM private.reviews WHERE org_id = ? AND id = ?`,
		testOrgID, reviewID).Scan(&row).Error; err != nil {
		t.Fatalf("read the stored review: %v", err)
	}
	return row
}

func loadSidecarReview(t *testing.T, sc *models.Sidecar, reviewID string) *models.Review {
	t.Helper()
	rev, err := models.GetSidecarReview(models.DB, testOrgID, sc.ID, reviewID)
	if err != nil {
		t.Fatalf("load the review: %v", err)
	}
	return rev
}

// refile files the same statement again under a new review, as PostReview does.
func refile(t *testing.T, first *models.Review, statement string, now time.Time) (*models.Review, []string, error) {
	t.Helper()
	next := *first
	next.ID = uuid.NewString()
	next.SessionID = uuid.NewString()
	next.Status = models.ReviewStatusPending
	next.ReviewGroups = nil
	next.ExpiresAt = nil
	next.CreatedAt = now
	sess := models.Session{
		ID: next.SessionID, OrgID: testOrgID, BlobInput: models.BlobInputType(statement),
		ConnectionType: "custom", Verb: "exec", Status: "open",
		UserID: first.OwnerID, UserName: "sidecar", UserEmail: "hoop@hoop.dev", CreatedAt: now,
	}
	ids, err := models.CreateSidecarReview(models.DB, sess, &next, statement)
	return &next, ids, err
}

func liveReview(sc *models.Sidecar, statement string, now time.Time) (*models.Review, error) {
	return models.GetLiveSidecarReview(models.DB, testOrgID, sc.ID, "appdb", ttlRule,
		models.HashStatement([]byte(statement)), now)
}

// approved is the decision doIndividualReview hands the write.
func approved(rev *models.Review) *models.Review {
	rev.Status = models.ReviewStatusApproved
	now := time.Now().UTC()
	email := "approver@hoop.dev"
	for i := range rev.ReviewGroups {
		rev.ReviewGroups[i].Status = models.ReviewStatusApproved
		rev.ReviewGroups[i].OwnerEmail = &email
		rev.ReviewGroups[i].ReviewedAt = &now
	}
	return rev
}

func seedPendingGroup(t *testing.T, reviewID string) {
	t.Helper()
	if err := models.DB.Table("private.review_groups").Create(map[string]any{
		"id":         uuid.NewString(),
		"org_id":     testOrgID,
		"review_id":  reviewID,
		"group_name": "dba",
		"status":     models.ReviewStatusPending,
	}).Error; err != nil {
		t.Fatalf("seed review group: %v", err)
	}
}

// One database for the TTL tests: a PGlite instance keeps its memory until the
// test binary exits. Each case seeds its own sidecar and reads rows by id.
func TestSidecarReviewTTL(t *testing.T) {
	startTestDB(t)
	t.Run("ExpiredSidecarReviewIsNotLive", testExpiredSidecarReviewIsNotLive)
	t.Run("CreateSidecarReviewFreesAnExpiredStatement", testCreateSidecarReviewFreesAnExpiredStatement)
	t.Run("AnApprovalOutlivesThePendingDeadline", testAnApprovalOutlivesThePendingDeadline)
	t.Run("NoApprovalTTLClearsTheDeadline", testNoApprovalTTLClearsTheDeadline)
	t.Run("ClaimNeverReleasesAnExpiredReview", testClaimNeverReleasesAnExpiredReview)
	t.Run("DeadlinesUseTheBoundClock", testDeadlinesUseTheBoundClock)
	t.Run("DeadlinesUseTheDatabaseClock", testDeadlinesUseTheDatabaseClock)
	t.Run("ExpireSidecarReview", testExpireSidecarReview)
	t.Run("UpdateSidecarReviewDeadline", testUpdateSidecarReviewDeadline)
	t.Run("ReadsReportAnExpiredSidecarReview", testReadsReportAnExpiredSidecarReview)
	t.Run("GatewayReviewHasNoDeadline", testGatewayReviewHasNoDeadline)
	t.Run("ReviewStatusLabelsLeaveOutOnlyExpired", testReviewStatusLabelsLeaveOutOnlyExpired)
	t.Run("PendingCountSkipsALapsedSidecarReview", testPendingCountSkipsALapsedSidecarReview)
	// Last: it drops the columns every other case needs.
	t.Run("ReviewTTLMigrationRollsBack", testReviewTTLMigrationRollsBack)
}

// The compliance report counts reviews awaiting a decision; nobody can decide a
// lapsed sidecar review. A gateway row keeps its count whatever expires_at holds.
func testPendingCountSkipsALapsedSidecarReview(t *testing.T) {
	sc := seedSidecar(t, "pending-count")
	count := func() int {
		t.Helper()
		pending, _, err := models.CountPendingReviews(testOrgID, time.Now().UTC())
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		return pending
	}
	before := count()
	rev := seedSidecarReview(t, sc, "DELETE FROM "+uuid.NewString()+";")
	setDeadline(t, rev.ID, timeAt(time.Hour))
	if got := count(); got != before+1 {
		t.Fatalf("a live sidecar review: pending %d, want %d", got, before+1)
	}
	setDeadline(t, rev.ID, timeAt(-time.Minute))
	if got := count(); got != before {
		t.Fatalf("a lapsed sidecar review: pending %d, want %d", got, before)
	}

	if err := models.DB.Exec(`UPDATE private.reviews SET listener_name = NULL WHERE id = ?`, rev.ID).Error; err != nil {
		t.Fatalf("make it a gateway row: %v", err)
	}
	if got := count(); got != before+1 {
		t.Errorf("a gateway row with a past expires_at: pending %d, want %d", got, before+1)
	}
}

// A lapsed holder is not live, so a resend never answers from it.
func testExpiredSidecarReviewIsNotLive(t *testing.T) {
	sc := seedSidecar(t, "not-live")
	now := time.Now().UTC()

	for _, tt := range []struct {
		name      string
		approve   bool
		status    models.ReviewStatusType
		deadline  *time.Time
		wantFound bool
	}{
		{name: "pending past the deadline", deadline: timeAt(-time.Minute)},
		{name: "approved past the deadline", approve: true, deadline: timeAt(-time.Minute)},
		// EXPIRED never keeps a hash; the lookup refuses one that does.
		{name: "expired with a hash", status: models.ReviewStatusExpired},
		{name: "pending inside the deadline", deadline: timeAt(time.Hour), wantFound: true},
		{name: "approved inside the deadline", approve: true, deadline: timeAt(time.Hour), wantFound: true},
		{name: "pending with no deadline", wantFound: true},
		// Nothing expires these, so the deadline must not hide them from a refile.
		{name: "processing past the deadline", status: models.ReviewStatusProcessing,
			deadline: timeAt(-time.Minute), wantFound: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			statement := "DELETE FROM " + uuid.NewString() + ";"
			var rev *models.Review
			if tt.approve {
				rev = seedApprovedSidecarReview(t, sc, statement)
			} else {
				rev = seedSidecarReview(t, sc, statement)
			}
			setDeadline(t, rev.ID, tt.deadline)
			if tt.status != "" {
				if err := models.UpdateReviewStatus(testOrgID, rev.ID, tt.status); err != nil {
					t.Fatalf("set status: %v", err)
				}
			}
			got, err := liveReview(sc, statement, now)
			if !tt.wantFound {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					t.Fatalf("the live lookup returned %+v, err=%v; want gorm.ErrRecordNotFound", got, err)
				}
				return
			}
			if err != nil || got.ID != rev.ID {
				t.Fatalf("the live lookup lost the review: got %+v, err=%v", got, err)
			}
		})
	}
}

// Filing over a lapsed holder expires it in the same transaction, so the
// statement is not blocked forever. A live holder still blocks.
func testCreateSidecarReviewFreesAnExpiredStatement(t *testing.T) {
	sc := seedSidecar(t, "refile-expired")

	for _, approve := range []bool{false, true} {
		name := "pending"
		if approve {
			name = "approved"
		}
		t.Run("over a lapsed "+name+" holder", func(t *testing.T) {
			statement := "DELETE FROM " + uuid.NewString() + ";"
			var old *models.Review
			if approve {
				old = seedApprovedSidecarReview(t, sc, statement)
			} else {
				old = seedSidecarReview(t, sc, statement)
			}
			setDeadline(t, old.ID, timeAt(-time.Minute))

			next, ids, err := refile(t, old, statement, time.Now().UTC())
			if err != nil {
				t.Fatalf("refile: %v", err)
			}
			if !slices.Equal(ids, []string{old.ID}) {
				t.Errorf("expired ids = %v, want [%s]", ids, old.ID)
			}
			if got := readStored(t, old.ID); got.Status != string(models.ReviewStatusExpired) || got.StatementHash.Valid {
				t.Errorf("old review = %+v, want EXPIRED with no hash", got)
			}
			if s := sessionStatus(t, old.SessionID); s != "done" {
				t.Errorf("old session = %q, want done", s)
			}
			if got := readStored(t, next.ID); got.Status != string(models.ReviewStatusPending) || !got.StatementHash.Valid {
				t.Errorf("new review = %+v, want PENDING with a hash", got)
			}
		})
	}

	t.Run("over a live holder", func(t *testing.T) {
		const statement = "DELETE FROM live;"
		old := seedSidecarReview(t, sc, statement)
		setDeadline(t, old.ID, timeAt(time.Hour))

		_, ids, err := refile(t, old, statement, time.Now().UTC())
		if !errors.Is(err, gorm.ErrDuplicatedKey) {
			t.Fatalf("refile over a live holder: err=%v, want gorm.ErrDuplicatedKey", err)
		}
		if len(ids) != 0 {
			t.Errorf("expired ids = %v, want none", ids)
		}
		if got := readStored(t, old.ID); got.Status != string(models.ReviewStatusPending) || !got.StatementHash.Valid {
			t.Errorf("live holder = %+v, want PENDING with its hash", got)
		}
		if s := sessionStatus(t, old.SessionID); s != "open" {
			t.Errorf("live holder session = %q, want open", s)
		}
	})
}

// The approval replaces the pending deadline, so the old one no longer counts.
func testAnApprovalOutlivesThePendingDeadline(t *testing.T) {
	sc := seedSidecar(t, "outlives")
	const statement = "DELETE FROM outlives;"
	rev := seedSidecarReview(t, sc, statement)
	setDeadline(t, rev.ID, timeAt(10*time.Minute))
	setApprovalTTL(t, rev.ID, 3600)

	if err := models.UpdateSidecarReview(models.DB, approved(loadSidecarReview(t, sc, rev.ID)),
		models.ReviewStatusPending, time.Now().UTC()); err != nil {
		t.Fatalf("approve: %v", err)
	}

	later := time.Now().UTC().Add(20 * time.Minute)
	if got, err := liveReview(sc, statement, later); err != nil || got.ID != rev.ID {
		t.Fatalf("the approval is not live after the pending deadline: got %+v, err=%v", got, err)
	}
	_, ids, err := refile(t, rev, statement, later)
	if !errors.Is(err, gorm.ErrDuplicatedKey) || len(ids) != 0 {
		t.Fatalf("refile over the approval: ids=%v err=%v, want gorm.ErrDuplicatedKey and none expired", ids, err)
	}
	if got := readStored(t, rev.ID); got.Status != string(models.ReviewStatusApproved) {
		t.Fatalf("the approval = %s, want APPROVED", got.Status)
	}
	claimed, status, err := models.ClaimApprovedSidecarReview(models.DB, testOrgID, rev.ID, later)
	if err != nil || !claimed || status != models.ReviewStatusExecuted {
		t.Fatalf("claim = (%v, %s, %v), want (true, EXECUTED)", claimed, status, err)
	}
}

// With a pending limit only, an approval has no deadline.
func testNoApprovalTTLClearsTheDeadline(t *testing.T) {
	sc := seedSidecar(t, "no-approval-ttl")
	rev := seedSidecarReview(t, sc, "DELETE FROM clears;")
	setDeadline(t, rev.ID, timeAt(10*time.Minute))

	if err := models.UpdateSidecarReview(models.DB, approved(loadSidecarReview(t, sc, rev.ID)),
		models.ReviewStatusPending, time.Now().UTC()); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if got := readStored(t, rev.ID); got.Status != string(models.ReviewStatusApproved) || got.ExpiresAt != nil {
		t.Fatalf("after approval = %+v, want APPROVED with no deadline", got)
	}
	claimed, status, err := models.ClaimApprovedSidecarReview(models.DB, testOrgID, rev.ID, time.Now().UTC().Add(20*time.Minute))
	if err != nil || !claimed || status != models.ReviewStatusExecuted {
		t.Fatalf("claim = (%v, %s, %v), want (true, EXECUTED)", claimed, status, err)
	}
}

// A lapsed approval releases nothing. The claim does not record the expiry.
func testClaimNeverReleasesAnExpiredReview(t *testing.T) {
	sc := seedSidecar(t, "claim-expired")

	for _, tt := range []struct {
		name        string
		deadline    *time.Time
		wantClaimed bool
		wantStatus  models.ReviewStatusType
	}{
		{"past the deadline", timeAt(-time.Minute), false, models.ReviewStatusApproved},
		{"inside the deadline", timeAt(time.Hour), true, models.ReviewStatusExecuted},
		{"no deadline", nil, true, models.ReviewStatusExecuted},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rev := seedApprovedSidecarReview(t, sc, "DELETE FROM "+uuid.NewString()+";")
			setDeadline(t, rev.ID, tt.deadline)
			before := sessionStatus(t, rev.SessionID)

			claimed, status, err := models.ClaimApprovedSidecarReview(models.DB, testOrgID, rev.ID, time.Now().UTC())
			if err != nil || claimed != tt.wantClaimed || status != tt.wantStatus {
				t.Fatalf("claim = (%v, %s, %v), want (%v, %s)", claimed, status, err, tt.wantClaimed, tt.wantStatus)
			}
			if !tt.wantClaimed {
				if s := sessionStatus(t, rev.SessionID); s != before {
					t.Errorf("a refused claim moved the session from %q to %q", before, s)
				}
			}
		})
	}
}

// Deadlines compare against the now the caller passes, in UTC. The columns
// have no time zone, and a driver binds a time by its wall clock.
func testDeadlinesUseTheBoundClock(t *testing.T) {
	sc := seedSidecar(t, "bound-clock")
	dbDeadline := func(reviewID string) {
		t.Helper()
		if err := models.DB.Exec(`UPDATE private.reviews SET expires_at = (now() AT TIME ZONE 'UTC') + interval '1 hour'
			WHERE org_id = ? AND id = ?`, testOrgID, reviewID).Error; err != nil {
			t.Fatalf("set deadline: %v", err)
		}
	}
	west := time.FixedZone("x", -5*3600)
	east := time.FixedZone("y", 5*3600)

	const statement = "DELETE FROM clock;"
	rev := seedApprovedSidecarReview(t, sc, statement)
	dbDeadline(rev.ID)

	claimed, status, err := models.ClaimApprovedSidecarReview(models.DB, testOrgID, rev.ID, time.Now().UTC().Add(2*time.Hour))
	if err != nil || claimed || status != models.ReviewStatusApproved {
		t.Fatalf("a claim bound past the deadline = (%v, %s, %v), want (false, APPROVED)", claimed, status, err)
	}
	pastWest := time.Now().Add(2 * time.Hour).In(west)
	claimed, status, err = models.ClaimApprovedSidecarReview(models.DB, testOrgID, rev.ID, pastWest)
	if err != nil || claimed || status != models.ReviewStatusApproved {
		t.Fatalf("a zoned claim past the deadline = (%v, %s, %v), want (false, APPROVED)", claimed, status, err)
	}
	if got, err := liveReview(sc, statement, pastWest); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("a zoned lookup past the deadline returned %+v, err=%v", got, err)
	}
	if got, err := liveReview(sc, statement, time.Now().In(east)); err != nil || got.ID != rev.ID {
		t.Fatalf("a zoned lookup inside the deadline lost the review: got %+v, err=%v", got, err)
	}
	claimed, status, err = models.ClaimApprovedSidecarReview(models.DB, testOrgID, rev.ID, time.Now().In(east))
	if err != nil || !claimed || status != models.ReviewStatusExecuted {
		t.Fatalf("a zoned claim inside the deadline = (%v, %s, %v), want (true, EXECUTED)", claimed, status, err)
	}

	pending := seedSidecarReview(t, sc, "DELETE FROM clock2;")
	dbDeadline(pending.ID)
	if expired, status, err := models.ExpireSidecarReview(models.DB, testOrgID, pending.ID, time.Now().In(east)); err != nil || expired || status != models.ReviewStatusPending {
		t.Fatalf("a zoned expiry inside the deadline = (%v, %s, %v), want (false, PENDING)", expired, status, err)
	}
	if expired, status, err := models.ExpireSidecarReview(models.DB, testOrgID, pending.ID, pastWest); err != nil || !expired || status != models.ReviewStatusExpired {
		t.Fatalf("a zoned expiry past the deadline = (%v, %s, %v), want (true, EXPIRED)", expired, status, err)
	}
}

func testExpireSidecarReview(t *testing.T) {
	sc := seedSidecar(t, "expire")

	t.Run("records a lapsed review once", func(t *testing.T) {
		const statement = "DELETE FROM lapsed;"
		rev := seedSidecarReview(t, sc, statement)
		setDeadline(t, rev.ID, timeAt(-time.Minute))

		expired, status, err := models.ExpireSidecarReview(models.DB, testOrgID, rev.ID, time.Now().UTC())
		if err != nil || !expired || status != models.ReviewStatusExpired {
			t.Fatalf("expire = (%v, %s, %v), want (true, EXPIRED)", expired, status, err)
		}
		if got := readStored(t, rev.ID); got.Status != string(models.ReviewStatusExpired) || got.StatementHash.Valid {
			t.Errorf("stored = %+v, want EXPIRED with no hash", got)
		}
		if s := sessionStatus(t, rev.SessionID); s != "done" {
			t.Errorf("session = %q, want done", s)
		}

		expired, status, err = models.ExpireSidecarReview(models.DB, testOrgID, rev.ID, time.Now().UTC())
		if err != nil || expired || status != models.ReviewStatusExpired {
			t.Fatalf("second expire = (%v, %s, %v), want (false, EXPIRED)", expired, status, err)
		}

		// the predicate an older binary looks a statement up with
		var n int64
		if err := models.DB.Raw(`SELECT count(*) FROM private.reviews
			WHERE org_id = ? AND sidecar_id = ? AND listener_name = ? AND access_request_rule_name = ?
			AND statement_hash = ? AND status NOT IN ('EXECUTED', 'REJECTED', 'REVOKED')`,
			testOrgID, sc.ID, "appdb", ttlRule, models.HashStatement([]byte(statement))).Scan(&n).Error; err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != 0 {
			t.Errorf("the 000125 predicate still matches %d rows", n)
		}
	})

	for _, tt := range []struct {
		name     string
		deadline *time.Time
	}{
		{"a future deadline", timeAt(time.Hour)},
		{"no deadline", nil},
	} {
		t.Run("leaves "+tt.name, func(t *testing.T) {
			rev := seedSidecarReview(t, sc, "DELETE FROM "+uuid.NewString()+";")
			setDeadline(t, rev.ID, tt.deadline)
			expired, status, err := models.ExpireSidecarReview(models.DB, testOrgID, rev.ID, time.Now().UTC())
			if err != nil || expired || status != models.ReviewStatusPending {
				t.Fatalf("expire = (%v, %s, %v), want (false, PENDING)", expired, status, err)
			}
		})
	}

	t.Run("leaves a rejected review", func(t *testing.T) {
		rev := seedSidecarReview(t, sc, "DELETE FROM rejected;")
		setDeadline(t, rev.ID, timeAt(-time.Minute))
		if err := models.UpdateReviewStatus(testOrgID, rev.ID, models.ReviewStatusRejected); err != nil {
			t.Fatalf("reject: %v", err)
		}
		expired, status, err := models.ExpireSidecarReview(models.DB, testOrgID, rev.ID, time.Now().UTC())
		if err != nil || expired || status != models.ReviewStatusRejected {
			t.Fatalf("expire = (%v, %s, %v), want (false, REJECTED)", expired, status, err)
		}
		if got := readStored(t, rev.ID); !got.StatementHash.Valid {
			t.Errorf("a rejected review lost its hash")
		}
	})

	t.Run("leaves a review with no listener", func(t *testing.T) {
		rev := seedSidecarReview(t, sc, "DELETE FROM nolistener;")
		setDeadline(t, rev.ID, timeAt(-time.Minute))
		if err := models.DB.Exec(`UPDATE private.reviews SET listener_name = NULL WHERE id = ?`, rev.ID).Error; err != nil {
			t.Fatalf("clear listener: %v", err)
		}
		expired, status, err := models.ExpireSidecarReview(models.DB, testOrgID, rev.ID, time.Now().UTC())
		if err != nil || expired || status != models.ReviewStatusPending {
			t.Fatalf("expire = (%v, %s, %v), want (false, PENDING)", expired, status, err)
		}
		if s := sessionStatus(t, rev.SessionID); s != "open" {
			t.Errorf("session = %q, want open", s)
		}
	})

	t.Run("an unknown id", func(t *testing.T) {
		_, _, err := models.ExpireSidecarReview(models.DB, testOrgID, uuid.NewString(), time.Now().UTC())
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("err = %v, want gorm.ErrRecordNotFound", err)
		}
	})
}

// A call binds its time, then can wait on the row lock past the deadline. A
// stale bound time stands for that wait: each check also reads the database clock.
func testDeadlinesUseTheDatabaseClock(t *testing.T) {
	sc := seedSidecar(t, "db-clock")
	lapsed := timeAt(-time.Minute)
	stale := time.Now().UTC().Add(-2 * time.Minute)

	t.Run("a claim", func(t *testing.T) {
		rev := seedApprovedSidecarReview(t, sc, "DELETE FROM claim_clock;")
		setDeadline(t, rev.ID, lapsed)
		claimed, status, err := models.ClaimApprovedSidecarReview(models.DB, testOrgID, rev.ID, stale)
		if err != nil || claimed || status != models.ReviewStatusApproved {
			t.Fatalf("a stale claim = (%v, %s, %v), want (false, APPROVED)", claimed, status, err)
		}
	})
	t.Run("a decision", func(t *testing.T) {
		rev := seedSidecarReview(t, sc, "DELETE FROM decision_clock;")
		setDeadline(t, rev.ID, lapsed)
		err := models.UpdateSidecarReview(models.DB, approved(loadSidecarReview(t, sc, rev.ID)), models.ReviewStatusPending, stale)
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("a stale decision returned %v, want gorm.ErrRecordNotFound", err)
		}
		if got := readStored(t, rev.ID).Status; got != string(models.ReviewStatusPending) {
			t.Fatalf("a stale decision wrote %s", got)
		}
	})
	t.Run("a lookup and an expiry", func(t *testing.T) {
		const statement = "DELETE FROM expiry_clock;"
		rev := seedSidecarReview(t, sc, statement)
		setDeadline(t, rev.ID, lapsed)
		if got, err := liveReview(sc, statement, stale); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("a stale lookup found %+v, err=%v", got, err)
		}
		expired, status, err := models.ExpireSidecarReview(models.DB, testOrgID, rev.ID, stale)
		if err != nil || !expired || status != models.ReviewStatusExpired {
			t.Fatalf("a stale expiry = (%v, %s, %v), want (true, EXPIRED)", expired, status, err)
		}
	})
}

func testUpdateSidecarReviewDeadline(t *testing.T) {
	sc := seedSidecar(t, "decision-deadline")

	t.Run("an approval starts the approval clock", func(t *testing.T) {
		rev := seedSidecarReview(t, sc, "DELETE FROM clock;")
		setDeadline(t, rev.ID, timeAt(10*time.Minute))
		setApprovalTTL(t, rev.ID, 3600)
		now := time.Now().UTC()
		if err := models.UpdateSidecarReview(models.DB, approved(loadSidecarReview(t, sc, rev.ID)), models.ReviewStatusPending, now); err != nil {
			t.Fatalf("approve: %v", err)
		}
		got := readStored(t, rev.ID)
		if got.ExpiresAt == nil {
			t.Fatalf("the approval has no deadline")
		}
		if d := got.ExpiresAt.Sub(now.Add(time.Hour)); d < -time.Second || d > time.Second {
			t.Errorf("deadline = %v, want %v within 1s", got.ExpiresAt, now.Add(time.Hour))
		}
		if s := sessionStatus(t, rev.SessionID); s != "ready" {
			t.Errorf("session = %q, want ready", s)
		}
	})

	t.Run("no approval limit clears the deadline", func(t *testing.T) {
		rev := seedSidecarReview(t, sc, "DELETE FROM clear;")
		setDeadline(t, rev.ID, timeAt(10*time.Minute))
		if err := models.UpdateSidecarReview(models.DB, approved(loadSidecarReview(t, sc, rev.ID)), models.ReviewStatusPending, time.Now().UTC()); err != nil {
			t.Fatalf("approve: %v", err)
		}
		if got := readStored(t, rev.ID); got.ExpiresAt != nil {
			t.Errorf("deadline = %v, want NULL", got.ExpiresAt)
		}
	})

	t.Run("approved to approved keeps the deadline", func(t *testing.T) {
		rev := seedApprovedSidecarReview(t, sc, "DELETE FROM keep;")
		deadline := timeAt(30 * time.Minute).Truncate(time.Microsecond)
		setDeadline(t, rev.ID, &deadline)
		setApprovalTTL(t, rev.ID, 3600)
		if err := models.UpdateSidecarReview(models.DB, loadSidecarReview(t, sc, rev.ID), models.ReviewStatusApproved, time.Now().UTC()); err != nil {
			t.Fatalf("update: %v", err)
		}
		if got := readStored(t, rev.ID); got.ExpiresAt == nil || !got.ExpiresAt.Equal(deadline) {
			t.Errorf("deadline = %v, want %v", got.ExpiresAt, deadline)
		}
	})

	t.Run("past the deadline writes nothing", func(t *testing.T) {
		rev := seedSidecarReview(t, sc, "DELETE FROM late;")
		seedPendingGroup(t, rev.ID)
		setDeadline(t, rev.ID, timeAt(10*time.Minute))
		read := loadSidecarReview(t, sc, rev.ID)
		err := models.UpdateSidecarReview(models.DB, approved(read), models.ReviewStatusPending, time.Now().UTC().Add(20*time.Minute))
		if !errors.Is(err, models.ErrSidecarReviewExpired) {
			t.Fatalf("err = %v, want ErrSidecarReviewExpired", err)
		}
		assertUndecided(t, sc, rev)
	})

	t.Run("a deadline that passed after the read", func(t *testing.T) {
		rev := seedSidecarReview(t, sc, "DELETE FROM raced;")
		seedPendingGroup(t, rev.ID)
		setDeadline(t, rev.ID, timeAt(10*time.Minute))
		read := loadSidecarReview(t, sc, rev.ID)
		setDeadline(t, rev.ID, timeAt(-time.Minute))
		err := models.UpdateSidecarReview(models.DB, approved(read), models.ReviewStatusPending, time.Now().UTC())
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("err = %v, want gorm.ErrRecordNotFound", err)
		}
		if got := readStored(t, rev.ID); got.Status != string(models.ReviewStatusPending) {
			t.Errorf("status = %s, want PENDING", got.Status)
		}
		if s := sessionStatus(t, rev.SessionID); s != "open" {
			t.Errorf("session = %q, want open", s)
		}
	})
}

// assertUndecided checks the review, its group and its session hold what filing wrote.
func assertUndecided(t *testing.T, sc *models.Sidecar, rev *models.Review) {
	t.Helper()
	got := loadSidecarReview(t, sc, rev.ID)
	if got.Status != models.ReviewStatusPending {
		t.Errorf("status = %s, want PENDING", got.Status)
	}
	if len(got.ReviewGroups) != 1 || got.ReviewGroups[0].Status != models.ReviewStatusPending || got.ReviewGroups[0].OwnerEmail != nil {
		t.Errorf("review groups = %+v, want one PENDING group with no reviewer", got.ReviewGroups)
	}
	if s := sessionStatus(t, rev.SessionID); s != "open" {
		t.Errorf("session = %q, want open", s)
	}
}

// Every read shows a lapsed sidecar review as EXPIRED and writes nothing.
func testReadsReportAnExpiredSidecarReview(t *testing.T) {
	sc := seedSidecar(t, "reads")

	lapsed := seedSidecarReview(t, sc, "DELETE FROM lapsed;")
	setDeadline(t, lapsed.ID, timeAt(-time.Minute))
	lapsedApproval := seedApprovedSidecarReview(t, sc, "DELETE FROM approval;")
	setDeadline(t, lapsedApproval.ID, timeAt(-time.Minute))
	live := seedSidecarReview(t, sc, "DELETE FROM live;")
	setDeadline(t, live.ID, timeAt(time.Hour))
	rejected := seedSidecarReview(t, sc, "DELETE FROM rejected;")
	setDeadline(t, rejected.ID, timeAt(-time.Minute))
	if err := models.UpdateReviewStatus(testOrgID, rejected.ID, models.ReviewStatusRejected); err != nil {
		t.Fatalf("reject: %v", err)
	}
	noListener := seedSidecarReview(t, sc, "DELETE FROM nolistener;")
	setDeadline(t, noListener.ID, timeAt(-time.Minute))
	if err := models.DB.Exec(`UPDATE private.reviews SET listener_name = NULL WHERE id = ?`, noListener.ID).Error; err != nil {
		t.Fatalf("clear listener: %v", err)
	}

	want := map[string]models.ReviewStatusType{
		lapsed.ID:         models.ReviewStatusExpired,
		lapsedApproval.ID: models.ReviewStatusExpired,
		live.ID:           models.ReviewStatusPending,
		rejected.ID:       models.ReviewStatusRejected,
		noListener.ID:     models.ReviewStatusPending,
	}
	stored := map[string]string{
		lapsed.ID:         string(models.ReviewStatusPending),
		lapsedApproval.ID: string(models.ReviewStatusApproved),
		live.ID:           string(models.ReviewStatusPending),
		rejected.ID:       string(models.ReviewStatusRejected),
		noListener.ID:     string(models.ReviewStatusPending),
	}

	list, err := models.ListReviews(testOrgID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	listed := map[string]models.ReviewStatusType{}
	for _, r := range *list {
		listed[r.ID] = r.Status
	}
	for id, status := range want {
		byID, err := models.GetReviewByIdOrSid(testOrgID, id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if byID.Status != status {
			t.Errorf("GetReviewByIdOrSid(%s) = %s, want %s", id, byID.Status, status)
		}
		bySid, err := models.GetReviewByIdOrSid(testOrgID, byID.SessionID)
		if err != nil || bySid.Status != status {
			t.Errorf("GetReviewByIdOrSid(session) = %v %v, want %s", bySid, err, status)
		}
		if listed[id] != status {
			t.Errorf("ListReviews(%s) = %s, want %s", id, listed[id], status)
		}
		if got := loadSidecarReview(t, sc, id); got.Status != status {
			t.Errorf("GetSidecarReview(%s) = %s, want %s", id, got.Status, status)
		}
		if got := readStored(t, id); got.Status != stored[id] {
			t.Errorf("stored status of %s = %s, want %s: a read must not write", id, got.Status, stored[id])
		}
	}
	if got := loadSidecarReview(t, sc, lapsed.ID); got.SidecarExpiresAt() == nil {
		t.Errorf("an expired read shows no deadline")
	}
}

// A gateway review never gets a deadline, on filing or on a decision.
func testGatewayReviewHasNoDeadline(t *testing.T) {
	sessionID := uuid.NewString()
	now := time.Now().UTC()
	if err := models.UpsertSession(models.Session{
		ID: sessionID, OrgID: testOrgID, BlobInput: models.BlobInputType("select 1;"),
		ConnectionType: "database", Verb: "exec", Status: "open",
		UserID: "user-1", UserName: "user one", UserEmail: "user@hoop.dev", CreatedAt: now,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	rev := &models.Review{
		ID: uuid.NewString(), OrgID: testOrgID, SessionID: sessionID,
		Type: models.ReviewTypeOneTime, Status: models.ReviewStatusPending,
		ConnectionName: "pg-prod", OwnerID: "user-1", OwnerEmail: "user@hoop.dev", CreatedAt: now,
	}
	if err := models.CreateReview(rev, "select 1;"); err != nil {
		t.Fatalf("create: %v", err)
	}
	assertNoDeadline := func(when string) {
		t.Helper()
		var row struct {
			ExpiresAt      *time.Time
			ApprovalTTLSec *int
		}
		if err := models.DB.Raw(`SELECT expires_at, approval_ttl_sec FROM private.reviews WHERE id = ?`, rev.ID).
			Scan(&row).Error; err != nil {
			t.Fatalf("read: %v", err)
		}
		if row.ExpiresAt != nil || row.ApprovalTTLSec != nil {
			t.Errorf("%s: expires_at=%v approval_ttl_sec=%v, want both NULL", when, row.ExpiresAt, row.ApprovalTTLSec)
		}
	}
	assertNoDeadline("after filing")

	rev.Status = models.ReviewStatusApproved
	if err := models.UpdateReview(rev); err != nil {
		t.Fatalf("update: %v", err)
	}
	assertNoDeadline("after the decision")
	got, err := models.GetReviewByIdOrSid(testOrgID, rev.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != models.ReviewStatusApproved || got.SidecarExpiresAt() != nil || got.SidecarApprovalTTLSec() != nil {
		t.Errorf("read = %s expires=%v ttl=%v, want APPROVED with no deadline", got.Status, got.SidecarExpiresAt(), got.SidecarApprovalTTLSec())
	}
}

// EXPIRED is left out of reviewStatusLabels on purpose, and only EXPIRED.
func testReviewStatusLabelsLeaveOutOnlyExpired(t *testing.T) {
	var labels []string
	if err := models.DB.Raw(`SELECT unnest(enum_range(NULL::private.enum_reviews_status))::text`).
		Scan(&labels).Error; err != nil {
		t.Fatalf("read the enum: %v", err)
	}
	if !slices.Contains(labels, string(models.ReviewStatusExpired)) {
		t.Fatalf("the enum has no EXPIRED label: %v", labels)
	}
	for _, label := range labels {
		if got, want := models.IsValidReviewStatus(label), label != string(models.ReviewStatusExpired); got != want {
			t.Errorf("IsValidReviewStatus(%q) = %v, want %v", label, got, want)
		}
	}
}

// The down migration settles what an older binary would release, keeps every
// row and leaves the 000125 index working.
func testReviewTTLMigrationRollsBack(t *testing.T) {
	sc := seedSidecar(t, "ttl-rollback")
	const lapsedStatement = "DELETE FROM lapsed;"
	lapsed := seedApprovedSidecarReview(t, sc, lapsedStatement)
	setDeadline(t, lapsed.ID, timeAt(-time.Minute))
	live := seedSidecarReview(t, sc, "DELETE FROM live;")
	setDeadline(t, live.ID, timeAt(time.Hour))
	// an EXPIRED row that kept its hash, which no writer of this binary leaves
	stray := seedSidecarReview(t, sc, "DELETE FROM stray;")
	if err := models.UpdateReviewStatus(testOrgID, stray.ID, models.ReviewStatusExpired); err != nil {
		t.Fatalf("set status: %v", err)
	}
	var before int64
	if err := models.DB.Raw(`SELECT count(*) FROM private.reviews`).Scan(&before).Error; err != nil {
		t.Fatalf("count: %v", err)
	}

	down, err := migrations.FS.ReadFile("000128_sidecar_review_ttl.down.sql")
	if err != nil {
		t.Fatalf("read the down migration: %v", err)
	}
	if err := models.DB.Exec(string(down)).Error; err != nil {
		t.Fatalf("the down migration failed: %v", err)
	}

	type row struct {
		Status        string
		StatementHash sql.NullString
	}
	read := func(id string) row {
		t.Helper()
		var r row
		if err := models.DB.Raw(`SELECT status, statement_hash FROM private.reviews WHERE id = ?`, id).Scan(&r).Error; err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		return r
	}
	if got := read(lapsed.ID); got.Status != string(models.ReviewStatusExpired) || got.StatementHash.Valid {
		t.Errorf("lapsed approval = %+v, want EXPIRED with no hash", got)
	}
	if s := sessionStatus(t, lapsed.SessionID); s != "done" {
		t.Errorf("lapsed session = %q, want done", s)
	}
	if got := read(live.ID); got.Status != string(models.ReviewStatusPending) || !got.StatementHash.Valid {
		t.Errorf("live review = %+v, want PENDING with its hash", got)
	}
	if s := sessionStatus(t, live.SessionID); s != "open" {
		t.Errorf("live session = %q, want open", s)
	}
	if got := read(stray.ID); got.StatementHash.Valid {
		t.Errorf("an EXPIRED row kept its hash")
	}
	var after int64
	if err := models.DB.Raw(`SELECT count(*) FROM private.reviews`).Scan(&after).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if after != before {
		t.Errorf("reviews = %d after rollback, want %d", after, before)
	}
	var columns []string
	if err := models.DB.Raw(`SELECT table_name || '.' || column_name FROM information_schema.columns
		WHERE table_schema = 'private'
		AND ((table_name = 'reviews' AND column_name IN ('expires_at', 'approval_ttl_sec'))
		OR (table_name = 'access_request_rules' AND column_name IN ('pending_ttl_sec', 'approval_ttl_sec')))`).
		Scan(&columns).Error; err != nil {
		t.Fatalf("read columns: %v", err)
	}
	if len(columns) != 0 {
		t.Errorf("columns left after rollback: %v", columns)
	}

	// An older binary files the lapsed statement again; the live one stays unique.
	insert := func(statement string) error {
		return models.DB.Exec(`INSERT INTO private.reviews
			(id, org_id, session_id, connection_name, type, status, owner_id, owner_email,
			sidecar_id, listener_name, access_request_rule_name, statement_hash)
			VALUES (?, ?, ?, '', 'onetime', 'PENDING', ?, 'hoop@hoop.dev', ?, 'appdb', ?, ?)`,
			uuid.NewString(), testOrgID, uuid.NewString(), sc.ID, sc.ID, ttlRule,
			models.HashStatement([]byte(statement))).Error
	}
	if err := insert(lapsedStatement); err != nil {
		t.Errorf("refiling the lapsed statement failed: %v", err)
	}
	if err := insert("DELETE FROM live;"); !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Errorf("a duplicate of the live review: err=%v, want gorm.ErrDuplicatedKey", err)
	}
}

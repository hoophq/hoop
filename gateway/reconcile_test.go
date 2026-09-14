package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/pglite"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const reconcileTestOrgID = "00000000-0000-0000-0000-0000000000b1"

// startTestDB boots the embedded database, applies the migrations and points
// models.DB at it, exactly like Run does on startup. The reconciliation is one
// UPDATE joining two tables on a status enum, so an in-memory fake would not
// exercise what actually breaks.
func startTestDB(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	ctx := context.Background()
	inst, err := pglite.Start(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("start embedded database: %v", err)
	}
	// A close that fails leaks an embedded Postgres process and its port. Left
	// unchecked it surfaces later, as a confusing failure in an unrelated test,
	// so it fails the test that leaked it instead (as gateway/pglite does).
	t.Cleanup(func() {
		if err := inst.Close(ctx); err != nil {
			t.Errorf("close embedded database: %v", err)
		}
	})

	if err := modelsbootstrap.MigrateDB(inst.MigrateDSN(), ""); err != nil {
		t.Fatalf("migrations failed: %v", err)
	}
	// The embedded backend serves one session at a time.
	if err := models.InitDatabaseConnection(inst.DSN(), 1); err != nil {
		t.Fatalf("open gorm connection: %v", err)
	}
	if err := models.DB.Exec(
		`INSERT INTO private.orgs (id, name) VALUES (?, 'reconcile-test')`, reconcileTestOrgID).Error; err != nil {
		t.Fatalf("seed org: %v", err)
	}
}

// seedReview writes one session in sessionStatus and the one-time review that
// belongs to it, and returns the review id.
func seedReview(t *testing.T, sessionStatus string, reviewStatus models.ReviewStatusType) string {
	t.Helper()
	now := time.Now().UTC()
	sessionID := uuid.NewString()
	reviewID := uuid.NewString()

	err := models.UpsertSession(models.Session{
		ID:             sessionID,
		OrgID:          reconcileTestOrgID,
		BlobInput:      models.BlobInputType("select 1;"),
		ConnectionType: "custom",
		Verb:           pb.ClientVerbExec,
		Status:         sessionStatus,
		UserID:         "user-1",
		UserName:       "user one",
		UserEmail:      "user@hoop.dev",
		CreatedAt:      now,
	})
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}

	err = models.CreateReview(&models.Review{
		ID:         reviewID,
		OrgID:      reconcileTestOrgID,
		SessionID:  sessionID,
		Type:       models.ReviewTypeOneTime,
		Status:     reviewStatus,
		OwnerID:    "user-1",
		OwnerEmail: "user@hoop.dev",
		CreatedAt:  now,
	}, "select 1;")
	if err != nil {
		t.Fatalf("seed review: %v", err)
	}
	return reviewID
}

func reviewStatus(t *testing.T, reviewID string) models.ReviewStatusType {
	t.Helper()
	rev, err := models.GetReviewByIdOrSid(reconcileTestOrgID, reviewID)
	if err != nil {
		t.Fatalf("read review %s: %v", reviewID, err)
	}
	return rev.Status
}

// The job is silent when it works and nothing downstream notices a review that
// never settles, so this is what fails if the wiring or the join stops
// matching. Both boot paths call it, and for the control plane the rows it
// finds were written by a gateway sharing the same database (ADR-0013).
func TestReconcileStaleReviewsSettlesAnExecutionThatFinished(t *testing.T) {
	startTestDB(t)

	// Left behind by an execution whose session closed while the process
	// that started it was down.
	stale := seedReview(t, "done", models.ReviewStatusProcessing)

	reconcileStaleReviews(models.DB)

	if got := reviewStatus(t, stale); got != models.ReviewStatusExecuted {
		t.Errorf("stale review = %q, want %q", got, models.ReviewStatusExecuted)
	}
}

// A review waiting on a human has not been executed by anyone, so settling it
// as EXECUTED would close it without an approval and let the statement past
// the gate. This is the failure worth a test of its own.
func TestReconcileStaleReviewsLeavesAReviewNobodyRanAlone(t *testing.T) {
	startTestDB(t)

	pendingOpen := seedReview(t, "open", models.ReviewStatusPending)
	// Pending, but its session is finished: still nobody executed it.
	pendingDone := seedReview(t, "done", models.ReviewStatusPending)
	// Running, and the session says so.
	processingOpen := seedReview(t, "open", models.ReviewStatusProcessing)

	reconcileStaleReviews(models.DB)

	for name, id := range map[string]string{
		"pending with an open session":    pendingOpen,
		"pending with a finished session": pendingDone,
		"processing with an open session": processingOpen,
	} {
		if got := reviewStatus(t, id); got == models.ReviewStatusExecuted {
			t.Errorf("%s was settled as %q, want it untouched", name, got)
		}
	}
}

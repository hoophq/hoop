package models_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

// The hoop-inspect claim is the one place "single use" stops being a comment
// and becomes a property, and it is a property of the SQL rather than of any
// Go code around it. These run against the real schema for that reason: an
// in-memory fake would happily agree with a query that a database rejects, and
// the failure mode of getting this wrong is an approval that authorizes more
// than one execution.

const (
	inspectConnA = "00000000-0000-0000-0000-00000000c0a1"
	inspectConnB = "00000000-0000-0000-0000-00000000c0b2"
	inspectOwner = "sandbox-a"
	inspectHash  = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	otherHash    = "0000000000000000000000000000000000000000000000000000000000000000"
)

// seedInspectReview inserts one review the way the create endpoint would, and
// returns its id. minutesAgo backdates created_at so ordering is testable
// without sleeping.
func seedInspectReview(t *testing.T, owner, connID, hash, marker, status string, minutesAgo int) string {
	t.Helper()
	id := uuid.NewString()
	var markerArg any
	if marker != "" {
		markerArg = marker
	}
	err := models.DB.Exec(`
		INSERT INTO private.reviews
			(id, org_id, session_id, connection_id, connection_name, type, status,
			 owner_id, owner_email, statement_hash, request_marker, created_at)
		VALUES (?, ?, ?, ?, 'appdb', 'onetime', ?::private.enum_reviews_status,
			 ?, ?, ?, ?, NOW() - make_interval(mins => ?))`,
		id, testOrgID, uuid.NewString(), connID, status,
		owner, owner+"@sandbox", hash, markerArg, minutesAgo).Error
	if err != nil {
		t.Fatalf("seed review: %v", err)
	}
	return id
}

func statusOf(t *testing.T, reviewID string) string {
	t.Helper()
	var got string
	err := models.DB.Raw(`SELECT status::text FROM private.reviews WHERE id = ?`, reviewID).
		Scan(&got).Error
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	return got
}

// An approval authorizes exactly one execution. The claim consumes it in the
// same statement that selects it, so the second attempt finds nothing and the
// relay takes the denial path.
func TestClaimInspectReviewIsSingleUse(t *testing.T) {
	startTestDB(t)
	id := seedInspectReview(t, inspectOwner, inspectConnA, inspectHash, "", "APPROVED", 0)

	got, err := models.ClaimInspectReview(models.DB, testOrgID, inspectOwner, inspectConnA, inspectHash)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if got.ID != id {
		t.Errorf("claimed %s, want %s", got.ID, id)
	}
	if s := statusOf(t, id); s != "EXECUTED" {
		t.Errorf("status after claim = %s, want EXECUTED", s)
	}

	_, err = models.ClaimInspectReview(models.DB, testOrgID, inspectOwner, inspectConnA, inspectHash)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("second claim: err = %v, want ErrRecordNotFound — an approval was reused", err)
	}
}

// Every status other than APPROVED denies, and denies without mutating
// anything: a claim must never move a review a human rejected.
func TestClaimInspectReviewOnlyMatchesApproved(t *testing.T) {
	startTestDB(t)

	for _, status := range []string{"PENDING", "REJECTED", "REVOKED", "EXECUTED", "PROCESSING"} {
		hash := strings.Repeat("a", 63) + string(rune('0'+len(status)%10))
		id := seedInspectReview(t, inspectOwner, inspectConnA, hash, "", status, 0)

		_, err := models.ClaimInspectReview(models.DB, testOrgID, inspectOwner, inspectConnA, hash)
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Errorf("%s was claimable: err = %v", status, err)
		}
		if s := statusOf(t, id); s != status {
			t.Errorf("%s review was mutated to %s", status, s)
		}
	}
}

// Scoping comes from the credential and from the connection, not from the
// statement hash. Each of these is a real bypass if it does not hold — the
// connection one especially: a sandbox may reach several, and an approval for
// appdb must not authorize the same SQL against payments-db.
func TestClaimInspectReviewIsScoped(t *testing.T) {
	startTestDB(t)
	seedInspectReview(t, inspectOwner, inspectConnA, inspectHash, "", "APPROVED", 0)

	otherOrg := "00000000-0000-0000-0000-0000000000b2"
	if err := models.DB.Exec(
		`INSERT INTO private.orgs (id, name) VALUES (?, 'other-org')`, otherOrg).Error; err != nil {
		t.Fatalf("seed org: %v", err)
	}

	for name, args := range map[string][4]string{
		"another sandbox":    {testOrgID, "sandbox-b", inspectConnA, inspectHash},
		"another connection": {testOrgID, inspectOwner, inspectConnB, inspectHash},
		"another org":        {otherOrg, inspectOwner, inspectConnA, inspectHash},
		"another statement":  {testOrgID, inspectOwner, inspectConnA, otherHash},
	} {
		_, err := models.ClaimInspectReview(models.DB, args[0], args[1], args[2], args[3])
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Errorf("%s claimed an approval that is not its own: err = %v", name, err)
		}
	}

	// The rightful caller still gets it.
	if _, err := models.ClaimInspectReview(models.DB, testOrgID, inspectOwner, inspectConnA, inspectHash); err != nil {
		t.Fatalf("the owning sandbox was refused its own approval: %v", err)
	}
}

// A hash is not unique: the same statement legitimately accumulates many
// reviews, and more than one can be APPROVED at once. Without ORDER BY the
// choice is nondeterministic, which makes "which approval did this consume" an
// unanswerable question in an audit.
func TestClaimInspectReviewTakesTheOldestApproval(t *testing.T) {
	startTestDB(t)
	oldest := seedInspectReview(t, inspectOwner, inspectConnA, inspectHash, "", "APPROVED", 30)
	newest := seedInspectReview(t, inspectOwner, inspectConnA, inspectHash, "", "APPROVED", 5)

	first, err := models.ClaimInspectReview(models.DB, testOrgID, inspectOwner, inspectConnA, inspectHash)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if first.ID != oldest {
		t.Fatalf("claimed %s, want the oldest approval %s", first.ID, oldest)
	}
	second, err := models.ClaimInspectReview(models.DB, testOrgID, inspectOwner, inspectConnA, inspectHash)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if second.ID != newest {
		t.Errorf("claimed %s, want %s", second.ID, newest)
	}
}

// Two connections from one sandbox running the same statement against one
// approval must produce exactly one execution.
//
// The embedded backend serves one session at a time, so these claims are
// serialized rather than genuinely simultaneous. That still proves the part
// that can be wrong in the query — the conditional UPDATE, which is what makes
// the grant single-use — while the row lock it also relies on is only
// exercised under a real pool.
func TestClaimInspectReviewYieldsOneWinner(t *testing.T) {
	startTestDB(t)
	seedInspectReview(t, inspectOwner, inspectConnA, inspectHash, "", "APPROVED", 0)

	const racers = 8
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		winners []string
	)
	for range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := models.ClaimInspectReview(models.DB, testOrgID, inspectOwner, inspectConnA, inspectHash)
			if err != nil {
				return
			}
			mu.Lock()
			winners = append(winners, got.ID)
			mu.Unlock()
		}()
	}
	wg.Wait()

	if len(winners) != 1 {
		t.Fatalf("%d claims succeeded against one approval, want 1", len(winners))
	}
}

// The create path dedupes on the marker, because the claim filters on APPROVED
// and a retry issued before a human looked at the queue would otherwise never
// see its own PENDING review.
func TestPendingInspectReviewLookupByMarker(t *testing.T) {
	startTestDB(t)
	pending := seedInspectReview(t, inspectOwner, inspectConnA, inspectHash, "task-42", "PENDING", 0)

	got, err := models.GetPendingInspectReviewByMarker(models.DB, testOrgID, inspectOwner, inspectConnA, "task-42")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.ID != pending {
		t.Errorf("found %s, want %s", got.ID, pending)
	}

	// A different marker is a different request, even for identical SQL:
	// that is the whole reason the marker exists.
	if _, err := models.GetPendingInspectReviewByMarker(
		models.DB, testOrgID, inspectOwner, inspectConnA, "task-99"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("a different marker matched: %v", err)
	}

	// An answered review does not suppress a later ask. Each request is
	// answered on its own merits.
	seedInspectReview(t, inspectOwner, inspectConnA, inspectHash, "task-7", "REJECTED", 0)
	if _, err := models.GetPendingInspectReviewByMarker(
		models.DB, testOrgID, inspectOwner, inspectConnA, "task-7"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("a rejected review was returned as pending: %v", err)
	}
}

// The poll answers "may I retry yet", so an approval outranks a newer review
// in any other state, and polling never consumes anything.
func TestInspectReviewStatusPrefersApproved(t *testing.T) {
	startTestDB(t)
	approved := seedInspectReview(t, inspectOwner, inspectConnA, inspectHash, "", "APPROVED", 30)
	seedInspectReview(t, inspectOwner, inspectConnA, inspectHash, "", "PENDING", 1)

	got, err := models.GetInspectReviewStatus(models.DB, testOrgID, inspectOwner, inspectConnA, inspectHash)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if got.ID != approved || got.Status != models.ReviewStatusApproved {
		t.Errorf("got %s/%s, want the approved review %s", got.ID, got.Status, approved)
	}
	if s := statusOf(t, approved); s != "APPROVED" {
		t.Errorf("polling consumed the approval: status = %s", s)
	}

	if _, err := models.GetInspectReviewStatus(
		models.DB, testOrgID, inspectOwner, inspectConnA, otherHash); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("an unknown statement returned a review: %v", err)
	}
}

// The session behind a claimed review is closed once and stays closed: a
// re-run must not move the timestamp.
func TestCloseInspectSessionIsIdempotent(t *testing.T) {
	startTestDB(t)
	sid := uuid.NewString()
	err := models.DB.Exec(`
		INSERT INTO private.sessions (id, org_id, connection, connection_type, verb, status)
		VALUES (?, ?, 'appdb', 'database', 'exec', 'open')`, sid, testOrgID).Error
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}

	if err := models.CloseInspectSession(models.DB, testOrgID, sid); err != nil {
		t.Fatalf("close: %v", err)
	}
	var first struct {
		Status  string `gorm:"column:status"`
		EndedAt string `gorm:"column:ended_at"`
	}
	read := func() {
		t.Helper()
		if err := models.DB.Raw(
			`SELECT status::text AS status, ended_at::text AS ended_at
			   FROM private.sessions WHERE id = ?`, sid).Scan(&first).Error; err != nil {
			t.Fatalf("read session: %v", err)
		}
	}
	read()
	if first.Status != "done" || first.EndedAt == "" {
		t.Fatalf("session not closed: %+v", first)
	}
	was := first.EndedAt

	if err := models.CloseInspectSession(models.DB, testOrgID, sid); err != nil {
		t.Fatalf("second close: %v", err)
	}
	read()
	if first.EndedAt != was {
		t.Errorf("ended_at moved on a second close: %s -> %s", was, first.EndedAt)
	}
}

// seedOpenSession inserts a session the way the inspect create endpoint would,
// including the input blob UpsertSession writes alongside it.
func seedOpenSession(t *testing.T, status string, ended bool) string {
	t.Helper()
	sid := uuid.NewString()
	blobID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("blobinput:"+sid)).String()

	err := models.DB.Exec(`
		INSERT INTO private.blobs (id, org_id, type, blob_stream)
		VALUES (?, ?, 'session-input', '["DELETE FROM t"]'::jsonb)`, blobID, testOrgID).Error
	if err != nil {
		t.Fatalf("seed blob: %v", err)
	}

	endedAt := "NULL"
	if ended {
		endedAt = "NOW()"
	}
	err = models.DB.Exec(`
		INSERT INTO private.sessions
			(id, org_id, connection, connection_type, verb, status, user_id, user_email,
			 blob_input_id, created_at, ended_at)
		VALUES (?, ?, 'appdb', 'database', 'exec', ?, ?, 'sandbox@a', ?, NOW(), `+endedAt+`)`,
		sid, testOrgID, status, inspectOwner, blobID).Error
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return sid
}

func sessionExists(t *testing.T, sid string) bool {
	t.Helper()
	var n int64
	if err := models.DB.Raw(`SELECT count(1) FROM private.sessions WHERE id = ?`, sid).
		Scan(&n).Error; err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n > 0
}

func blobExists(t *testing.T, sid string) bool {
	t.Helper()
	blobID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("blobinput:"+sid)).String()
	var n int64
	if err := models.DB.Raw(`SELECT count(1) FROM private.blobs WHERE id = ?`, blobID).
		Scan(&n).Error; err != nil {
		t.Fatalf("count blobs: %v", err)
	}
	return n > 0
}

// A session persisted to anchor a review that then failed to be created has no
// reason to exist. Removing it must take the input blob with it, or the leak
// is only half fixed.
func TestDeleteSessionWithInputRemovesBothRows(t *testing.T) {
	startTestDB(t)
	sid := seedOpenSession(t, "open", false)

	if err := models.DeleteSessionWithInput(testOrgID, sid); err != nil {
		t.Fatalf("DeleteSessionWithInput: %v", err)
	}
	if sessionExists(t, sid) {
		t.Error("the session survived")
	}
	if blobExists(t, sid) {
		t.Error("the input blob was orphaned")
	}
}

// Compensation that could delete a session which actually ran is worse than
// the leak it fixes. Anything but an open, unfinished session is left alone.
func TestDeleteSessionWithInputRefusesASessionThatRan(t *testing.T) {
	startTestDB(t)
	for _, tc := range []struct {
		name   string
		status string
		ended  bool
	}{
		{"already finished", "done", true},
		{"open but ended", "open", true},
		{"ready for execution", "ready", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sid := seedOpenSession(t, tc.status, tc.ended)
			err := models.DeleteSessionWithInput(testOrgID, sid)
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Fatalf("err = %v, want ErrRecordNotFound (a no-op)", err)
			}
			if !sessionExists(t, sid) {
				t.Error("a session that ran was deleted")
			}
			if !blobExists(t, sid) {
				t.Error("its input blob was deleted")
			}
		})
	}
}

// Scoped by org, like everything else on this path.
func TestDeleteSessionWithInputIsOrgScoped(t *testing.T) {
	startTestDB(t)
	sid := seedOpenSession(t, "open", false)
	other := uuid.NewString()

	if err := models.DeleteSessionWithInput(other, sid); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("err = %v, want ErrRecordNotFound", err)
	}
	if !sessionExists(t, sid) {
		t.Fatal("another org deleted this session")
	}
}

// insertMarkedReview inserts a review directly, returning the error so a
// constraint violation is observable rather than fatal.
func insertMarkedReview(owner, connID, marker, status string) error {
	var markerArg any
	if marker != "" {
		markerArg = marker
	}
	return models.DB.Exec(`
		INSERT INTO private.reviews
			(id, org_id, session_id, connection_id, connection_name, type, status,
			 owner_id, owner_email, statement_hash, request_marker, created_at)
		VALUES (?, ?, ?, ?, 'appdb', 'onetime', ?::private.enum_reviews_status,
			 ?, ?, ?, ?, NOW())`,
		uuid.NewString(), testOrgID, uuid.NewString(), connID, status,
		owner, owner+"@sandbox", inspectHash, markerArg).Error
}

// The dedupe is a read followed by two separate inserting transactions, so two
// concurrent retries under one marker can both find nothing and both file.
// Nothing in Go can stop that — the gateway runs multiple replicas — so the
// database has to be the referee.
//
// It matters because each approval authorizes one execution: two duplicates
// approved by a reviewer who thought they were one request authorize two runs.
func TestOnePendingReviewPerMarker(t *testing.T) {
	startTestDB(t)

	if err := insertMarkedReview(inspectOwner, inspectConnA, "task-1", "PENDING"); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	err := insertMarkedReview(inspectOwner, inspectConnA, "task-1", "PENDING")
	if err == nil {
		t.Fatal("a second PENDING review under the same marker was accepted; " +
			"concurrent retries can still file duplicates")
	}
	if !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatalf("err = %v, want gorm.ErrDuplicatedKey — the handler branches on it", err)
	}
}

// The constraint has to bind exactly what the dedupe claims and nothing more.
func TestPendingMarkerUniquenessIsNarrow(t *testing.T) {
	startTestDB(t)

	t.Run("a different marker is its own request", func(t *testing.T) {
		if err := insertMarkedReview(inspectOwner, inspectConnA, "task-a", "PENDING"); err != nil {
			t.Fatalf("first: %v", err)
		}
		if err := insertMarkedReview(inspectOwner, inspectConnA, "task-b", "PENDING"); err != nil {
			t.Errorf("two markers collided: %v", err)
		}
	})

	t.Run("a different connection is its own scope", func(t *testing.T) {
		if err := insertMarkedReview(inspectOwner, inspectConnA, "task-c", "PENDING"); err != nil {
			t.Fatalf("first: %v", err)
		}
		if err := insertMarkedReview(inspectOwner, inspectConnB, "task-c", "PENDING"); err != nil {
			t.Errorf("an approval scope leaked across connections: %v", err)
		}
	})

	t.Run("a different sandbox is its own scope", func(t *testing.T) {
		if err := insertMarkedReview("sandbox-x", inspectConnA, "task-d", "PENDING"); err != nil {
			t.Fatalf("first: %v", err)
		}
		if err := insertMarkedReview("sandbox-y", inspectConnA, "task-d", "PENDING"); err != nil {
			t.Errorf("two sandboxes collided on one marker: %v", err)
		}
	})

	// An answered review leaves the partial index, which is the documented
	// behaviour: a rejected or revoked answer must not suppress a later
	// request under the same marker.
	for _, answered := range []string{"APPROVED", "REJECTED", "EXECUTED"} {
		t.Run("an "+answered+" review does not block the next request", func(t *testing.T) {
			marker := "task-" + answered
			if err := insertMarkedReview(inspectOwner, inspectConnA, marker, answered); err != nil {
				t.Fatalf("seed %s: %v", answered, err)
			}
			if err := insertMarkedReview(inspectOwner, inspectConnA, marker, "PENDING"); err != nil {
				t.Errorf("an answered review suppressed a later request: %v", err)
			}
		})
	}

	// Every review created by any other path has a NULL marker, and there are
	// many of them. The index must not constrain those at all.
	t.Run("unmarked reviews are unconstrained", func(t *testing.T) {
		for i := range 3 {
			if err := insertMarkedReview(inspectOwner, inspectConnA, "", "PENDING"); err != nil {
				t.Fatalf("unmarked review %d was rejected: %v", i+1, err)
			}
		}
	})
}

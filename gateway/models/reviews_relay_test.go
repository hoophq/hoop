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
	relayConnA = "00000000-0000-0000-0000-00000000c0a1"
	relayConnB = "00000000-0000-0000-0000-00000000c0b2"
	relayOwner = "sandbox-a"
	relayHash  = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	otherHash    = "0000000000000000000000000000000000000000000000000000000000000000"
)

// seedRelayReview inserts one review the way the create endpoint would, and
// returns its id. minutesAgo backdates created_at so ordering is testable
// without sleeping.
func seedRelayReview(t *testing.T, owner, connID, hash, marker, status string, minutesAgo int) string {
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
func TestClaimRelayReviewIsSingleUse(t *testing.T) {
	startTestDB(t)
	id := seedRelayReview(t, relayOwner, relayConnA, relayHash, "", "APPROVED", 0)

	got, err := models.ClaimRelayReview(models.DB, testOrgID, relayOwner, relayConnA, relayHash)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if got.ID != id {
		t.Errorf("claimed %s, want %s", got.ID, id)
	}
	if s := statusOf(t, id); s != "EXECUTED" {
		t.Errorf("status after claim = %s, want EXECUTED", s)
	}

	_, err = models.ClaimRelayReview(models.DB, testOrgID, relayOwner, relayConnA, relayHash)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("second claim: err = %v, want ErrRecordNotFound — an approval was reused", err)
	}
}

// Every status other than APPROVED denies, and denies without mutating
// anything: a claim must never move a review a human rejected.
func TestClaimRelayReviewOnlyMatchesApproved(t *testing.T) {
	startTestDB(t)

	for _, status := range []string{"PENDING", "REJECTED", "REVOKED", "EXECUTED", "PROCESSING"} {
		hash := strings.Repeat("a", 63) + string(rune('0'+len(status)%10))
		id := seedRelayReview(t, relayOwner, relayConnA, hash, "", status, 0)

		_, err := models.ClaimRelayReview(models.DB, testOrgID, relayOwner, relayConnA, hash)
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
func TestClaimRelayReviewIsScoped(t *testing.T) {
	startTestDB(t)
	seedRelayReview(t, relayOwner, relayConnA, relayHash, "", "APPROVED", 0)

	otherOrg := "00000000-0000-0000-0000-0000000000b2"
	if err := models.DB.Exec(
		`INSERT INTO private.orgs (id, name) VALUES (?, 'other-org')`, otherOrg).Error; err != nil {
		t.Fatalf("seed org: %v", err)
	}

	for name, args := range map[string][4]string{
		"another sandbox":    {testOrgID, "sandbox-b", relayConnA, relayHash},
		"another connection": {testOrgID, relayOwner, relayConnB, relayHash},
		"another org":        {otherOrg, relayOwner, relayConnA, relayHash},
		"another statement":  {testOrgID, relayOwner, relayConnA, otherHash},
	} {
		_, err := models.ClaimRelayReview(models.DB, args[0], args[1], args[2], args[3])
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Errorf("%s claimed an approval that is not its own: err = %v", name, err)
		}
	}

	// The rightful caller still gets it.
	if _, err := models.ClaimRelayReview(models.DB, testOrgID, relayOwner, relayConnA, relayHash); err != nil {
		t.Fatalf("the owning sandbox was refused its own approval: %v", err)
	}
}

// A hash is not unique: the same statement legitimately accumulates many
// reviews, and more than one can be APPROVED at once. Without ORDER BY the
// choice is nondeterministic, which makes "which approval did this consume" an
// unanswerable question in an audit.
func TestClaimRelayReviewTakesTheOldestApproval(t *testing.T) {
	startTestDB(t)
	oldest := seedRelayReview(t, relayOwner, relayConnA, relayHash, "", "APPROVED", 30)
	newest := seedRelayReview(t, relayOwner, relayConnA, relayHash, "", "APPROVED", 5)

	first, err := models.ClaimRelayReview(models.DB, testOrgID, relayOwner, relayConnA, relayHash)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if first.ID != oldest {
		t.Fatalf("claimed %s, want the oldest approval %s", first.ID, oldest)
	}
	second, err := models.ClaimRelayReview(models.DB, testOrgID, relayOwner, relayConnA, relayHash)
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
func TestClaimRelayReviewYieldsOneWinner(t *testing.T) {
	startTestDB(t)
	seedRelayReview(t, relayOwner, relayConnA, relayHash, "", "APPROVED", 0)

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
			got, err := models.ClaimRelayReview(models.DB, testOrgID, relayOwner, relayConnA, relayHash)
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
func TestPendingRelayReviewLookupByMarker(t *testing.T) {
	startTestDB(t)
	pending := seedRelayReview(t, relayOwner, relayConnA, relayHash, "task-42", "PENDING", 0)

	got, err := models.GetPendingRelayReviewByMarker(models.DB, testOrgID, relayOwner, relayConnA, "task-42")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.ID != pending {
		t.Errorf("found %s, want %s", got.ID, pending)
	}

	// A different marker is a different request, even for identical SQL:
	// that is the whole reason the marker exists.
	if _, err := models.GetPendingRelayReviewByMarker(
		models.DB, testOrgID, relayOwner, relayConnA, "task-99"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("a different marker matched: %v", err)
	}

	// An answered review does not suppress a later ask. Each request is
	// answered on its own merits.
	seedRelayReview(t, relayOwner, relayConnA, relayHash, "task-7", "REJECTED", 0)
	if _, err := models.GetPendingRelayReviewByMarker(
		models.DB, testOrgID, relayOwner, relayConnA, "task-7"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("a rejected review was returned as pending: %v", err)
	}
}

// The poll answers "may I retry yet", so an approval outranks a newer review
// in any other state, and polling never consumes anything.
func TestRelayReviewStatusPrefersApproved(t *testing.T) {
	startTestDB(t)
	approved := seedRelayReview(t, relayOwner, relayConnA, relayHash, "", "APPROVED", 30)
	seedRelayReview(t, relayOwner, relayConnA, relayHash, "", "PENDING", 1)

	got, err := models.GetRelayReviewStatus(models.DB, testOrgID, relayOwner, relayConnA, relayHash)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if got.ID != approved || got.Status != models.ReviewStatusApproved {
		t.Errorf("got %s/%s, want the approved review %s", got.ID, got.Status, approved)
	}
	if s := statusOf(t, approved); s != "APPROVED" {
		t.Errorf("polling consumed the approval: status = %s", s)
	}

	if _, err := models.GetRelayReviewStatus(
		models.DB, testOrgID, relayOwner, relayConnA, otherHash); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("an unknown statement returned a review: %v", err)
	}
}

// The session behind a claimed review is closed once and stays closed: a
// re-run must not move the timestamp.
func TestCloseRelaySessionIsIdempotent(t *testing.T) {
	startTestDB(t)
	sid := uuid.NewString()
	err := models.DB.Exec(`
		INSERT INTO private.sessions (id, org_id, connection, connection_type, verb, status)
		VALUES (?, ?, 'appdb', 'database', 'exec', 'open')`, sid, testOrgID).Error
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}

	if err := models.CloseRelaySession(models.DB, testOrgID, sid); err != nil {
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

	if err := models.CloseRelaySession(models.DB, testOrgID, sid); err != nil {
		t.Fatalf("second close: %v", err)
	}
	read()
	if first.EndedAt != was {
		t.Errorf("ended_at moved on a second close: %s -> %s", was, first.EndedAt)
	}
}

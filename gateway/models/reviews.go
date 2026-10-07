package models

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type (
	ReviewStatusType string
	ReviewType       string
)

const (
	ReviewStatusPending    ReviewStatusType = "PENDING"
	ReviewStatusApproved   ReviewStatusType = "APPROVED"
	ReviewStatusRejected   ReviewStatusType = "REJECTED"
	ReviewStatusRevoked    ReviewStatusType = "REVOKED"
	ReviewStatusProcessing ReviewStatusType = "PROCESSING"
	ReviewStatusExecuted   ReviewStatusType = "EXECUTED"
	ReviewStatusUnknown    ReviewStatusType = "UNKNOWN"
	// ReviewStatusExpired is a sidecar review past its deadline. It is terminal.
	// Only expireSidecarReviewsTx writes it, always with statement_hash NULL.
	ReviewStatusExpired ReviewStatusType = "EXPIRED"

	ReviewTypeJit     ReviewType = "jit"
	ReviewTypeOneTime ReviewType = "onetime"
)

// ErrSidecarReviewExpired is a decision on a sidecar review past its deadline.
var ErrSidecarReviewExpired = errors.New("the review expired")

func (t ReviewStatusType) Str() string { return string(t) }

// reviewStatusLabels mirrors private.enum_reviews_status. It exists so a caller
// can tell a real status from arbitrary input before comparing against the enum
// column: casting a non-label to enum_reviews_status is an error, not an empty
// result. Keep it in sync when the enum gains a value — an omission only costs
// the index, never correctness. EXPIRED is left out, so the session filter SQL stays.
var reviewStatusLabels = map[ReviewStatusType]struct{}{
	ReviewStatusPending:    {},
	ReviewStatusApproved:   {},
	ReviewStatusRejected:   {},
	ReviewStatusRevoked:    {},
	ReviewStatusProcessing: {},
	ReviewStatusExecuted:   {},
	ReviewStatusUnknown:    {},
}

// IsValidReviewStatus reports whether v is a label of private.enum_reviews_status.
func IsValidReviewStatus(v string) bool {
	_, ok := reviewStatusLabels[ReviewStatusType(v)]
	return ok
}

type Review struct {
	ID             string           `gorm:"column:id"`
	OrgID          string           `gorm:"column:org_id"`
	SessionID      string           `gorm:"column:session_id"`
	Type           ReviewType       `gorm:"column:type"`
	Status         ReviewStatusType `gorm:"column:status"`
	ConnectionName string           `gorm:"column:connection_name"`
	ConnectionID   sql.NullString   `gorm:"column:connection_id"`

	// SidecarID and ListenerName bind a review to a sidecar's listener, in
	// place of the connection a gateway review points at. SidecarID is nulled
	// if the sidecar is deleted; the listener is what the review is about, so
	// it outlives that.
	SidecarID    sql.NullString `gorm:"column:sidecar_id"`
	ListenerName sql.NullString `gorm:"column:listener_name"`

	// StatementHash is the SHA-256 of the exact statement bytes, in hex. It
	// is how a sidecar's retry of a held statement finds the review already
	// filed for it, so the same bytes are never reviewed twice and an
	// approval releases those bytes and no others. Empty on a review filed
	// from a connection.
	StatementHash sql.NullString `gorm:"column:statement_hash"`

	BlobInputID       sql.NullString    `gorm:"column:blob_input_id"`
	InputEnvVars      map[string]string `gorm:"column:input_env_vars;serializer:json"`
	InputClientArgs   pq.StringArray    `gorm:"column:input_client_args;type:text[]"`
	AccessDurationSec int64             `gorm:"column:access_duration_sec"`
	OwnerID           string            `gorm:"column:owner_id"`
	OwnerEmail        string            `gorm:"column:owner_email"`
	OwnerName         *string           `gorm:"column:owner_name"`
	OwnerSlackID      *string           `gorm:"column:owner_slack_id"`

	ReviewGroups          []ReviewGroups `gorm:"column:review_groups;serializer:json;->"`
	AccessRequestRuleName *string        `gorm:"column:access_request_rule_name"`
	ForceApprovalGroups   pq.StringArray `gorm:"column:force_approval_groups;type:text[]"`
	MinApprovals          *int           `gorm:"column:min_approvals"`

	CreatedAt       time.Time         `gorm:"column:created_at"`
	RevokedAt       *time.Time        `gorm:"column:revoked_at"`
	TimeWindow      *ReviewTimeWindow `gorm:"column:time_window;serializer:json;"`
	RejectionReason *string           `gorm:"column:rejection_reason"`

	// ExpiresAt ends the live status of a sidecar review; nil is no limit. ApprovalTTLSec is copied
	// from the rule at filing. A gateway review sets neither.
	ExpiresAt      *time.Time `gorm:"column:expires_at"`
	ApprovalTTLSec *int       `gorm:"column:approval_ttl_sec"`
}

// SidecarReviewDeadline returns from + ttlSec, or nil for no limit.
func SidecarReviewDeadline(from time.Time, ttlSec *int) *time.Time {
	if ttlSec == nil || *ttlSec <= 0 {
		return nil
	}
	t := from.UTC().Add(time.Duration(*ttlSec) * time.Second)
	return &t
}

func (r *Review) isSidecar() bool {
	return r.ListenerName.Valid && r.ListenerName.String != ""
}

// PastSidecarDeadline reports a live sidecar review whose deadline passed at now.
func (r *Review) PastSidecarDeadline(now time.Time) bool {
	if !r.isSidecar() || r.ExpiresAt == nil {
		return false
	}
	if r.Status != ReviewStatusPending && r.Status != ReviewStatusApproved {
		return false
	}
	return !now.Before(*r.ExpiresAt)
}

// SidecarExpiresAt returns the deadline an API response shows, or nil.
func (r *Review) SidecarExpiresAt() *time.Time {
	if !r.isSidecar() {
		return nil
	}
	switch r.Status {
	case ReviewStatusPending, ReviewStatusApproved, ReviewStatusExpired:
		return r.ExpiresAt
	}
	return nil
}

// SidecarApprovalTTLSec returns the approval limit an API response shows, or nil.
func (r *Review) SidecarApprovalTTLSec() *int {
	if !r.isSidecar() {
		return nil
	}
	return r.ApprovalTTLSec
}

// reportSidecarExpiry shows a lapsed sidecar review as EXPIRED. It writes
// nothing: a claim, a refile or a decision records the expiry.
func (r *Review) reportSidecarExpiry(now time.Time) {
	if r.PastSidecarDeadline(now) {
		r.Status = ReviewStatusExpired
	}
}

type ReviewTimeWindow struct {
	Type          string            `json:"type"`
	Configuration map[string]string `json:"configuration"`
}

type ReviewGroups struct {
	ID            string           `json:"id"`
	OrgID         string           `json:"org_id"`
	ReviewID      string           `json:"review_id"`
	GroupName     string           `json:"group_name"`
	Status        ReviewStatusType `json:"status"`
	OwnerID       *string          `json:"owner_id"`
	OwnerEmail    *string          `json:"owner_email"`
	OwnerName     *string          `json:"owner_name"`
	OwnerSlackID  *string          `json:"owner_slack_id"`
	ReviewedAt    *time.Time       `json:"reviewed_at"`
	ForcedReview  bool             `json:"forced_review"`
	AddedOnDenial bool             `json:"added_on_denial"`
}

// RejectedByEmail returns the email of the reviewer whose group rejected the
// review, or "" when no rejecting group with an email is recorded. It mirrors
// the web UI, which resolves "Rejected by" from the review group whose status
// is REJECTED.
func (r *Review) RejectedByEmail() string {
	if r == nil {
		return ""
	}
	for _, rg := range r.ReviewGroups {
		if rg.Status == ReviewStatusRejected && rg.OwnerEmail != nil {
			return *rg.OwnerEmail
		}
	}
	return ""
}

type ReviewJit struct {
	ID                string     `gorm:"column:id"`
	OrgID             string     `gorm:"column:org_id"`
	SessionID         string     `gorm:"column:session_id"`
	Type              string     `gorm:"column:type"`
	AccessDurationSec int64      `gorm:"column:access_duration_sec"`
	OwnerEmail        string     `gorm:"column:owner_email"`
	CreatedAt         time.Time  `gorm:"column:created_at"`
	RevokedAt         *time.Time `gorm:"column:revoked_at"`
}

// HashStatement computes Review.StatementHash: the SHA-256 of the exact
// statement bytes, in hex, as models.HashAPIKey digests a token.
//
// The exact bytes, never a normalized form. The partial unique index compares
// this value, so anything that folded case, whitespace or literals here would
// widen what a single approval releases.
func HashStatement(statement []byte) string {
	sum := sha256.Sum256(statement)
	return hex.EncodeToString(sum[:])
}

func generateBlobInputID(reviewID string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, fmt.Appendf(nil, "reviewinput:%s", reviewID)).String()
}

// GetBlobInput returns the input if the blob input id is set
func (r *Review) GetBlobInput() (string, error) {
	if !r.BlobInputID.Valid {
		return "", nil
	}
	blobID := generateBlobInputID(r.ID)
	var blob Blob
	err := DB.Table("private.blobs").
		Where("org_id = ? AND id = ?", r.OrgID, blobID).
		First(&blob).
		Error
	if err != nil {
		return "", err
	}

	result := []string{}
	if err := json.Unmarshal(blob.BlobStream, &result); err != nil {
		return "", fmt.Errorf("failed decoding blob input to []string: %v", err)
	}
	if len(result) == 0 {
		return "", nil
	}
	return result[0], nil
}

// ReviewViewer is the user a review is read for.
type ReviewViewer struct {
	UserID           string
	Groups           []string
	IsAuditorOrAdmin bool
}

// reviewVisibilityCondition is the Sessions rule: the requester or a reviewer
// group member. A sidecar review's owner_id is the sidecar, so only its groups count.
const reviewVisibilityCondition = `
	AND (
		(COALESCE(rv.listener_name, '') = '' AND rv.owner_id = ?)
		OR EXISTS (
			SELECT 1 FROM private.review_groups AS vg
			WHERE vg.review_id = rv.id AND NOT vg.added_on_denial
				AND vg.group_name = ANY((?)::text[])
		)
	)`

func (v *ReviewViewer) condition() (string, []any) {
	if v == nil || v.IsAuditorOrAdmin {
		return "", nil
	}
	return reviewVisibilityCondition, []any{v.UserID, pq.StringArray(append([]string{}, v.Groups...))}
}

func GetReviewByIdOrSid(orgID, id string) (*Review, error) {
	return getReviewByIdOrSid(DB, orgID, id, nil)
}

// GetReviewByIdOrSidForViewer returns ErrNotFound for a review the viewer cannot see.
func GetReviewByIdOrSidForViewer(db *gorm.DB, orgID, id string, viewer ReviewViewer) (*Review, error) {
	return getReviewByIdOrSid(db, orgID, id, &viewer)
}

func getReviewByIdOrSid(db *gorm.DB, orgID, id string, viewer *ReviewViewer) (*Review, error) {
	visibility, visibilityArgs := viewer.condition()
	var review Review
	err := db.Raw(`
	SELECT
		id, org_id, session_id, connection_name, connection_id, sidecar_id, listener_name,
		type, access_duration_sec, status,
		blob_input_id, input_env_vars, input_client_args, time_window, access_request_rule_name,
		force_approval_groups, min_approvals, owner_id, owner_email, owner_name, owner_slack_id,
		( SELECT jsonb_agg(
				jsonb_build_object(
					'id', rg.id,
					'org_id', rg.org_id,
					'review_id', rg.review_id,
					'group_name', rg.group_name,
					'status', rg.status,
					'owner_id', rg.owner_id,
					'owner_email', rg.owner_email,
					'owner_name', rg.owner_name,
					'owner_slack_id', rg.owner_slack_id,
					'reviewed_at', to_char(rg.reviewed_at, 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
					'added_on_denial', rg.added_on_denial
				)
			)
			FROM private.review_groups AS rg
			WHERE rg.review_id = rv.id
		) AS review_groups,
	created_at, revoked_at, rejection_reason, expires_at, approval_ttl_sec
	FROM private.reviews rv
	WHERE org_id = ? AND (id = ? OR session_id = ?)`+visibility,
		append([]any{orgID, id, id}, visibilityArgs...)...).
		First(&review).
		Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrNotFound
	}
	if err == nil {
		review.reportSidecarExpiry(time.Now().UTC())
	}
	return &review, err
}

func ListReviews(db *gorm.DB, orgID string, viewer ReviewViewer) (*[]Review, error) {
	visibility, visibilityArgs := viewer.condition()
	var reviews []Review
	err := db.Raw(`
	SELECT
		id, org_id, session_id, connection_name, connection_id, sidecar_id, listener_name,
		type, access_duration_sec, status,
		blob_input_id, input_env_vars, input_client_args, access_request_rule_name,
		force_approval_groups, min_approvals, owner_id, owner_email, owner_name, owner_slack_id,
		( SELECT jsonb_agg(
				jsonb_build_object(
					'id', rg.id,
					'org_id', rg.org_id,
					'review_id', rg.review_id,
					'group_name', rg.group_name,
					'status', rg.status,
					'owner_id', rg.owner_id,
					'owner_email', rg.owner_email,
					'owner_name', rg.owner_name,
					'owner_slack_id', rg.owner_slack_id,
					'reviewed_at', to_char(rg.reviewed_at, 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
					'added_on_denial', rg.added_on_denial
				)
			)
			FROM private.review_groups AS rg
			WHERE rg.review_id = rv.id
		) AS review_groups,
	created_at, revoked_at, rejection_reason, expires_at, approval_ttl_sec
	FROM private.reviews rv
	WHERE org_id = ?`+visibility, append([]any{orgID}, visibilityArgs...)...).
		Find(&reviews).
		Error
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	for i := range reviews {
		reviews[i].reportSidecarExpiry(now)
	}
	return &reviews, nil
}

// CountPendingReviews returns how many reviews of an org are still awaiting a
// decision, and how many of those were created before staleBefore. Callers
// that only need the figures must use this instead of ListReviews, which reads
// every review row of the org along with its review groups. A lapsed sidecar review awaits nothing.
func CountPendingReviews(orgID string, staleBefore time.Time) (pending, stale int, err error) {
	var counts struct {
		Pending int `gorm:"column:pending"`
		Stale   int `gorm:"column:stale"`
	}
	err = DB.Raw(`
	SELECT
		COUNT(*) AS pending,
		COUNT(*) FILTER (WHERE created_at < ?) AS stale
	FROM private.reviews
	WHERE org_id = ? AND status = ?
	AND NOT (listener_name IS NOT NULL AND expires_at IS NOT NULL AND expires_at <= ?)`,
		staleBefore, orgID, ReviewStatusPending, time.Now().UTC()).
		Scan(&counts).
		Error
	if err != nil {
		return 0, 0, err
	}
	return counts.Pending, counts.Stale, nil
}

// Create the review object, when input is not empty it generates a blob id
// and save the input as well.
func CreateReview(rev *Review, input string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		return createReviewTx(tx, rev, input)
	})
}

// createReviewTx is CreateReview's body with the transaction supplied by the
// caller, so a caller that must write a review alongside other rows gets one
// rollback boundary instead of two. The behaviour is otherwise unchanged.
func createReviewTx(tx *gorm.DB, rev *Review, input string) error {
	blobID := generateBlobInputID(rev.ID)
	if input != "" {
		rev.BlobInputID = sql.NullString{String: blobID, Valid: true}
	}
	err := tx.Table("private.reviews").
		Create(rev).
		Error
	if err != nil {
		return err
	}

	if input != "" {
		blobInput := Blob{
			ID:         blobID,
			OrgID:      rev.OrgID,
			Type:       "review-input",
			BlobStream: json.RawMessage(fmt.Sprintf("[%q]", input)),
		}
		err = tx.Table("private.blobs").
			Create(blobInput).
			Error
		if err != nil {
			return fmt.Errorf("failed creating review blob input, reason=%v", err)
		}
	}

	var errs []string
	for _, rg := range rev.ReviewGroups {
		err = tx.Table("private.review_groups").
			Create(map[string]any{
				"id":             rg.ID,
				"org_id":         rg.OrgID,
				"review_id":      rev.ID,
				"group_name":     rg.GroupName,
				"status":         rg.Status,
				"owner_id":       rg.OwnerID,
				"owner_email":    rg.OwnerEmail,
				"owner_slack_id": rg.OwnerSlackID,
				"reviewed_at":    rg.ReviewedAt,
			}).
			Error

		if err != nil {
			errs = append(errs, fmt.Sprintf("%v", err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%v", errs)
	}
	return nil
}

// Lookup for the latest review jit approved
func GetApprovedReviewJit(orgID, ownerUserID, connectionID string) (*ReviewJit, error) {
	var jit ReviewJit
	err := DB.Raw(`
	SELECT id, org_id, session_id, type, access_duration_sec, owner_email, created_at, revoked_at
	FROM private.reviews
	WHERE org_id = ? AND type = 'jit' AND status = 'APPROVED' AND owner_id = ? AND connection_id = ?
	ORDER BY created_at DESC
	LIMIT 1`, orgID, ownerUserID, connectionID).
		First(&jit).
		Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrNotFound
	}
	return &jit, err
}

// update the review resource,
// it updates the session status when the review status is approved, rejected or revoked
func UpdateReview(rev *Review) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Table("private.reviews").
			Where("org_id = ?", rev.OrgID).
			Updates(rev)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("no record updated for review %s", rev.ID)
		}
		return saveReviewDecisionTx(tx, rev)
	})
}

// UpdateSidecarReview writes a decision only while the review holds fromStatus inside its deadline, else
// ErrSidecarReviewExpired or gorm.ErrRecordNotFound (a claim or an expiry won). An approval starts its clock.
func UpdateSidecarReview(db *gorm.DB, rev *Review, fromStatus ReviewStatusType, now time.Time) error {
	now = now.UTC()
	read := Review{Status: fromStatus, ExpiresAt: rev.ExpiresAt, ListenerName: rev.ListenerName}
	if read.PastSidecarDeadline(now) {
		return ErrSidecarReviewExpired
	}
	approvedNow := fromStatus == ReviewStatusPending && rev.Status == ReviewStatusApproved
	return db.Transaction(func(tx *gorm.DB) error {
		if err := lockSidecarReviewTx(tx, rev.OrgID, rev.ID); err != nil {
			return err
		}
		if approvedNow {
			// The lock wait can be longer than the approval limit: count from
			// the later of now and the database clock after the lock.
			from, err := laterOfDatabaseClockTx(tx, now)
			if err != nil {
				return err
			}
			rev.ExpiresAt = SidecarReviewDeadline(from, rev.ApprovalTTLSec)
		}
		res := tx.Table("private.reviews").
			Where("org_id = ? AND id = ? AND status = ? AND listener_name IS NOT NULL AND (expires_at IS NULL OR "+
				sidecarInsideDeadline+")", rev.OrgID, rev.ID, fromStatus, now).
			Updates(rev)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		if approvedNow {
			// Updates(struct) skips a nil pointer, so no approval limit clears it here.
			var deadline any = gorm.Expr("NULL")
			if rev.ExpiresAt != nil {
				deadline = *rev.ExpiresAt
			}
			err := tx.Table("private.reviews").
				Where("org_id = ? AND id = ?", rev.OrgID, rev.ID).
				UpdateColumn("expires_at", deadline).Error
			if err != nil {
				return err
			}
		}
		return saveReviewDecisionTx(tx, rev)
	})
}

// saveReviewDecisionTx writes the review groups and the session status of a
// decision. UpdateReview and UpdateSidecarReview share it.
func saveReviewDecisionTx(tx *gorm.DB, rev *Review) error {
	var errs []string
	for _, rg := range rev.ReviewGroups {
		res := tx.Table("private.review_groups").
			Where("org_id = ? AND review_id = ?", rev.OrgID, rev.ID).
			Save(rg)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			errs = append(errs, fmt.Sprintf("no rows updated for review group, gid=%v, name=%v, status=%v",
				rg.ID, rg.GroupName, rg.Status))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%v", errs)
	}

	var sessionStatus string
	switch rev.Status {
	case ReviewStatusApproved:
		sessionStatus = "ready"
	case ReviewStatusRejected, ReviewStatusRevoked:
		sessionStatus = "done"
	}

	if sessionStatus != "" {
		return tx.Table("private.sessions").
			Where("org_id = ? AND id = ?", rev.OrgID, rev.SessionID).
			UpdateColumn("status", sessionStatus).
			Error
	}
	return nil
}

func UpdateReviewStatus(orgID, id string, status ReviewStatusType) error {
	res := DB.Table("private.reviews").
		Where("org_id = ? AND id = ?", orgID, id).
		UpdateColumn("status", status)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// SetReviewStatusExecutedIfFinished settles the one-time review of the given
// session as EXECUTED once the session has finished. A review stays in
// PROCESSING while the execution runs in the agent (legacy rows may hold
// UNKNOWN); the session is the source of truth for the execution outcome, so
// when it reaches the done status the review is considered consumed. It is a
// no-op (false, nil) when the session has no review, the review is in any
// other status or the session has not finished.
func SetReviewStatusExecutedIfFinished(db *gorm.DB, orgID, sessionID string) (bool, error) {
	res := db.Exec(`
	UPDATE private.reviews AS r
	SET status = ?
	FROM private.sessions AS s
	WHERE s.org_id = r.org_id AND s.id = r.session_id
	AND r.org_id = ? AND r.session_id = ? AND r.type = ?
	AND r.status IN (?, ?) AND s.status = 'done'`,
		ReviewStatusExecuted, orgID, sessionID, ReviewTypeOneTime,
		ReviewStatusProcessing, ReviewStatusUnknown)
	return res.RowsAffected > 0, res.Error
}

// sidecarReviewSelect reads a sidecar review with its groups, so every lookup
// answers with the same policy a fresh review carries. Callers add the WHERE.
const sidecarReviewSelect = `
	SELECT
		id, org_id, session_id, connection_name, connection_id, sidecar_id, listener_name,
		statement_hash, type, access_duration_sec, status,
		blob_input_id, input_env_vars, input_client_args, time_window, access_request_rule_name,
		force_approval_groups, min_approvals, owner_id, owner_email, owner_name, owner_slack_id,
		( SELECT jsonb_agg(
				jsonb_build_object(
					'id', rg.id,
					'org_id', rg.org_id,
					'review_id', rg.review_id,
					'group_name', rg.group_name,
					'status', rg.status,
					'owner_id', rg.owner_id,
					'owner_email', rg.owner_email,
					'owner_name', rg.owner_name,
					'owner_slack_id', rg.owner_slack_id,
					'reviewed_at', to_char(rg.reviewed_at, 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
					'added_on_denial', rg.added_on_denial
				)
			)
			FROM private.review_groups AS rg
			WHERE rg.review_id = rv.id
		) AS review_groups,
	created_at, revoked_at, rejection_reason, expires_at, approval_ttl_sec
	FROM private.reviews rv`

// GetLiveSidecarReview returns the review already filed for these exact
// statement bytes on this listener and rule, or gorm.ErrRecordNotFound.
//
// "Live" excludes EXECUTED, REJECTED and REVOKED, matching the partial unique
// index: a spent approval or a refusal is final for that review, and the next
// request files a new one. Groups load as GetReviewByIdOrSid loads them, so a
// match carries the same policy as a fresh review. A lapsed PENDING or APPROVED holder is not live;
// CreateSidecarReview expires exactly those, so no lapsed holder blocks the insert.
func GetLiveSidecarReview(db *gorm.DB, orgID, sidecarID, listenerName, ruleName, statementHash string, now time.Time) (*Review, error) {
	now = now.UTC()
	var review Review
	err := db.Raw(sidecarReviewSelect+`
	WHERE org_id = ? AND sidecar_id = ? AND listener_name = ?
	AND access_request_rule_name = ? AND statement_hash = ? AND status NOT IN (?, ?, ?, ?)
	AND (status NOT IN (?, ?) OR expires_at IS NULL OR `+sidecarInsideDeadline+`)`,
		orgID, sidecarID, listenerName, ruleName, statementHash,
		ReviewStatusExecuted, ReviewStatusRejected, ReviewStatusRevoked, ReviewStatusExpired,
		ReviewStatusPending, ReviewStatusApproved, now).
		First(&review).
		Error
	if err != nil {
		return nil, err
	}
	return &review, nil
}

// GetSidecarReview returns one review this sidecar filed, in any status, or
// gorm.ErrRecordNotFound.
//
// Unlike GetLiveSidecarReview it returns every status: a sidecar waiting on a
// review must learn that it was spent or refused, not miss the row and file a
// new one. The sidecar scope is the authorization: a token reads only
// its own reviews.
func GetSidecarReview(db *gorm.DB, orgID, sidecarID, reviewID string) (*Review, error) {
	var review Review
	err := db.Raw(sidecarReviewSelect+`
	WHERE org_id = ? AND sidecar_id = ? AND id = ?`,
		orgID, sidecarID, reviewID).
		First(&review).
		Error
	if err != nil {
		return nil, err
	}
	review.reportSidecarExpiry(time.Now().UTC())
	return &review, nil
}

// ListSidecarReviews returns the reviews this sidecar filed, newest first, at
// most limit of them. An empty status lists every status. The status filter
// and the result use the status reportSidecarExpiry shows.
//
// The sidecar scope is the authorization, as in GetSidecarReview.
func ListSidecarReviews(db *gorm.DB, orgID, sidecarID string, status ReviewStatusType, limit int) ([]Review, error) {
	now := time.Now().UTC()
	query := sidecarReviewSelect + `
	WHERE org_id = ? AND sidecar_id = ?`
	args := []any{orgID, sidecarID}
	switch status {
	case "":
	case ReviewStatusPending, ReviewStatusApproved:
		query += ` AND status = ? AND NOT ` + sidecarLapsed
		args = append(args, status, now)
	case ReviewStatusExpired:
		query += ` AND (status = ? OR (status IN (?, ?) AND ` + sidecarLapsed + `))`
		args = append(args, status, ReviewStatusPending, ReviewStatusApproved, now)
	default:
		query += ` AND status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC, id LIMIT ?`
	args = append(args, limit)

	var reviews []Review
	if err := db.Raw(query, args...).Find(&reviews).Error; err != nil {
		return nil, err
	}
	for i := range reviews {
		reviews[i].reportSidecarExpiry(now)
	}
	return reviews, nil
}

// ClaimApprovedSidecarReview consumes an approved review exactly once.
//
// Concurrent retries all read APPROVED, but only one conditional UPDATE matches
// a row, and only its caller may forward. Winner and loser both see EXECUTED
// afterwards, which is why the answer is returned rather than read off the
// status. A claim also closes the session (done + ended_at): nothing else ever
// will, since there is no connection and no agent to report an exit. An
// approval past its deadline is never claimed.
func ClaimApprovedSidecarReview(db *gorm.DB, orgID, reviewID string, now time.Time) (bool, ReviewStatusType, error) {
	now = now.UTC()
	var claimed bool
	var status string
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := lockSidecarReviewTx(tx, orgID, reviewID); err != nil {
			return err
		}
		res := tx.Exec(`
		UPDATE private.reviews
		SET status = ?
		WHERE org_id = ? AND id = ? AND status = ? AND sidecar_id IS NOT NULL
		AND (expires_at IS NULL OR `+sidecarInsideDeadline+`)`,
			ReviewStatusExecuted, orgID, reviewID, ReviewStatusApproved, now)
		if res.Error != nil {
			return res.Error
		}
		claimed = res.RowsAffected > 0

		if claimed {
			err := tx.Exec(`
			UPDATE private.sessions AS s
			SET status = 'done', ended_at = ?
			FROM private.reviews AS r
			WHERE r.org_id = s.org_id AND r.session_id = s.id
			AND r.org_id = ? AND r.id = ?`, now, orgID, reviewID).Error
			if err != nil {
				return err
			}
		}

		return tx.Raw(`SELECT status FROM private.reviews WHERE org_id = ? AND id = ?`,
			orgID, reviewID).Scan(&status).Error
	})
	if err != nil {
		return false, "", err
	}
	return claimed, ReviewStatusType(status), nil
}

// CreateSidecarReview writes the session and the review in one transaction, so
// a crash between them cannot leave a session no review points at.
//
// Returns gorm.ErrDuplicatedKey when a racing request filed for the same bytes
// first. That is the index doing its job, not a fault: answer from the winner.
// display is the reviewer's text (apisidecar displayStatement): raw binary bytes
// fail the JSONB blob write. The approval binds to rev.StatementHash. A lapsed holder of the same key
// is expired first in the transaction, and its ids are returned.
func CreateSidecarReview(db *gorm.DB, sess Session, rev *Review, display string) ([]string, error) {
	// CreatedAt is the expiry clock: a zero one would expire no lapsed holder.
	if rev.CreatedAt.IsZero() {
		return nil, errors.New("a sidecar review needs its creation time")
	}
	var expired []string
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		expired, err = expireSidecarReviewsTx(tx, rev.CreatedAt, expireScopeStatement,
			rev.OrgID, rev.SidecarID, rev.ListenerName, rev.AccessRequestRuleName, rev.StatementHash)
		if err != nil {
			return fmt.Errorf("failed expiring the previous review: %w", err)
		}
		if err := upsertSessionTx(tx, sess); err != nil {
			return fmt.Errorf("failed creating session: %w", err)
		}
		return createReviewTx(tx, rev, display)
	})
	if err != nil {
		return nil, err
	}
	return expired, nil
}

// sidecarInsideDeadline: expires_at is after both the bound time (the one ?) and the
// database clock, read as the UTC wall time the column stores. A write runs it after
// it locks the row, so a call that waited on the lock checks the clock after the wait.
const sidecarInsideDeadline = `(expires_at > ? AND expires_at > (clock_timestamp() AT TIME ZONE 'UTC'))`

// sidecarLapsed matches a review PastSidecarDeadline reports, given its status
// is live. It takes one argument: now.
const sidecarLapsed = `(COALESCE(listener_name, '') <> '' AND expires_at IS NOT NULL AND expires_at <= ?)`

// laterOfDatabaseClockTx returns the later of t and the database clock.
func laterOfDatabaseClockTx(tx *gorm.DB, t time.Time) (time.Time, error) {
	var dbNow time.Time
	if err := tx.Raw(`SELECT clock_timestamp() AT TIME ZONE 'UTC'`).Scan(&dbNow).Error; err != nil {
		return t, err
	}
	if dbNow = dbNow.UTC(); dbNow.After(t) {
		return dbNow, nil
	}
	return t, nil
}

// lockSidecarReviewTx locks one review before a deadline check reads the clock.
func lockSidecarReviewTx(tx *gorm.DB, orgID, reviewID string) error {
	return tx.Exec(`SELECT 1 FROM private.reviews WHERE org_id = ? AND id = ? FOR UPDATE`, orgID, reviewID).Error
}

// The scopes expireSidecarReviewsTx accepts. No input reaches the SQL text.
const (
	expireScopeReview    = `org_id = ? AND id = ?`
	expireScopeStatement = `org_id = ? AND sidecar_id = ? AND listener_name = ? AND access_request_rule_name = ? AND statement_hash = ?`
)

// expireSidecarReviewsTx records EXPIRED on the lapsed live sidecar reviews in
// scope and closes their sessions. Clearing the hash frees the statement.
func expireSidecarReviewsTx(tx *gorm.DB, now time.Time, scope string, args ...any) ([]string, error) {
	if scope != expireScopeReview && scope != expireScopeStatement {
		return nil, fmt.Errorf("unknown expiry scope")
	}
	now = now.UTC()
	err := tx.Exec(`SELECT 1 FROM private.reviews WHERE listener_name IS NOT NULL AND status IN (?, ?) AND `+
		scope+` FOR UPDATE`, append([]any{ReviewStatusPending, ReviewStatusApproved}, args...)...).Error
	if err != nil {
		return nil, err
	}
	var ids []string
	err = tx.Raw(`
	UPDATE private.reviews SET status = ?, statement_hash = NULL
	WHERE listener_name IS NOT NULL AND status IN (?, ?)
	AND expires_at IS NOT NULL AND NOT (`+sidecarInsideDeadline+`) AND `+scope+`
	RETURNING id`,
		append([]any{ReviewStatusExpired, ReviewStatusPending, ReviewStatusApproved, now}, args...)...).
		Scan(&ids).Error
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	// The close a rejection does. ended_at stays NULL: nothing ran.
	err = tx.Exec(`
	UPDATE private.sessions AS s SET status = 'done'
	FROM private.reviews AS r
	WHERE r.org_id = s.org_id AND r.session_id = s.id
	AND r.id IN ? AND s.status <> 'done'`, ids).Error
	if err != nil {
		return nil, err
	}
	return ids, nil
}

// ExpireSidecarReview records EXPIRED on one lapsed sidecar review and returns
// its status after. expired is true only for the call that moved the row.
func ExpireSidecarReview(db *gorm.DB, orgID, reviewID string, now time.Time) (bool, ReviewStatusType, error) {
	var expired bool
	var status ReviewStatusType
	err := db.Transaction(func(tx *gorm.DB) error {
		ids, err := expireSidecarReviewsTx(tx, now, expireScopeReview, orgID, reviewID)
		if err != nil {
			return err
		}
		expired = len(ids) > 0
		var row struct{ Status ReviewStatusType }
		err = tx.Table("private.reviews").Select("status").
			Where("org_id = ? AND id = ?", orgID, reviewID).Take(&row).Error
		status = row.Status
		return err
	})
	if err != nil {
		return false, "", err
	}
	return expired, status, nil
}

// ReconcileStaleReviews settles as EXECUTED every one-time review left in
// PROCESSING or UNKNOWN whose session already finished. These reviews are
// normally settled when the session closes, but a gateway restart in between
// leaves them stale; this runs once at startup. It returns the number of
// reviews updated.
func ReconcileStaleReviews(db *gorm.DB) (int64, error) {
	res := db.Exec(`
	UPDATE private.reviews AS r
	SET status = ?
	FROM private.sessions AS s
	WHERE s.org_id = r.org_id AND s.id = r.session_id
	AND r.type = ? AND r.status IN (?, ?) AND s.status = 'done'`,
		ReviewStatusExecuted, ReviewTypeOneTime,
		ReviewStatusProcessing, ReviewStatusUnknown)
	return res.RowsAffected, res.Error
}

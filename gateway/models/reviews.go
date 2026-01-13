package models

import (
	"database/sql"
	"encoding/json"
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

	ReviewTypeJit     ReviewType = "jit"
	ReviewTypeOneTime ReviewType = "onetime"
)

func (t ReviewStatusType) Str() string { return string(t) }

type Review struct {
	ID                string            `gorm:"column:id"`
	OrgID             string            `gorm:"column:org_id"`
	SessionID         string            `gorm:"column:session_id"`
	Type              ReviewType        `gorm:"column:type"`
	Status            ReviewStatusType  `gorm:"column:status"`
	ConnectionName    string            `gorm:"column:connection_name"`
	ConnectionID      sql.NullString    `gorm:"column:connection_id"`
	BlobInputID       sql.NullString    `gorm:"column:blob_input_id"`
	InputEnvVars      map[string]string `gorm:"column:input_env_vars;serializer:json"`
	InputClientArgs   pq.StringArray    `gorm:"column:input_client_args;type:text[]"`
	AccessDurationSec int64             `gorm:"column:access_duration_sec"`
	OwnerID           string            `gorm:"column:owner_id"`
	OwnerEmail        string            `gorm:"column:owner_email"`
	OwnerName         *string           `gorm:"column:owner_name"`
	OwnerSlackID      *string           `gorm:"column:owner_slack_id"`
	ReviewGroups      []ReviewGroups    `gorm:"column:review_groups;serializer:json;->"`
	CreatedAt         time.Time         `gorm:"column:created_at"`
	RevokedAt         *time.Time        `gorm:"column:revoked_at"`
	TimeWindow        *ReviewTimeWindow `gorm:"column:time_window;serializer:json;"`
}

type ReviewTimeWindow struct {
	Type          string            `json:"type"`
	Configuration map[string]string `json:"configuration"`
}

type ReviewGroups struct {
	ID           string           `json:"id"`
	OrgID        string           `json:"org_id"`
	ReviewID     string           `json:"review_id"`
	GroupName    string           `json:"group_name"`
	Status       ReviewStatusType `json:"status"`
	OwnerID      *string          `json:"owner_id"`
	OwnerEmail   *string          `json:"owner_email"`
	OwnerName    *string          `json:"owner_name"`
	OwnerSlackID *string          `json:"owner_slack_id"`
	ReviewedAt   *time.Time       `json:"reviewed_at"`
	ForcedReview bool             `json:"forced_review"`
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

func GetReviewByIdOrSid(orgID, id string) (*Review, error) {
	var review Review
	err := DB.Raw(`
	SELECT
		id, org_id, session_id, connection_name, type, access_duration_sec, status,
		blob_input_id, input_env_vars, input_client_args, time_window,
		owner_id, owner_email, owner_name, owner_slack_id,
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
					'reviewed_at', to_char(rg.reviewed_at, 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')
				)
			)
			FROM private.review_groups AS rg
			WHERE rg.review_id = rv.id
		) AS review_groups,
	created_at, revoked_at
	FROM private.reviews rv
	WHERE org_id = ? AND (id = ? OR session_id = ?)`, orgID, id, id).
		First(&review).
		Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrNotFound
	}
	return &review, err
}

func ListReviews(orgID string) (*[]Review, error) {
	var reviews []Review
	err := DB.Raw(`
	SELECT
		id, org_id, session_id, connection_name, type, access_duration_sec, status,
		blob_input_id, input_env_vars, input_client_args,
		owner_id, owner_email, owner_name, owner_slack_id,
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
					'reviewed_at', to_char(rg.reviewed_at, 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')
				)
			)
			FROM private.review_groups AS rg
			WHERE rg.review_id = rv.id
		) AS review_groups,
	created_at, revoked_at
	FROM private.reviews rv
	WHERE org_id = ?`, orgID).
		Find(&reviews).
		Error
	if err != nil {
		return nil, err
	}

	return &reviews, nil
}

// Create the review object, when input is not empty it generates a blob id
// and save the input as well.
func CreateReview(rev *Review, input string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
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
	})
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
		var errs []string
		for _, rg := range rev.ReviewGroups {
			res = tx.Table("private.review_groups").
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
	})
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

package pgreview

import (
	"fmt"
	"time"

	"github.com/runopsio/hoop/gateway/pgrest"
)

type ReviewGroup struct {
	ID           string  `json:"id"`
	OrgID        string  `json:"org_id"`
	ReviewID     string  `json:"review_id"`
	GroupName    string  `json:"group_name"`
	Status       string  `json:"status"`
	OwnerUserID  *string `json:"owner_id"`
	OwnerEmail   *string `json:"owner_email"`
	OwnerName    *string `json:"owner_name"`
	OwnerSlackID *string `json:"owner_slack_id"`
	ReviewedAt   *string `json:"reviewed_at"`
}

type Review struct {
	ID                string            `json:"id"`
	OrgID             string            `json:"org_id"`
	SessionID         *string           `json:"session_id"`
	ConnectionID      *string           `json:"connection_id"`
	ConnectionName    string            `json:"connection_name"`
	Type              string            `json:"type"`
	BlobInputID       *string           `json:"blob_input_id"`
	InputEnvVars      map[string]string `json:"input_env_vars"`
	InputClientArgs   []string          `json:"input_client_args"`
	AccessDurationSec int               `json:"access_duration_sec"`
	Status            string            `json:"status"`
	OwnerUserID       string            `json:"owner_id"`
	OwnerEmail        string            `json:"owner_email"`
	OwnerName         *string           `json:"owner_name"`
	OwnerSlackID      *string           `json:"owner_slack_id"`
	CreatedAt         string            `json:"created_at"`
	RevokedAt         *string           `json:"revoked_at"`

	BlobInput    *pgrest.Blob  `json:"blob_input"`
	ReviewGroups []ReviewGroup `json:"review_groups"`
}

// func (r *Review) GetSessionID() string {
// 	if r.SessionID != nil {
// 		return *r.SessionID
// 	}
// 	return ""
// }

func (r *Review) GetCreatedAt() time.Time {
	createdAt, _ := time.ParseInLocation("2006-01-02T15:04:05", r.CreatedAt, time.UTC)
	return createdAt
}

func (r *Review) GetRevokedAt() *time.Time {
	if r.RevokedAt != nil {
		revokedAt, _ := time.ParseInLocation("2006-01-02T15:04:05", *r.RevokedAt, time.UTC)
		return &revokedAt
	}
	return nil
}

func (r *Review) GetBlobInput() (v string) {
	if r.BlobInput != nil {
		if len(r.BlobInput.BlobStream) > 0 {
			v, _ = r.BlobInput.BlobStream[0].(string)
		}
	}
	return
}

func (r *Review) GetAccessDuration() time.Duration {
	d, _ := time.ParseDuration(fmt.Sprintf("%vs", r.AccessDurationSec))
	return d
}

func toString(v *string) string {
	if v != nil {
		return *v
	}
	return ""
}

func toStringPtr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

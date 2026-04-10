package models

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type AgentIdentitySecret struct {
	ID              string     `gorm:"column:id"`
	AgentIdentityID string     `gorm:"column:agent_identity_id"`
	KeyPrefix       string     `gorm:"column:key_prefix"`
	HashedSecret    string     `gorm:"column:hashed_secret"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
	ExpiresAt       *time.Time `gorm:"column:expires_at"`
}

func CreateAgentIdentitySecret(s *AgentIdentitySecret) error {
	return DB.Model(&AgentIdentitySecret{}).Create(s).Error
}

func ListAgentIdentitySecrets(orgID, agentIdentityID string) ([]AgentIdentitySecret, error) {
	var items []AgentIdentitySecret
	// verify the agent identity belongs to the org before listing its secrets
	err := DB.Raw(`
	SELECT s.id, s.agent_identity_id, s.key_prefix, s.hashed_secret, s.created_at, s.expires_at
	FROM private.agent_identity_secrets s
	JOIN private.agent_identities a ON a.id = s.agent_identity_id
	WHERE a.org_id = ? AND s.agent_identity_id = ?`, orgID, agentIdentityID).
		Find(&items).
		Error
	return items, err
}

func DeleteAgentIdentitySecret(orgID, id, agentIdentityID string) error {
	res := DB.Exec(`
	DELETE FROM private.agent_identity_secrets s
	USING private.agent_identities a
	WHERE s.agent_identity_id = a.id
	  AND a.org_id = ?
	  AND s.id = ?
	  AND s.agent_identity_id = ?`, orgID, id, agentIdentityID)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// GetAgentIdentityContext looks up an agent identity by its raw token and returns a Context
// suitable for use in the auth middleware. It narrows candidates by key_prefix before
// doing the more expensive bcrypt comparison.
func GetAgentIdentityContext(rawToken string) (*Context, error) {
	if len(rawToken) < 8 {
		return nil, fmt.Errorf("token too short")
	}
	keyPrefix := rawToken[:8]

	type secretCandidate struct {
		ID              string     `gorm:"column:id"`
		AgentIdentityID string     `gorm:"column:agent_identity_id"`
		HashedSecret    string     `gorm:"column:hashed_secret"`
		ExpiresAt       *time.Time `gorm:"column:expires_at"`
	}

	var candidates []secretCandidate
	err := DB.Raw(`
	SELECT id, agent_identity_id, hashed_secret, expires_at
	FROM private.agent_identity_secrets
	WHERE key_prefix = ?`, keyPrefix).
		Find(&candidates).
		Error
	if err != nil {
		return nil, err
	}

	var matchedAgentID string
	for _, c := range candidates {
		if err := bcrypt.CompareHashAndPassword([]byte(c.HashedSecret), []byte(rawToken)); err == nil {
			if c.ExpiresAt != nil && time.Now().After(*c.ExpiresAt) {
				return nil, fmt.Errorf("token expired")
			}
			matchedAgentID = c.AgentIdentityID
			break
		}
	}
	if matchedAgentID == "" {
		return nil, nil
	}

	type agentContextRow struct {
		OrgID          string          `gorm:"column:org_id"`
		OrgName        string          `gorm:"column:org_name"`
		OrgLicenseData json.RawMessage `gorm:"column:org_license_data"`
		UserID         string          `gorm:"column:user_id"`
		UserSubject    string          `gorm:"column:user_subject"`
		UserName       string          `gorm:"column:user_name"`
		UserStatus     string          `gorm:"column:user_status"`
		UserGroups     pq.StringArray  `gorm:"column:user_groups;type:text[]"`
	}

	var row agentContextRow
	err = DB.Raw(`
	SELECT
		o.id AS org_id,
		o.name AS org_name,
		o.license_data AS org_license_data,
		a.id AS user_id,
		a.subject AS user_subject,
		a.name AS user_name,
		a.status AS user_status,
		COALESCE((
			SELECT array_agg(ug.name::TEXT) FROM private.user_groups ug
			WHERE ug.service_account_id = a.id
		), ARRAY[]::TEXT[]) AS user_groups
	FROM private.agent_identities a
	JOIN private.orgs o ON o.id = a.org_id
	WHERE a.id = ?`, matchedAgentID).
		Scan(&row).
		Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}
	if row.UserID == "" {
		return nil, nil
	}

	return &Context{
		OrgID:          row.OrgID,
		OrgName:        row.OrgName,
		OrgLicenseData: row.OrgLicenseData,
		UserID:         row.UserID,
		UserSubject:    row.UserSubject,
		UserEmail:      row.UserSubject,
		UserName:       row.UserName,
		UserStatus:     row.UserStatus,
		UserGroups:     row.UserGroups,
	}, nil
}

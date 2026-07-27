package models

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type AIAgent struct {
	ID            string         `gorm:"column:id;type:uuid;default:gen_random_uuid();primaryKey"`
	OrgID         string         `gorm:"column:org_id"`
	Name          string         `gorm:"column:name"`
	KeyHash       string         `gorm:"column:key_hash"`
	MaskedKey     string         `gorm:"column:masked_key"`
	Status        string         `gorm:"column:status"`
	Groups        pq.StringArray `gorm:"column:groups;type:text[];->"`
	CreatedBy     string         `gorm:"column:created_by"`
	DeactivatedBy *string        `gorm:"column:deactivated_by"`
	CreatedAt     time.Time      `gorm:"column:created_at"`
	DeactivatedAt *time.Time     `gorm:"column:deactivated_at"`
	LastUsedAt    *time.Time     `gorm:"column:last_used_at"`
}

func GenerateAIAgent() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("failed to generate random key: " + err.Error())
	}
	return "hpk_" + base64.RawURLEncoding.EncodeToString(b)
}

func HashAIAgent(rawKey string) string {
	h := sha256.Sum256([]byte(rawKey))
	return fmt.Sprintf("%x", h)
}

func MaskAIAgent(rawKey string) string {
	if len(rawKey) <= 8 {
		return strings.Repeat("*", len(rawKey))
	}
	return rawKey[:8] + strings.Repeat("*", len(rawKey)-8)
}

func ListAIAgents(orgID string) ([]AIAgent, error) {
	var items []AIAgent
	err := DB.Raw(`
	SELECT ak.id, ak.org_id, ak.name, ak.masked_key, ak.status,
	ak.created_by, ak.deactivated_by, ak.created_at, ak.deactivated_at, ak.last_used_at,
	COALESCE((
		SELECT array_agg(ug.name::TEXT) FROM private.user_groups ug
		WHERE ug.ai_agent_id = ak.id
	), ARRAY[]::TEXT[]) AS groups
	FROM private.ai_agents ak
	WHERE ak.org_id = ?`, orgID).
		Find(&items).
		Error
	if err != nil {
		return nil, err
	}
	return items, nil
}

func GetAIAgentByNameOrID(orgID, nameOrID string) (*AIAgent, error) {
	var item AIAgent
	identifierClause := "ak.name = ?"
	if _, err := uuid.Parse(nameOrID); err == nil {
		identifierClause = "ak.id = ?"
	}

	err := DB.Raw(`
	SELECT ak.id, ak.org_id, ak.name, ak.masked_key, ak.status,
	ak.created_by, ak.deactivated_by, ak.created_at, ak.deactivated_at, ak.last_used_at,
	COALESCE((
		SELECT array_agg(ug.name::TEXT) FROM private.user_groups ug
		WHERE ug.ai_agent_id = ak.id
	), ARRAY[]::TEXT[]) AS groups
	FROM private.ai_agents ak
	WHERE ak.org_id = ? AND `+identifierClause, orgID, nameOrID).
		Scan(&item).
		Error
	if err != nil {
		return nil, err
	}
	if item.ID == "" {
		return nil, nil
	}
	return &item, nil
}

func CreateAIAgent(aiAgent *AIAgent) error {
	if aiAgent.ID == "" {
		aiAgent.ID = uuid.NewString()
	}
	aiAgent.CreatedAt = time.Now().UTC()
	return DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Table("private.ai_agents").Create(map[string]any{
			"id":         aiAgent.ID,
			"org_id":     aiAgent.OrgID,
			"name":       aiAgent.Name,
			"key_hash":   aiAgent.KeyHash,
			"masked_key": aiAgent.MaskedKey,
			"status":     aiAgent.Status,
			"created_by": aiAgent.CreatedBy,
			"created_at": aiAgent.CreatedAt,
		}).Error
		if err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return ErrAlreadyExists
			}
			return err
		}
		for _, group := range aiAgent.Groups {
			err = tx.Exec(`
			INSERT INTO private.user_groups (org_id, ai_agent_id, name)
			VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, aiAgent.OrgID, aiAgent.ID, group).
				Error
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func UpdateAIAgent(aiAgent *AIAgent) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Table("private.ai_agents").
			Where("id = ? AND org_id = ?", aiAgent.ID, aiAgent.OrgID).
			Updates(map[string]any{
				"name": aiAgent.Name,
			})
		if res.Error != nil {
			if errors.Is(res.Error, gorm.ErrDuplicatedKey) {
				return ErrAlreadyExists
			}
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		err := tx.Exec(`
			DELETE FROM private.user_groups
			WHERE org_id = ? AND ai_agent_id = ?`, aiAgent.OrgID, aiAgent.ID).
			Error
		if err != nil {
			return fmt.Errorf("failed to delete ai agent groups: %v", err)
		}
		for _, group := range aiAgent.Groups {
			err = tx.Exec(`
			INSERT INTO private.user_groups (org_id, ai_agent_id, name)
			VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, aiAgent.OrgID, aiAgent.ID, group).
				Error
			if err != nil {
				return fmt.Errorf("failed to insert ai agent group: %v", err)
			}
		}
		return nil
	})
}

// GetAIAgentContext looks up an active AI agent by its hash and returns
// a fully populated models.Context for use in auth middleware.
func GetAIAgentContext(keyHash string) (*Context, error) {
	var ctx struct {
		OrgID          string          `gorm:"column:org_id"`
		OrgName        string          `gorm:"column:org_name"`
		OrgLicenseData json.RawMessage `gorm:"column:org_license_data"`
		AIAgentID      string          `gorm:"column:ai_agent_id"`
		AIAgentName    string          `gorm:"column:ai_agent_name"`
		Groups         pq.StringArray  `gorm:"column:groups;type:text[]"`
	}
	err := DB.Raw(`
	SELECT ak.id AS ai_agent_id, ak.name AS ai_agent_name,
		o.id AS org_id, o.name AS org_name, o.license_data AS org_license_data,
		COALESCE((
			SELECT array_agg(ug.name::TEXT) FROM private.user_groups ug
			WHERE ug.ai_agent_id = ak.id
		), ARRAY[]::TEXT[]) AS groups
	FROM private.ai_agents ak
	JOIN private.orgs o ON ak.org_id = o.id
	WHERE ak.key_hash = ? AND ak.status = 'active'`, keyHash).
		Scan(&ctx).
		Error
	if err != nil {
		return nil, err
	}
	if ctx.AIAgentID == "" {
		return nil, nil
	}
	return &Context{
		OrgID:          ctx.OrgID,
		OrgName:        ctx.OrgName,
		OrgLicenseData: ctx.OrgLicenseData,
		UserID:         ctx.AIAgentID,
		UserSubject:    ctx.AIAgentID,
		UserName:       ctx.AIAgentName,
		UserEmail:      ctx.AIAgentName,
		UserStatus:     "active",
		UserGroups:     ctx.Groups,
	}, nil
}

// UpdateAIAgentLastUsed sets the last_used_at timestamp for an AI agent.
func UpdateAIAgentLastUsed(id string) {
	DB.Table("private.ai_agents").
		Where("id = ?", id).
		Update("last_used_at", time.Now().UTC())
}

func RevokeAIAgent(orgID, id, deactivatedBy string) error {
	res := DB.Table("private.ai_agents").
		Where("id = ? AND org_id = ? AND status = 'active'", id, orgID).
		Updates(map[string]any{
			"status":         "revoked",
			"deactivated_by": deactivatedBy,
			"deactivated_at": time.Now(),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func ReactivateAIAgent(orgID, id string) error {
	res := DB.Table("private.ai_agents").
		Where("id = ? AND org_id = ? AND status = 'revoked'", id, orgID).
		Updates(map[string]any{
			"status":         "active",
			"deactivated_by": nil,
			"deactivated_at": nil,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

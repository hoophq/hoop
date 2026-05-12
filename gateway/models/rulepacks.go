package models

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type Rulepack struct {
	ID          uuid.UUID      `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	OrgID       uuid.UUID      `gorm:"column:org_id;index:idx_rulepacks_org_display_name,unique"`
	DisplayName string         `gorm:"column:display_name;index:idx_rulepacks_org_display_name,unique"`
	Description *string        `gorm:"column:description"`
	Version     *string        `gorm:"column:version"`
	Tags        pq.StringArray `gorm:"column:tags;type:text[]"`
	IsManaged   bool           `gorm:"column:is_managed"`
	CreatedAt   time.Time      `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt   time.Time      `gorm:"column:updated_at;autoUpdateTime"`
}

func (Rulepack) TableName() string { return "private.rulepacks" }

type RulepackFilterOption struct {
	Search   string
	Page     int
	PageSize int
}

func GetRulepack(db *gorm.DB, orgID, id uuid.UUID) (*Rulepack, error) {
	var rp Rulepack
	err := db.Where("org_id = ? AND id = ?", orgID, id).First(&rp).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &rp, nil
}

func ListRulepacks(db *gorm.DB, orgID uuid.UUID, opts RulepackFilterOption) ([]*Rulepack, int64, error) {
	var total int64
	query := db.Model(&Rulepack{}).Where("org_id = ?", orgID)

	if opts.Search != "" {
		query = query.Where("display_name ILIKE ?", "%"+opts.Search+"%")
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	query = query.Order("display_name ASC")

	if opts.PageSize > 0 {
		offset := 0
		if opts.Page > 1 {
			offset = (opts.Page - 1) * opts.PageSize
		}
		query = query.Limit(opts.PageSize).Offset(offset)
	}

	var rps []*Rulepack
	if err := query.Find(&rps).Error; err != nil {
		return nil, 0, err
	}
	return rps, total, nil
}

func CreateRulepack(db *gorm.DB, rp *Rulepack) error {
	err := db.Create(rp).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrAlreadyExists
	}
	return err
}

func UpdateRulepack(db *gorm.DB, rp *Rulepack) error {
	result := db.Model(&Rulepack{}).
		Where("org_id = ? AND id = ?", rp.OrgID, rp.ID).
		Updates(map[string]any{
			"display_name": rp.DisplayName,
			"description":  rp.Description,
			"version":      rp.Version,
			"tags":         rp.Tags,
			"updated_at":   time.Now(),
		})
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrDuplicatedKey) {
			return ErrAlreadyExists
		}
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func DeleteRulepack(db *gorm.DB, orgID, id uuid.UUID) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var ruleNamesToDelete struct {
			DataMasking         []string
			Guardrail           []string
			AccessRequest       []string
			AccessControlGroups []string
		}

		var rulepackAttribute string
		if err := tx.Table("private.attributes").
			Where("org_id = ? AND rulepack_id = ?", orgID, id).
			Select("name").
			First(&rulepackAttribute).Error; err != nil {
			return err
		}

		// Datamasking
		if err := tx.Table("private.datamasking_rules_attributes ja").
			Where("ja.org_id = ? AND ja.attribute_name = ?", orgID, rulepackAttribute).
			Distinct("ja.datamasking_rule_name").
			Pluck("ja.datamasking_rule_name", &ruleNamesToDelete.DataMasking).Error; err != nil {
			return err
		}
		if len(ruleNamesToDelete.DataMasking) > 0 {
			if err := tx.Exec(
				"DELETE FROM private.datamasking_rules WHERE org_id = ? AND name IN ?",
				orgID, ruleNamesToDelete.DataMasking).Error; err != nil {
				return err
			}
		}

		// Guardrail
		if err := tx.Table("private.guardrail_rules_attributes ja").
			Where("ja.org_id = ? AND ja.attribute_name = ?", orgID, rulepackAttribute).
			Distinct("ja.guardrail_rule_name").
			Pluck("ja.guardrail_rule_name", &ruleNamesToDelete.Guardrail).Error; err != nil {
			return err
		}
		if len(ruleNamesToDelete.Guardrail) > 0 {
			if err := tx.Exec(
				"DELETE FROM private.guardrail_rules WHERE org_id = ? AND name IN ?",
				orgID, ruleNamesToDelete.Guardrail).Error; err != nil {
				return err
			}
		}

		// Access Request
		if err := tx.Table("private.access_request_rules_attributes ja").
			Where("ja.org_id = ? AND ja.attribute_name = ?", orgID, rulepackAttribute).
			Distinct("ja.access_rule_name").
			Pluck("ja.access_rule_name", &ruleNamesToDelete.AccessRequest).Error; err != nil {
			return err
		}
		if len(ruleNamesToDelete.AccessRequest) > 0 {
			if err := tx.Exec(
				"DELETE FROM private.access_request_rules WHERE org_id = ? AND name IN ?",
				orgID, ruleNamesToDelete.AccessRequest).Error; err != nil {
				return err
			}
		}

		// Access Control Groups
		if err := tx.Table("private.access_control_groups_attributes ja").
			Where("ja.org_id = ? AND ja.attribute_name = ?", orgID, rulepackAttribute).
			Distinct("ja.group_name").
			Pluck("ja.group_name", &ruleNamesToDelete.AccessControlGroups).Error; err != nil {
			return err
		}
		if len(ruleNamesToDelete.AccessControlGroups) > 0 {
			if err := tx.Exec(
				"DELETE FROM private.user_groups WHERE org_id = ? AND name IN ?",
				orgID, ruleNamesToDelete.AccessControlGroups).Error; err != nil {
				return err
			}
		}

		result := tx.Where("org_id = ? AND id = ?", orgID, id).Delete(&Rulepack{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	})
}

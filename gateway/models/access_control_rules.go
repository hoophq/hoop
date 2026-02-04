package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type AccessControlRules struct {
	ID    uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	OrgID uuid.UUID `gorm:"column:org_id;uniqueIndex:idx_access_control_rules_org_name"`

	Name        string  `gorm:"column:name;uniqueIndex:idx_access_control_rules_org_name"`
	Description *string `gorm:"column:description"`

	ReviewersGroups    pq.StringArray `gorm:"column:reviewers_groups;type:text[]"`
	ForceApproveGroups pq.StringArray `gorm:"column:force_approve_groups;type:text[]"`
	AccessMaxDuration  *int           `gorm:"column:access_max_duration"`
	MinApprovals       *int           `gorm:"column:min_approvals"`

	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

func (m AccessControlRules) TableName() string {
	return "private.access_control_rules"
}

func GetAccessControlRulesByName(db *gorm.DB, name string, orgID uuid.UUID) (*AccessControlRules, error) {
	var accessControlRules AccessControlRules
	result := db.Where("name = ? AND org_id = ?", name, orgID).First(&accessControlRules)
	if result.Error != nil {
		return nil, result.Error
	}

	return &accessControlRules, nil
}

func CreateAccessControlRules(db *gorm.DB, accessControlRules *AccessControlRules) error {
	result := db.Create(accessControlRules)
	return result.Error
}

func UpdateAccessControlRules(db *gorm.DB, accessControlRules *AccessControlRules) error {
	result := db.Save(accessControlRules)
	return result.Error
}

type AccessControlRulesFilterOption struct {
	Page     int
	PageSize int
}

func ListAccessControlRules(db *gorm.DB, orgID uuid.UUID, opts AccessControlRulesFilterOption) ([]AccessControlRules, int64, error) {
	var accessControlRules []AccessControlRules
	var total int64

	// Get total count
	if err := db.Model(&AccessControlRules{}).Where("org_id = ?", orgID).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Build query with pagination
	query := db.Where("org_id = ?", orgID).Order("created_at DESC")

	// Apply pagination if specified
	if opts.PageSize > 0 {
		offset := 0
		if opts.Page > 1 {
			offset = (opts.Page - 1) * opts.PageSize
		}
		query = query.Limit(opts.PageSize).Offset(offset)
	}

	result := query.Find(&accessControlRules)
	if result.Error != nil {
		return nil, 0, result.Error
	}

	return accessControlRules, total, nil
}

func DeleteAccessControlRulesByName(db *gorm.DB, name string, orgID uuid.UUID) error {
	result := db.Where("name = ? AND org_id = ?", name, orgID).Delete(&AccessControlRules{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

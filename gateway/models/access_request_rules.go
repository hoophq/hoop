package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type AccessRequestRules struct {
	ID    uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	OrgID uuid.UUID `gorm:"column:org_id;uniqueIndex:idx_access_request_rules_org_name" json:"org_id"`

	Name        string  `gorm:"column:name;uniqueIndex:idx_access_request_rules_org_name" json:"name"`
	Description *string `gorm:"column:description" json:"description"`

	ReviewersGroups     pq.StringArray `gorm:"column:reviewers_groups;type:text[]" json:"reviewers_groups"`
	ForceApprovalGroups pq.StringArray `gorm:"column:force_approval_groups;type:text[]" json:"force_approval_groups"`
	AccessMaxDuration   *int           `gorm:"column:access_max_duration" json:"access_max_duration"`
	MinApprovals        *int           `gorm:"column:min_approvals" json:"min_approvals"`

	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (m AccessRequestRules) TableName() string {
	return "private.access_request_rules"
}

func GetAccessRequestRulesByName(db *gorm.DB, name string, orgID uuid.UUID) (*AccessRequestRules, error) {
	var accessRequestRules AccessRequestRules
	result := db.Where("name = ? AND org_id = ?", name, orgID).First(&accessRequestRules)
	if result.Error != nil {
		return nil, result.Error
	}

	return &accessRequestRules, nil
}

func CreateAccessRequestRules(db *gorm.DB, accessRequestRules *AccessRequestRules) error {
	result := db.Create(accessRequestRules)
	return result.Error
}

func UpdateAccessRequestRules(db *gorm.DB, accessRequestRules *AccessRequestRules) error {
	result := db.Save(accessRequestRules)
	return result.Error
}

type AccessRequestRulesFilterOption struct {
	Page     int
	PageSize int
}

func ListAccessRequestRules(db *gorm.DB, orgID uuid.UUID, opts AccessRequestRulesFilterOption) ([]AccessRequestRules, int64, error) {
	var accessRequestRules []AccessRequestRules
	var total int64

	// Get total count
	if err := db.Model(&AccessRequestRules{}).Where("org_id = ?", orgID).Count(&total).Error; err != nil {
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

	result := query.Find(&accessRequestRules)
	if result.Error != nil {
		return nil, 0, result.Error
	}

	return accessRequestRules, total, nil
}

func DeleteAccessRequestRulesByName(db *gorm.DB, name string, orgID uuid.UUID) error {
	result := db.Where("name = ? AND org_id = ?", name, orgID).Delete(&AccessRequestRules{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

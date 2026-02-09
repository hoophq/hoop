package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type AccessRequestRule struct {
	ID    uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	OrgID uuid.UUID `gorm:"column:org_id;index:idx_access_request_rules_org_name,unique"`

	Name        string  `gorm:"column:name;index:idx_access_request_rules_org_name,unique"`
	Description *string `gorm:"column:description"`
	AccessType  string  `gorm:"column:access_type"`

	ConnectionNames        pq.StringArray `gorm:"column:connection_names;type:text[]"`
	ApprovalRequiredGroups pq.StringArray `gorm:"column:approval_required_groups;type:text[]"`
	AllGroupsMustApprove   bool           `gorm:"column:all_groups_must_approve;default:false"`
	ReviewersGroups        pq.StringArray `gorm:"column:reviewers_groups;type:text[]"`
	ForceApprovalGroups    pq.StringArray `gorm:"column:force_approval_groups;type:text[]"`

	AccessMaxDuration *int `gorm:"column:access_max_duration"`
	MinApprovals      *int `gorm:"column:min_approvals"`

	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

func (m AccessRequestRule) TableName() string {
	return "private.access_request_rules"
}

func GetAccessRequestRuleByResourceNameAndAccessType(db *gorm.DB, orgID uuid.UUID, resourceName, accessType string) (*AccessRequestRule, error) {
	var accessRequestRule AccessRequestRule
	result := db.
		Where("org_id = ? AND connection_names @> ? AND access_type = ?", orgID, pq.Array([]string{resourceName}), accessType).
		First(&accessRequestRule)
	if result.Error != nil {
		return nil, result.Error
	}

	return &accessRequestRule, nil
}

func GetAccessRequestRuleByResourceNamesAndAccessType(db *gorm.DB, orgID uuid.UUID, resourceName []string, accessType string) (*AccessRequestRule, error) {
	var accessRequestRule AccessRequestRule
	result := db.
		Where("org_id = ? AND connection_names && ? AND access_type = ?", orgID, pq.StringArray(resourceName), accessType).
		First(&accessRequestRule)
	if result.Error != nil {
		return nil, result.Error
	}

	return &accessRequestRule, nil
}

func GetAccessRequestRuleByName(db *gorm.DB, name string, orgID uuid.UUID) (*AccessRequestRule, error) {
	var accessRequestRule AccessRequestRule
	result := db.Where("name = ? AND org_id = ?", name, orgID).First(&accessRequestRule)
	if result.Error != nil {
		return nil, result.Error
	}

	return &accessRequestRule, nil
}

func CreateAccessRequestRule(db *gorm.DB, accessRequestRules *AccessRequestRule) error {
	result := db.Create(accessRequestRules)
	return result.Error
}

func UpdateAccessRequestRule(db *gorm.DB, accessRequestRules *AccessRequestRule) error {
	result := db.Save(accessRequestRules)
	return result.Error
}

type AccessRequestRulesFilterOption struct {
	Page     int
	PageSize int
}

func ListAccessRequestRules(db *gorm.DB, orgID uuid.UUID, opts AccessRequestRulesFilterOption) ([]AccessRequestRule, int64, error) {
	var accessRequestRules []AccessRequestRule
	var total int64

	// Get total count
	if err := db.Model(&AccessRequestRule{}).Where("org_id = ?", orgID).Count(&total).Error; err != nil {
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

func GetConnectionAccessRequestRules(db *gorm.DB, orgID uuid.UUID, connectionName string) ([]AccessRequestRule, error) {
	var accessRequestRules []AccessRequestRule
	result := db.
		Where("org_id = ? AND connection_names @> ?", orgID, pq.Array([]string{connectionName})).
		Find(&accessRequestRules)
	if result.Error != nil {
		return nil, result.Error
	}

	return accessRequestRules, nil
}

func DeleteAccessRequestRuleByName(db *gorm.DB, name string, orgID uuid.UUID) error {
	result := db.Where("name = ? AND org_id = ?", name, orgID).Delete(&AccessRequestRule{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

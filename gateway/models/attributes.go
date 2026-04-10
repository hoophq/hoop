package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Attribute struct {
	ID          uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	OrgID       uuid.UUID `gorm:"column:org_id;index:idx_attributes_org_name,unique"`
	Name        string    `gorm:"column:name;index:idx_attributes_org_name,unique"`
	Description *string   `gorm:"column:description"`
	CreatedAt   time.Time `gorm:"column:created_at;autoCreateTime"`

	Connections         []ConnectionAttribute         `gorm:"foreignKey:OrgID,AttributeName;references:OrgID,Name"`
	AccessRequestRules  []AccessRequestRuleAttribute  `gorm:"foreignKey:OrgID,AttributeName;references:OrgID,Name"`
	GuardrailRules      []GuardrailRuleAttribute      `gorm:"foreignKey:OrgID,AttributeName;references:OrgID,Name"`
	DatamaskingRules    []DatamaskingRuleAttribute    `gorm:"foreignKey:OrgID,AttributeName;references:OrgID,Name"`
	AccessControlGroups []AccessControlGroupAttribute `gorm:"foreignKey:OrgID,AttributeName;references:OrgID,Name"`
}

func (Attribute) TableName() string {
	return "private.attributes"
}

// Junction tables
// Connection and Attribute
type ConnectionAttribute struct {
	OrgID          uuid.UUID `gorm:"column:org_id;primaryKey"`
	AttributeName  string    `gorm:"column:attribute_name;primaryKey"`
	ConnectionName string    `gorm:"column:connection_name;primaryKey"`
}

func (ConnectionAttribute) TableName() string { return "private.connections_attributes" }

// Access Request Rule and Attribute
type AccessRequestRuleAttribute struct {
	OrgID          uuid.UUID `gorm:"column:org_id;primaryKey"`
	AttributeName  string    `gorm:"column:attribute_name;primaryKey"`
	AccessRuleName string    `gorm:"column:access_rule_name;primaryKey"`
}

func (AccessRequestRuleAttribute) TableName() string {
	return "private.access_request_rules_attributes"
}

// Guardrail Rule and Attribute
type GuardrailRuleAttribute struct {
	OrgID             uuid.UUID `gorm:"column:org_id;primaryKey"`
	AttributeName     string    `gorm:"column:attribute_name;primaryKey"`
	GuardrailRuleName string    `gorm:"column:guardrail_rule_name;primaryKey"`
}

func (GuardrailRuleAttribute) TableName() string {
	return "private.guardrail_rules_attributes"
}

// Data Masking Rule and Attribute
type DatamaskingRuleAttribute struct {
	OrgID               uuid.UUID `gorm:"column:org_id;primaryKey"`
	AttributeName       string    `gorm:"column:attribute_name;primaryKey"`
	DatamaskingRuleName string    `gorm:"column:datamasking_rule_name;primaryKey"`
}

func (DatamaskingRuleAttribute) TableName() string {
	return "private.datamasking_rules_attributes"
}

// Access Control Group and Attribute
type AccessControlGroupAttribute struct {
	OrgID         uuid.UUID `gorm:"column:org_id;primaryKey"`
	AttributeName string    `gorm:"column:attribute_name;primaryKey"`
	GroupName     string    `gorm:"column:group_name;primaryKey"`
}

func (AccessControlGroupAttribute) TableName() string {
	return "private.access_control_groups_attributes"
}

func GetAttribute(db *gorm.DB, orgID uuid.UUID, name string) (*Attribute, error) {
	var attr Attribute
	err := db.
		Preload("Connections").
		Preload("AccessRequestRules").
		Preload("GuardrailRules").
		Preload("DatamaskingRules").
		Preload("AccessControlGroups").
		Where("org_id = ? AND name = ?", orgID, name).
		First(&attr).Error
	if err != nil {
		return nil, err
	}

	return &attr, nil
}

func UpsertAttribute(db *gorm.DB, attr *Attribute) error {
	return db.Transaction(func(tx *gorm.DB) error {
		err := tx.Clauses(clause.Returning{}).Omit("CreatedAt").Save(attr).Error
		if err != nil {
			return err
		}

		if attr.Connections != nil {
			if err := tx.Where("org_id = ? AND attribute_name = ?", attr.OrgID, attr.Name).
				Delete(&ConnectionAttribute{}).Error; err != nil {
				return err
			}
			if len(attr.Connections) > 0 {
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&attr.Connections).Error; err != nil {
					return err
				}
			}
		}

		if attr.AccessRequestRules != nil {
			if err := tx.Where("org_id = ? AND attribute_name = ?", attr.OrgID, attr.Name).
				Delete(&AccessRequestRuleAttribute{}).Error; err != nil {
				return err
			}
			if len(attr.AccessRequestRules) > 0 {
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&attr.AccessRequestRules).Error; err != nil {
					return err
				}
			}
		}

		if attr.GuardrailRules != nil {
			if err := tx.Where("org_id = ? AND attribute_name = ?", attr.OrgID, attr.Name).
				Delete(&GuardrailRuleAttribute{}).Error; err != nil {
				return err
			}
			if len(attr.GuardrailRules) > 0 {
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&attr.GuardrailRules).Error; err != nil {
					return err
				}
			}
		}

		if attr.DatamaskingRules != nil {
			if err := tx.Where("org_id = ? AND attribute_name = ?", attr.OrgID, attr.Name).
				Delete(&DatamaskingRuleAttribute{}).Error; err != nil {
				return err
			}
			if len(attr.DatamaskingRules) > 0 {
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&attr.DatamaskingRules).Error; err != nil {
					return err
				}
			}
		}

		if attr.AccessControlGroups != nil {
			if err := tx.Where("org_id = ? AND attribute_name = ?", attr.OrgID, attr.Name).
				Delete(&AccessControlGroupAttribute{}).Error; err != nil {
				return err
			}
			if len(attr.AccessControlGroups) > 0 {
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&attr.AccessControlGroups).Error; err != nil {
					return err
				}
			}
		}

		return nil
	})
}

type AttributeFilterOption struct {
	Search   string
	Page     int
	PageSize int
}

func ListAttributes(db *gorm.DB, orgID uuid.UUID, opts AttributeFilterOption) ([]*Attribute, int64, error) {
	var total int64
	query := db.Model(&Attribute{}).Where("org_id = ?", orgID)

	if opts.Search != "" {
		query = query.Where("name ILIKE ?", "%"+opts.Search+"%")
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	query = query.Order("name ASC")

	if opts.PageSize > 0 {
		offset := 0
		if opts.Page > 1 {
			offset = (opts.Page - 1) * opts.PageSize
		}
		query = query.Limit(opts.PageSize).Offset(offset)
	}

	var attrs []*Attribute
	if err := query.
		Preload("Connections").
		Preload("AccessRequestRules").
		Preload("GuardrailRules").
		Preload("DatamaskingRules").
		Preload("AccessControlGroups").
		Find(&attrs).Error; err != nil {
		return nil, 0, err
	}
	return attrs, total, nil
}

func DeleteAttribute(db *gorm.DB, orgID uuid.UUID, name string) error {
	result := db.Where("org_id = ? AND name = ?", orgID, name).
		Delete(&Attribute{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func GetConnectionRequestRulesByConnectionAttributes(db *gorm.DB, orgID uuid.UUID, connectionName string, accessType string) (*AccessRequestRule, error) {
	var accessRequestRule AccessRequestRule

	attributeNamesSubQuery := db.Model(&ConnectionAttribute{}).
		Distinct("attribute_name").
		Where("org_id = ? AND connection_name = ?", orgID, connectionName)

	ruleNamesSubQuery := db.Model(&AccessRequestRuleAttribute{}).
		Distinct("access_rule_name").
		Where("org_id = ? AND attribute_name IN (?)", orgID, attributeNamesSubQuery)

	result := db.Model(&AccessRequestRule{}).
		Where("org_id = ? AND access_type = ?", orgID, accessType).
		Where("name IN (?)", ruleNamesSubQuery).
		First(&accessRequestRule)
	if result.Error != nil {
		return nil, result.Error
	}

	return &accessRequestRule, nil
}

func GetConnectionAttributes(db *gorm.DB, orgID uuid.UUID, connectionName string) ([]string, error) {
	var attributeNames []string
	result := db.Model(&ConnectionAttribute{}).
		Select("attribute_name").
		Where("org_id = ? AND connection_name = ?", orgID, connectionName).
		Find(&attributeNames)
	if result.Error != nil {
		return nil, result.Error
	}
	return attributeNames, nil
}

// UpsertConnectionAttributes replaces all attribute associations for the given connection.
// If an attribute name does not exist in the attributes table, it is created automatically.
func UpsertConnectionAttributes(db *gorm.DB, orgID uuid.UUID, connectionName string, attributeNames []string) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("org_id = ? AND connection_name = ?", orgID, connectionName).
			Delete(&ConnectionAttribute{}).Error; err != nil {
			return err
		}
		if len(attributeNames) == 0 {
			return nil
		}

		attributes := make([]Attribute, len(attributeNames))
		for i, name := range attributeNames {
			attributes[i] = Attribute{OrgID: orgID, Name: name}
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&attributes).Error; err != nil {
			return err
		}

		assocs := make([]ConnectionAttribute, len(attributeNames))
		for i, name := range attributeNames {
			assocs[i] = ConnectionAttribute{OrgID: orgID, AttributeName: name, ConnectionName: connectionName}
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&assocs).Error
	})
}

// UpsertDatamaskingRuleAttributes replaces all attribute associations for the given datamasking rule.
// If an attribute name does not exist in the attributes table, it is created automatically.
func UpsertDatamaskingRuleAttributes(db *gorm.DB, orgID uuid.UUID, ruleName string, attributeNames []string) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("org_id = ? AND datamasking_rule_name = ?", orgID, ruleName).
			Delete(&DatamaskingRuleAttribute{}).Error; err != nil {
			return err
		}
		if len(attributeNames) == 0 {
			return nil
		}

		attributes := make([]Attribute, len(attributeNames))
		for i, name := range attributeNames {
			attributes[i] = Attribute{OrgID: orgID, Name: name}
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&attributes).Error; err != nil {
			return err
		}

		assocs := make([]DatamaskingRuleAttribute, len(attributeNames))
		for i, name := range attributeNames {
			assocs[i] = DatamaskingRuleAttribute{OrgID: orgID, AttributeName: name, DatamaskingRuleName: ruleName}
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&assocs).Error
	})
}

// UpsertAccessRequestRuleAttributes replaces all attribute associations for the given access request rule.
// If an attribute name does not exist in the attributes table, it is created automatically.
func UpsertAccessRequestRuleAttributes(db *gorm.DB, orgID uuid.UUID, ruleName string, attributeNames []string) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("org_id = ? AND access_rule_name = ?", orgID, ruleName).
			Delete(&AccessRequestRuleAttribute{}).Error; err != nil {
			return err
		}
		if len(attributeNames) == 0 {
			return nil
		}

		attributes := make([]Attribute, len(attributeNames))
		for i, name := range attributeNames {
			attributes[i] = Attribute{OrgID: orgID, Name: name}
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&attributes).Error; err != nil {
			return err
		}

		assocs := make([]AccessRequestRuleAttribute, len(attributeNames))
		for i, name := range attributeNames {
			assocs[i] = AccessRequestRuleAttribute{OrgID: orgID, AttributeName: name, AccessRuleName: ruleName}
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&assocs).Error
	})
}

// UpsertGuardrailRuleAttributes replaces all attribute associations for the given guardrail rule.
// If an attribute name does not exist in the attributes table, it is created automatically.
func UpsertGuardrailRuleAttributes(db *gorm.DB, orgID uuid.UUID, ruleName string, attributeNames []string) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("org_id = ? AND guardrail_rule_name = ?", orgID, ruleName).
			Delete(&GuardrailRuleAttribute{}).Error; err != nil {
			return err
		}
		if len(attributeNames) == 0 {
			return nil
		}

		attributes := make([]Attribute, len(attributeNames))
		for i, name := range attributeNames {
			attributes[i] = Attribute{OrgID: orgID, Name: name}
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&attributes).Error; err != nil {
			return err
		}

		assocs := make([]GuardrailRuleAttribute, len(attributeNames))
		for i, name := range attributeNames {
			assocs[i] = GuardrailRuleAttribute{OrgID: orgID, AttributeName: name, GuardrailRuleName: ruleName}
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&assocs).Error
	})
}

// UpsertAccessControlGroupAttributes replaces all attribute associations for the given access control group.
// If an attribute name does not exist in the attributes table, it is created automatically.
func UpsertAccessControlGroupAttributes(db *gorm.DB, orgID uuid.UUID, groupName string, attributeNames []string) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("org_id = ? AND group_name = ?", orgID, groupName).
			Delete(&AccessControlGroupAttribute{}).Error; err != nil {
			return err
		}
		if len(attributeNames) == 0 {
			return nil
		}

		attributes := make([]Attribute, len(attributeNames))
		for i, name := range attributeNames {
			attributes[i] = Attribute{OrgID: orgID, Name: name}
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&attributes).Error; err != nil {
			return err
		}

		assocs := make([]AccessControlGroupAttribute, len(attributeNames))
		for i, name := range attributeNames {
			assocs[i] = AccessControlGroupAttribute{OrgID: orgID, AttributeName: name, GroupName: groupName}
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&assocs).Error
	})
}

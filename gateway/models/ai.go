package models

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AIProviderFeature string

const (
	AISessionAnalyzerFeature AIProviderFeature = "session-analyzer"
)

type AIProvider struct {
	ID    uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	OrgID uuid.UUID `gorm:"column:org_id;index:idx_ai_session_analyzer_rules_org_feature"`

	Feature  string `gorm:"column:feature;index:idx_ai_session_analyzer_rules_org_feature"`
	Provider string `gorm:"column:provider"`

	ApiUrl *string `gorm:"column:api_url"`
	ApiKey *string `gorm:"column:api_key"`
	Model  string  `gorm:"column:model"`

	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

func (AIProvider) TableName() string {
	return "private.ai_providers"
}

type RiskEvaluationAction string

const (
	AllowExecution RiskEvaluationAction = "allow_execution"
	BlockExecution RiskEvaluationAction = "block_execution"
)

type AISessionAnalyzerRiskEvaluation struct {
	LowRiskAction    RiskEvaluationAction `json:"low_risk_action"`
	MediumRiskAction RiskEvaluationAction `json:"medium_risk_action"`
	HighRiskAction   RiskEvaluationAction `json:"high_risk_action"`
}

type AISessionAnalyzerRules struct {
	ID    uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	OrgID uuid.UUID `gorm:"column:org_id;index:idx_ai_session_analyzer_rules_org_name,unique"`

	Name            string                          `gorm:"column:name;index:idx_ai_session_analyzer_rules_org_name,unique"`
	Description     *string                         `gorm:"column:description"`
	ConnectionNames pq.StringArray                  `gorm:"column:connection_names;type:text[]"`
	RiskEvaluation  AISessionAnalyzerRiskEvaluation `gorm:"column:risk_evaluation;type:jsonb;serializer:json"`

	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

func (AISessionAnalyzerRules) TableName() string {
	return "private.ai_session_analyzer_rules"
}

func GetAIProvider(orgID uuid.UUID, feature AIProviderFeature) (*AIProvider, error) {
	var p AIProvider
	err := DB.Where("org_id = ? AND feature = ?", orgID, string(feature)).First(&p).Error
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func UpsertAIProvider(orgID uuid.UUID, feature AIProviderFeature, provider string, apiURL *string, apiKey *string, model string) (*AIProvider, error) {
	p := AIProvider{
		OrgID:    orgID,
		Feature:  string(feature),
		Provider: provider,
		ApiUrl:   apiURL,
		ApiKey:   apiKey,
		Model:    model,
	}
	err := DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "org_id"}, {Name: "feature"}},
		DoUpdates: clause.AssignmentColumns([]string{"provider", "api_url", "api_key", "model", "updated_at"}),
	}, clause.Returning{}).Create(&p).Error
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func DeleteAIProvider(orgID uuid.UUID, feature AIProviderFeature) error {
	result := DB.Where("org_id = ? AND feature = ?", orgID, string(feature)).Delete(&AIProvider{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func ListAISessionAnalyzerRules(orgID uuid.UUID, connectionNames []string, page, pageSize int) ([]*AISessionAnalyzerRules, int64, error) {
	var rules []*AISessionAnalyzerRules
	var total int64

	query := DB.Where("org_id = ?", orgID)
	if len(connectionNames) > 0 {
		query = query.Where("connection_names && ?", pq.StringArray(connectionNames))
	}

	// Get total count
	if err := query.Model(&AISessionAnalyzerRules{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Apply pagination
	if pageSize > 0 {
		offset := 0
		if page > 1 {
			offset = (page - 1) * pageSize
		}
		query = query.Limit(pageSize).Offset(offset)
	}

	err := query.Order("name ASC").Find(&rules).Error
	return rules, total, err
}

func GetAISessionAnalyzerRule(orgID uuid.UUID, name string) (*AISessionAnalyzerRules, error) {
	var rule AISessionAnalyzerRules
	err := DB.Where("org_id = ? AND name = ?", orgID, name).First(&rule).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, gorm.ErrRecordNotFound
		}
		return nil, err
	}
	return &rule, nil
}

func GetAISessionAnalyzerRuleByConnection(db *gorm.DB, orgID uuid.UUID, connectionName string) (*AISessionAnalyzerRules, error) {
	var rule AISessionAnalyzerRules
	result := db.
		Where("org_id = ? AND connection_names @> ?", orgID, pq.StringArray{connectionName}).
		First(&rule)
	if result.Error != nil {
		return nil, result.Error
	}

	return &rule, nil
}

func CreateAISessionAnalyzerRule(rule *AISessionAnalyzerRules) error {
	err := DB.Create(rule).Error
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return ErrAlreadyExists
		}
		return err
	}
	return nil
}

func UpdateAISessionAnalyzerRule(rule *AISessionAnalyzerRules) error {
	result := DB.Model(rule).
		Clauses(clause.Returning{}).
		Where("org_id = ? AND name = ?", rule.OrgID, rule.Name).
		Updates(map[string]any{
			"description":      rule.Description,
			"connection_names": rule.ConnectionNames,
			"risk_evaluation":  rule.RiskEvaluation,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func DeleteAISessionAnalyzerRule(orgID uuid.UUID, name string) error {
	result := DB.Where("org_id = ? AND name = ?", orgID, name).Delete(&AISessionAnalyzerRules{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func GetAIAnalyzerRulesByConnections(db *gorm.DB, orgID uuid.UUID, connectionNames []string) (*AISessionAnalyzerRules, error) {
	var rule AISessionAnalyzerRules
	result := db.
		Where("org_id = ? AND connection_names && ?", orgID, pq.StringArray(connectionNames)).
		First(&rule)
	if result.Error != nil {
		return nil, result.Error
	}

	return &rule, nil
}

package models

import "gorm.io/gorm"

type Audit struct {
	OrgID     string         `gorm:"column:org_id"`
	Event     string         `gorm:"column:event"`
	Metadata  map[string]any `gorm:"column:metadata;serializer:json"`
	CreatedBy string         `gorm:"column:created_by"`
}

const (
	FeatureAskAiEnabled  string = "feature-ask-ai-enabled"
	FeatureAskAiDisabled string = "feature-ask-ai-disabled"
)

func CreateAudit(orgID, event, createdBy string, metadata map[string]any) error {
	return DB.Table("private.audit").
		Create(&Audit{
			OrgID:     orgID,
			Event:     event,
			Metadata:  metadata,
			CreatedBy: createdBy,
		}).
		Error
}

func IsFeatureAskAiEnabled(orgID string) (bool, error) {
	audit := Audit{}
	err := DB.Table("private.audit").
		Where("org_id = ? AND event IN (?, ?)", orgID, FeatureAskAiEnabled, FeatureAskAiDisabled).
		Order("created_at DESC").
		Limit(1).
		Find(&audit).
		Error
	if err == gorm.ErrRecordNotFound {
		return false, nil
	}
	return audit.Event == FeatureAskAiEnabled, err
}

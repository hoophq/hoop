package models

import (
	"time"

	"github.com/google/uuid"
)

const tableSecurityAuditLog = "private.security_audit_log"

// SecurityAuditLog is the persisted security audit event (who, when, what, details, outcome).
type SecurityAuditLog struct {
	ID                      uuid.UUID      `gorm:"column:id;type:uuid;primaryKey"`
	OrgID                   string         `gorm:"column:org_id;type:uuid;not null"`
	ActorSubject            string         `gorm:"column:actor_subject;size:255;not null"`
	ActorEmail              string         `gorm:"column:actor_email;size:255"`
	ActorName               string         `gorm:"column:actor_name;size:255"`
	CreatedAt               time.Time      `gorm:"column:created_at;type:timestamptz;not null"`
	ResourceType            string         `gorm:"column:resource_type;size:64;not null"`
	Action                  string         `gorm:"column:action;size:32;not null"`
	ResourceID              *uuid.UUID     `gorm:"column:resource_id;type:uuid"`
	ResourceName            string         `gorm:"column:resource_name;size:255"`
	RequestPayloadRedacted  map[string]any `gorm:"column:request_payload_redacted;serializer:json"`
	Outcome                 bool           `gorm:"column:outcome;not null"` // true = success, false = failure
	ErrorMessage            string         `gorm:"column:error_message;type:text"`
}

// TableName overrides the table name.
func (SecurityAuditLog) TableName() string {
	return tableSecurityAuditLog
}

// CreateSecurityAuditLog inserts one audit event. ResourceID can be nil when the resource has a non-UUID id.
func CreateSecurityAuditLog(row *SecurityAuditLog) error {
	if row.ID == uuid.Nil {
		row.ID = uuid.New()
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	return DB.Table(tableSecurityAuditLog).Create(row).Error
}

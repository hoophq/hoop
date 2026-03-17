package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const tableSecurityAuditLog = "private.security_audit_log"

// SecurityAuditLog is the persisted security audit event (who, when, what, details, outcome).
type SecurityAuditLog struct {
	ID                      uuid.UUID      `gorm:"column:id;type:uuid;primaryKey;default:uuid_generate_v4()"`
	OrgID                   string         `gorm:"column:org_id;type:uuid;not null"`
	ActorSubject            string         `gorm:"column:actor_subject;size:255;not null"`
	ActorEmail              string         `gorm:"column:actor_email;size:255"`
	ActorName               string         `gorm:"column:actor_name;size:255"`
	CreatedAt               time.Time      `gorm:"column:created_at;type:timestamptz;not null;default:now()"`
	ResourceType            string         `gorm:"column:resource_type;size:64;not null"`
	Action                  string         `gorm:"column:action;size:32;not null"`
	HttpMethod              string         `gorm:"column:http_method;size:16;not null"`
	HttpStatus              int            `gorm:"column:http_status;not null"`
	HttpPath                string         `gorm:"column:http_path;size:512;not null"`
	ClientIP                string         `gorm:"column:client_ip;size:45;not null"`
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
	return DB.Table(tableSecurityAuditLog).Create(row).Error
}

// SecurityAuditLogFilter holds optional filters and pagination for listing audit logs.
type SecurityAuditLogFilter struct {
	Page          int
	PageSize      int
	ActorSubject  string // exact or partial (LIKE)
	ActorEmail    string
	ResourceType  string
	Action        string
	HttpMethod    string // GET, POST, PUT, DELETE, PATCH
	HttpStatus    int    // 0 = all, >0 = specific status code
	HttpPath      string // partial match
	ClientIP      string // exact or partial match
	Outcome       *bool  // true = success only, false = failure only, nil = all
	CreatedAfter  string // RFC3339 or date
	CreatedBefore string
}

// ListSecurityAuditLogs returns audit logs for the org with filters and pagination. Order: created_at DESC.
func ListSecurityAuditLogs(db *gorm.DB, orgID string, f SecurityAuditLogFilter) ([]SecurityAuditLog, int64, error) {
	if f.PageSize <= 0 {
		f.PageSize = 50
	}
	if f.PageSize > 100 {
		f.PageSize = 100
	}
	if f.Page < 1 {
		f.Page = 1
	}
	offset := (f.Page - 1) * f.PageSize

	q := db.Table(tableSecurityAuditLog).Where("org_id = ?", orgID)

	if f.ActorSubject != "" {
		q = q.Where("actor_subject ILIKE ?", "%"+f.ActorSubject+"%")
	}
	if f.ActorEmail != "" {
		q = q.Where("actor_email ILIKE ?", "%"+f.ActorEmail+"%")
	}
	if f.ResourceType != "" {
		q = q.Where("resource_type = ?", f.ResourceType)
	}
	if f.Action != "" {
		q = q.Where("action = ?", f.Action)
	}
	if f.HttpMethod != "" {
		q = q.Where("http_method = ?", f.HttpMethod)
	}
	if f.HttpStatus > 0 {
		q = q.Where("http_status = ?", f.HttpStatus)
	}
	if f.HttpPath != "" {
		q = q.Where("http_path ILIKE ?", "%"+f.HttpPath+"%")
	}
	if f.ClientIP != "" {
		q = q.Where("client_ip ILIKE ?", "%"+f.ClientIP+"%")
	}
	if f.Outcome != nil {
		q = q.Where("outcome = ?", *f.Outcome)
	}
	if f.CreatedAfter != "" {
		q = q.Where("created_at >= ?", f.CreatedAfter)
	}
	if f.CreatedBefore != "" {
		q = q.Where("created_at <= ?", f.CreatedBefore)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var rows []SecurityAuditLog
	err := q.Order("created_at DESC").Limit(f.PageSize).Offset(offset).Find(&rows).Error
	return rows, total, err
}

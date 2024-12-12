package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

const tableSessions string = "private.sessions"

type BlobInputType string

func (b *BlobInputType) Scan(value any) error {
	bytes, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("failed to cast blob input to []byte, got=%T", value)
	}
	result := []string{}
	err := json.Unmarshal(bytes, &result)
	if err != nil {
		return fmt.Errorf("failed decoding blob input to []string: %v", err)
	}
	if len(result) == 0 {
		return nil
	}
	*b = BlobInputType(result[0])
	return nil
}

// const tableSessions = "private.sessions"

type Session struct {
	ID                   string            `gorm:"column:id"`
	OrgID                string            `gorm:"column:org_id"`
	Connection           string            `gorm:"column:connection"`
	ConnectionType       string            `gorm:"column:connection_type"`
	Verb                 string            `gorm:"column:verb"`
	Labels               map[string]string `gorm:"column:labels;serializer:json"`
	Metadata             map[string]any    `gorm:"column:metadata;serializer:json"`
	IntegrationsMetadata map[string]any    `gorm:"column:integrations_metadata;serializer:json"`
	Metrics              map[string]any    `gorm:"column:metrics;serializer:json"`
	BlobInput            BlobInputType     `gorm:"column:blob_input"`
	UserID               string            `gorm:"column:user_id"`
	UserName             string            `gorm:"column:user_name"`
	UserEmail            string            `gorm:"column:user_email"`
	Status               string            `gorm:"column:status"`

	CreatedAt  time.Time  `gorm:"column:created_at"`
	EndSession *time.Time `gorm:"column:ended_at"`
}

func GetSessionByID(orgID, sid string) (*Session, error) {
	var session Session
	err := DB.Model(&Session{}).Raw(`
	SELECT
		s.id, s.org_id, s.connection, s.connection_type, s.verb, s.labels, s.metadata,
		s.user_id, s.user_name, s.user_email, s.status, s.integrations_metadata,
		bi.blob_stream AS blob_input,
		s.created_at, s.ended_at
	FROM private.sessions s
	LEFT JOIN private.blobs AS bi ON bi.type = 'session-input' AND  bi.id = s.blob_input_id
	WHERE s.org_id = ? AND s.id = ?
	`, orgID, sid).First(&session).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &session, nil
}

func UpdateSessionIntegrationMetadata(orgID, sid string, metadata map[string]any) error {
	res := DB.Table(tableSessions).
		Where("org_id = ? AND id = ?", orgID, sid).
		Updates(Session{IntegrationsMetadata: metadata})
	if res.Error == nil && res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}

func GetSessionJiraIssueByID(orgID, sid string) (string, error) {
	var jiraIssueKey string
	err := DB.Raw(`
	SELECT integrations_metadata->>'jira.issue_key' FROM private.sessions s
	WHERE s.org_id = ? AND s.id = ?`, orgID, sid).
		First(&jiraIssueKey).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", ErrNotFound
		}
		return "", err
	}
	return jiraIssueKey, nil
}

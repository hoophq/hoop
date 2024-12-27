package models

import (
	"database/sql"
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

type SessionOption struct {
	User           string
	ConnectionType string
	ConnectionName string
	StartDate      sql.NullString
	EndDate        sql.NullString
	Offset         int
	Limit          int
}

func NewSessionOption() SessionOption {
	return SessionOption{
		User:           "%",
		ConnectionType: "%",
		ConnectionName: "%",
		Limit:          20,
		Offset:         0,
	}
}

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
	BlobStream           json.RawMessage   `gorm:"column:blob_stream"`
	BlobStreamSize       int64             `gorm:"column:blob_stream_size"`
	UserID               string            `gorm:"column:user_id"`
	UserName             string            `gorm:"column:user_name"`
	UserEmail            string            `gorm:"column:user_email"`
	Status               string            `gorm:"column:status"`

	CreatedAt  time.Time  `gorm:"column:created_at"`
	EndSession *time.Time `gorm:"column:ended_at"`
}

type SessionList struct {
	Total       int64
	HasNextPage bool
	Items       []Session
}

func GetSessionByID(orgID, sid string) (*Session, error) {
	var session Session
	err := DB.Raw(`
	SELECT
		s.id, s.org_id, s.connection, s.connection_type, s.verb, s.labels,
		s.user_id, s.user_name, s.user_email, s.status, s.metadata, s.integrations_metadata, s.metrics,
		bi.blob_stream AS blob_input, bs.blob_stream AS blob_stream, pg_column_size(bs.blob_stream::TEXT) AS blob_stream_size,
		s.created_at, s.ended_at
	FROM private.sessions s
	LEFT JOIN private.blobs AS bi ON bi.type = 'session-input' AND  bi.id = s.blob_input_id
	LEFT JOIN private.blobs AS bs ON bs.type = 'session-stream' AND  bs.id = s.blob_stream_id
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

func ListSessions(orgID string, opt SessionOption) (*SessionList, error) {
	sessionList := &SessionList{Items: []Session{}}
	return sessionList, DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Raw(`
		SELECT COUNT(s.id)
		FROM private.sessions s
		WHERE s.org_id = @org_id AND
		(
			COALESCE(s.user_id::text, '') LIKE @user_id AND
			COALESCE(s.connection::text, '') LIKE @connection AND
			COALESCE(s.connection_type::text, '')::TEXT LIKE @connection_type AND
			CASE WHEN (@start_date)::text IS NOT NULL
				THEN s.created_at BETWEEN @start_date AND @end_date
				ELSE true
			END
		)`, map[string]any{
			"org_id":          orgID,
			"user_id":         opt.User,
			"connection":      opt.ConnectionName,
			"connection_type": opt.ConnectionType,
			"start_date":      opt.StartDate,
			"end_date":        opt.EndDate,
		}).First(&sessionList.Total).Error
		if err != nil {
			return fmt.Errorf("unable to obtain total count of sessions, reason=%v", err)
		}

		err = tx.Raw(`
		SELECT
			s.id, s.org_id, s.connection, s.connection_type, s.verb, s.labels,
			s.user_id, s.user_name, s.user_email, s.status, s.metadata, s.integrations_metadata, s.metrics,
			pg_column_size(bs.blob_stream::TEXT) AS blob_stream_size,
			s.created_at, s.ended_at
		FROM private.sessions s
		LEFT JOIN private.blobs AS bs ON bs.type = 'session-stream' AND  bs.id = s.blob_stream_id
		WHERE s.org_id = @org_id AND
		(
			COALESCE(s.user_id::text, '') LIKE @user_id AND
			COALESCE(s.connection::text, '') LIKE @connection AND
			COALESCE(s.connection_type::text, '')::TEXT LIKE @connection_type AND
			CASE WHEN (@start_date)::text IS NOT NULL
				THEN s.created_at BETWEEN @start_date AND @end_date
				ELSE true
			END
		)
		ORDER BY s.created_at DESC
		LIMIT @limit
		OFFSET @offset
		`, map[string]any{
			"org_id":          orgID,
			"user_id":         opt.User,
			"connection":      opt.ConnectionName,
			"connection_type": opt.ConnectionType,
			"start_date":      opt.StartDate,
			"end_date":        opt.EndDate,
			"limit":           opt.Limit,
			"offset":          opt.Offset,
		}).Find(&sessionList.Items).Error
		if err == nil {
			sessionList.HasNextPage = len(sessionList.Items) == opt.Limit
		}
		return err
	})
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
	SELECT COALESCE(integrations_metadata->>'jira_issue_key', '')::TEXT FROM private.sessions s
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

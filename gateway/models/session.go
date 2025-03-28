package models

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	tableSessions string = "private.sessions"
	tableBlobs    string = "private.blobs"
)

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
	User                string
	ConnectionType      string
	ConnectionName      string
	ReviewStatus        string
	ReviewApproverEmail *string
	StartDate           sql.NullString
	EndDate             sql.NullString
	Offset              int
	Limit               int
}

func NewSessionOption() SessionOption {
	return SessionOption{
		User:           "%",
		ConnectionType: "%",
		ConnectionName: "%",
		ReviewStatus:   "%",
		Limit:          20,
		Offset:         0,
	}
}

type Session struct {
	ID                   string            `gorm:"column:id"`
	OrgID                string            `gorm:"column:org_id"`
	Connection           string            `gorm:"column:connection"`
	ConnectionType       string            `gorm:"column:connection_type"`
	ConnectionSubtype    string            `gorm:"column:connection_subtype"`
	Verb                 string            `gorm:"column:verb"`
	Labels               map[string]string `gorm:"column:labels;serializer:json"`
	Metadata             map[string]any    `gorm:"column:metadata;serializer:json"`
	IntegrationsMetadata map[string]any    `gorm:"column:integrations_metadata;serializer:json"`
	Metrics              map[string]any    `gorm:"column:metrics;serializer:json"`
	BlobInputID          sql.NullString    `gorm:"column:blob_input_id"`
	BlobInput            BlobInputType     `gorm:"-"`
	BlobStream           json.RawMessage   `gorm:"-"`
	BlobStreamSize       int64             `gorm:"column:blob_stream_size;->"`
	UserID               string            `gorm:"column:user_id"`
	UserName             string            `gorm:"column:user_name"`
	UserEmail            string            `gorm:"column:user_email"`
	Status               string            `gorm:"column:status"`
	ExitCode             *int              `gorm:"column:exit_code"`
	Review               *Review           `gorm:"column:review;->"`

	CreatedAt  time.Time  `gorm:"column:created_at"`
	EndSession *time.Time `gorm:"column:ended_at"`
}

type SessionDone struct {
	ID         string
	OrgID      string
	Metrics    map[string]any
	BlobStream json.RawMessage
	ExitCode   *int
	Status     string
	EndSession *time.Time
}

type sessionDone struct {
	ID           string          `gorm:"column:id"`
	OrgID        string          `gorm:"column:org_id"`
	Metrics      map[string]any  `gorm:"column:metrics;serializer:json"`
	BlobStreamID sql.NullString  `gorm:"column:blob_stream_id"`
	BlobStream   json.RawMessage `gorm:"column:blob_stream"`
	ExitCode     *int            `gorm:"column:exit_code"`
	Status       string          `gorm:"column:status"`
	EndSession   *time.Time      `gorm:"column:ended_at"`
}

type SessionList struct {
	Total       int64
	HasNextPage bool
	Items       []Session
}

type Blob struct {
	ID         string          `gorm:"column:id"`
	OrgID      string          `gorm:"column:org_id"`
	BlobStream json.RawMessage `gorm:"column:blob_stream"`
	Type       string          `gorm:"column:type"`
}

type ReviewGroups struct {
	ID           string     `json:"id"`
	ReviewID     string     `json:"review_id"`
	GroupName    string     `json:"group_name"`
	Status       string     `json:"status"`
	OwnerID      *string    `json:"owner_id"`
	OwnerEmail   *string    `json:"owner_email"`
	OwnerName    *string    `json:"owner_name"`
	OwnerSlackID *string    `json:"owner_slack_id"`
	ReviewedAt   *time.Time `json:"reviewed_at"`
}

type Review struct {
	ID                string         `json:"id"`
	SessionID         string         `gorm:"column:session_id"`
	Type              string         `json:"type"`
	Status            string         `json:"status"`
	CreatedAt         time.Time      `json:"created_at"`
	RevokedAt         *time.Time     `json:"revoked_at"`
	AccessDurationSec int64          `json:"access_duration_sec"`
	ReviewGroups      []ReviewGroups `json:"review_groups"`
}

func (r *Review) Scan(value any) error {
	if value == nil {
		return nil
	}

	var data []byte
	switch v := value.(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return fmt.Errorf("unsupported data type: %T", value)
	}
	return json.Unmarshal(data, r)
}

func (s *Session) getBlobInput() (string, error) {
	var blob Blob
	err := DB.Raw(`
	SELECT b.id, b.org_id, b.blob_stream, b.type
	FROM private.sessions s
	LEFT JOIN private.blobs AS b ON b.type = 'session-input' AND  b.id = s.blob_input_id
	WHERE s.org_id = ? AND s.id = ?`, s.OrgID, s.ID).
		First(&blob).Error
	if err != nil {
		return "", err
	}

	result := []string{}
	if err := json.Unmarshal(blob.BlobStream, &result); err != nil {
		return "", fmt.Errorf("failed decoding blob input to []string: %v", err)
	}
	if len(result) == 0 {
		return "", nil
	}
	return result[0], nil
}

func (s *Session) GetBlobStream() (*Blob, error) {
	var blob Blob
	return &blob, DB.Raw(`
	SELECT b.id, b.org_id, b.blob_stream, b.type
	FROM private.sessions s
	LEFT JOIN private.blobs AS b ON b.type = 'session-stream' AND  b.id = s.blob_stream_id
	WHERE s.org_id = ? AND s.id = ?`, s.OrgID, s.ID).
		First(&blob).Error
}

func GetSessionByID(orgID, sid string) (*Session, error) {
	session := &Session{}
	err := DB.Debug().Raw(`
	SELECT
		s.id, s.org_id, s.connection, s.connection_type, s.connection_subtype, s.verb, s.labels, s.exit_code,
		s.user_id, s.user_name, s.user_email, s.status, s.metadata, s.integrations_metadata, s.metrics,
		metrics->>'event_size' AS blob_stream_size, s.blob_input_id,
		CASE
			WHEN rv.id IS NULL THEN NULL
			ELSE jsonb_build_object(
				'id', rv.id,
				'type', rv.type,
				'access_duration_sec', rv.access_duration_sec,
				'status', rv.status,
				'created_at', to_char(rv.created_at, 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
				'revoked_at', to_char(rv.revoked_at, 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
				'review_groups', (
					SELECT jsonb_agg(
						jsonb_build_object(
							'id', rg.id,
							'group_name', rg.group_name,
							'status', rg.status,
							'owner_id', rg.owner_id,
							'owner_email', rg.owner_email,
							'owner_name', rg.owner_name,
							'owner_slack_id', rg.owner_slack_id,
							'reviewed_at', to_char(rg.reviewed_at, 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')
						)
					)
					FROM private.review_groups AS rg
					WHERE rg.review_id = rv.id
				)
			)
		END AS review,
		s.created_at, s.ended_at
	FROM private.sessions s
	LEFT JOIN private.reviews AS rv ON rv.session_id = s.id
	WHERE s.org_id = ? AND s.id = ?
	`, orgID, sid).First(session).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	blobInput, err := session.getBlobInput()
	if err != nil {
		return nil, fmt.Errorf("failed obtaining blob input: %v", err)
	}
	session.BlobInput = BlobInputType(blobInput)
	return session, nil
}

func ListSessions(orgID string, opt SessionOption) (*SessionList, error) {
	sessionList := &SessionList{Items: []Session{}}
	return sessionList, DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Raw(`
		SELECT COUNT(s.id)
		FROM private.sessions s
		LEFT JOIN private.reviews AS rv ON rv.session_id = s.id
		WHERE s.org_id = @org_id AND
		(
			COALESCE(s.user_id::text, '') LIKE @user_id AND
			COALESCE(s.connection::text, '') LIKE @connection AND
			COALESCE(s.connection_type::text, '')::TEXT LIKE @connection_type AND
			COALESCE(rv.status::text, '')::TEXT LIKE @review_status AND
			CASE WHEN (@review_approver_email)::TEXT IS NOT NULL
				THEN
					EXISTS (
						SELECT 1 FROM private.review_groups rg
						INNER JOIN private.users u ON u.email = s.user_email
						INNER JOIN private.user_groups ug ON ug.user_id = u.id AND ug.name = rg.group_name
						WHERE rg.review_id = rv.id AND u.email = @review_approver_email
					)
				ELSE true
			END AND
			CASE WHEN (@start_date)::text IS NOT NULL
				THEN s.created_at BETWEEN @start_date AND @end_date
				ELSE true
			END
		)`, map[string]any{
			"org_id":                orgID,
			"user_id":               opt.User,
			"connection":            opt.ConnectionName,
			"connection_type":       opt.ConnectionType,
			"review_status":         opt.ReviewStatus,
			"review_approver_email": opt.ReviewApproverEmail,
			"start_date":            opt.StartDate,
			"end_date":              opt.EndDate,
		}).First(&sessionList.Total).Error
		if err != nil {
			return fmt.Errorf("unable to obtain total count of sessions, reason=%v", err)
		}

		err = tx.Raw(`
		SELECT
			s.id, s.org_id, s.connection, s.connection_type, s.connection_subtype, s.verb, s.labels, s.exit_code,
			s.user_id, s.user_name, s.user_email, s.status, s.metadata, s.integrations_metadata, s.metrics,
			metrics->>'event_size' AS blob_stream_size, s.blob_input_id, s.blob_stream_id,
			CASE
				WHEN rv.id IS NULL THEN NULL
				ELSE jsonb_build_object(
					'id', rv.id,
					'type', rv.type,
					'access_duration_sec', rv.access_duration_sec,
					'status', rv.status,
					'created_at', to_char(rv.created_at, 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
					'revoked_at', to_char(rv.revoked_at, 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
					'review_groups', (
						SELECT jsonb_agg(
							jsonb_build_object(
								'id', rg.id,
								'group_name', rg.group_name,
								'status', rg.status,
								'owner_id', rg.owner_id,
								'owner_email', rg.owner_email,
								'owner_name', rg.owner_name,
								'owner_slack_id', rg.owner_slack_id,
								'reviewed_at', to_char(rg.reviewed_at, 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')
							)
						)
						FROM private.review_groups AS rg
						WHERE rg.review_id = rv.id
					)
				)
			END AS review,
			s.created_at, s.ended_at
		FROM private.sessions s
		LEFT JOIN private.reviews AS rv ON rv.session_id = s.id
		WHERE s.org_id = @org_id AND
		(
			COALESCE(s.user_id::text, '') LIKE @user_id AND
			COALESCE(s.connection::text, '') LIKE @connection AND
			COALESCE(s.connection_type::text, '')::TEXT LIKE @connection_type AND
			COALESCE(rv.status::text, '')::TEXT LIKE @review_status AND
			CASE WHEN (@review_approver_email)::TEXT IS NOT NULL
				THEN
					EXISTS (
						SELECT 1 FROM private.review_groups rg
						INNER JOIN private.users u ON u.email = s.user_email
						INNER JOIN private.user_groups ug ON ug.user_id = u.id AND ug.name = rg.group_name
						WHERE rg.review_id = rv.id AND u.email = @review_approver_email
					)
				ELSE true
			END AND
			CASE WHEN (@start_date)::text IS NOT NULL
				THEN s.created_at BETWEEN @start_date AND @end_date
				ELSE true
			END
		)
		ORDER BY s.created_at DESC
		LIMIT @limit
		OFFSET @offset
		`, map[string]any{
			"org_id":                orgID,
			"user_id":               opt.User,
			"connection":            opt.ConnectionName,
			"connection_type":       opt.ConnectionType,
			"review_status":         opt.ReviewStatus,
			"review_approver_email": opt.ReviewApproverEmail,
			"start_date":            opt.StartDate,
			"end_date":              opt.EndDate,
			"limit":                 opt.Limit,
			"offset":                opt.Offset,
		}).Find(&sessionList.Items).Error
		if err == nil {
			sessionList.HasNextPage = len(sessionList.Items) == opt.Limit
		}
		return err
	})
}

// UpsertSession updates or create all attributes of a session with exception of
// session streams
func UpsertSession(sess Session) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		// generate deterministic uuid based on the session id to avoid duplicates
		blobInputID := sql.NullString{
			String: uuid.NewSHA1(uuid.NameSpaceURL, fmt.Appendf(nil, "blobinput:%s", sess.ID)).String(),
			Valid:  true,
		}

		blobInput := Blob{
			ID:         blobInputID.String,
			OrgID:      sess.OrgID,
			Type:       "session-input",
			BlobStream: json.RawMessage(fmt.Sprintf("[%q]", sess.BlobInput)),
		}
		res := tx.Table(tableBlobs).
			Where("org_id = ? AND id = ?", sess.OrgID, blobInputID.String).
			Updates(blobInput)
		if res.Error == nil && res.RowsAffected == 0 {
			res.Error = tx.Table(tableBlobs).Create(blobInput).Error
		}

		if res.Error != nil {
			return fmt.Errorf("failed creating session blob input, reason=%v", res.Error)
		}
		return tx.Table(tableSessions).Save(
			Session{
				ID:                   sess.ID,
				OrgID:                sess.OrgID,
				Labels:               sess.Labels,
				Metadata:             sess.Metadata,
				IntegrationsMetadata: sess.IntegrationsMetadata,
				Metrics:              sess.Metrics,
				Connection:           sess.Connection,
				ConnectionType:       sess.ConnectionType,
				ConnectionSubtype:    sess.ConnectionSubtype,
				Verb:                 sess.Verb,
				UserID:               sess.UserID,
				UserName:             sess.UserName,
				UserEmail:            sess.UserEmail,
				BlobInputID:          blobInputID,
				Status:               sess.Status,
				ExitCode:             sess.ExitCode,
				CreatedAt:            sess.CreatedAt,
				EndSession:           sess.EndSession,
			}).Error
	})
}

// UpdateSessionEventStream updates a session partially
func UpdateSessionEventStream(sess SessionDone) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		// generate deterministic uuid based on the session id to avoid duplicates
		blobStreamID := sql.NullString{
			String: uuid.NewSHA1(uuid.NameSpaceURL, fmt.Appendf(nil, "blobstream:%s", sess.ID)).String(),
			Valid:  true,
		}

		blobStream := Blob{
			ID:         blobStreamID.String,
			OrgID:      sess.OrgID,
			BlobStream: sess.BlobStream,
			Type:       "session-stream",
		}
		res := tx.Table(tableBlobs).
			Where("org_id = ? AND id = ?", sess.OrgID, blobStreamID.String).
			Updates(blobStream)
		if res.Error == nil && res.RowsAffected == 0 {
			res.Error = tx.Table(tableBlobs).Create(blobStream).Error
		}

		if res.Error != nil {
			return fmt.Errorf("failed creating session blob stream, reason=%v", res.Error)
		}

		// update: status, labels, metrics, end_date, exit_code, event_stream
		return tx.Table(tableSessions).
			Where("org_id = ? AND id = ?", sess.OrgID, sess.ID).
			Updates(sessionDone{
				ID:           sess.ID,
				OrgID:        sess.OrgID,
				Metrics:      sess.Metrics,
				BlobStreamID: blobStreamID,
				ExitCode:     sess.ExitCode,
				Status:       sess.Status,
				EndSession:   sess.EndSession,
			}).
			Error
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

func UpdateSessionMetadata(orgID, userEmail, sid string, metadata map[string]any) error {
	res := DB.Table(tableSessions).
		Where("org_id = ? AND id = ? AND user_email = ?", orgID, sid, userEmail).
		Updates(Session{Metadata: metadata})
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

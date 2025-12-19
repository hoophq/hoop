package models

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/hoophq/hoop/gateway/api/openapi"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SessionMetrics struct {
	ID        string `gorm:"column:id;->"`
	OrgID     string `gorm:"column:org_id"`
	SessionID string `gorm:"column:session_id"`

	InfoType      string `gorm:"column:info_type"`
	CountMasked   int64  `gorm:"column:count_masked"`
	CountAnalyzed int64  `gorm:"column:count_analyzed"`

	ConnectionType    string         `gorm:"column:connection_type"`
	ConnectionSubtype sql.NullString `gorm:"column:connection_subtype"`

	SessionCreatedAt time.Time  `gorm:"column:session_created_at"`
	SessionEndedAt   *time.Time `gorm:"column:session_ended_at"`
}

func IncrementSessionMaskedMetrics(db *gorm.DB, sessionID string, maskedMetrics map[string]int64) error {
	if len(maskedMetrics) == 0 {
		return nil
	}

	session := &Session{}
	if err := db.Table("private.sessions").Where("id = ?", sessionID).First(session).Error; err != nil {
		return err
	}

	for infoType, count := range maskedMetrics {
		err := db.Table("private.session_metrics").Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "session_id"}, {Name: "info_type"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"count_masked":     gorm.Expr("session_metrics.count_masked + ?", count),
				"session_ended_at": session.EndSession,
			}),
		}).Create(&SessionMetrics{
			SessionID: session.ID,
			OrgID:     session.OrgID,

			InfoType:    infoType,
			CountMasked: count,

			ConnectionType:    session.ConnectionType,
			ConnectionSubtype: sql.NullString{String: session.ConnectionSubtype, Valid: session.ConnectionSubtype != ""},

			SessionCreatedAt: session.CreatedAt,
			SessionEndedAt:   session.EndSession,
		}).Error

		if err != nil {
			return err
		}
	}

	return nil
}

func IncrementSessionAnalyzedMetrics(db *gorm.DB, sessionID string, analyzedMetrics map[string]int64) error {
	if len(analyzedMetrics) == 0 {
		return nil
	}

	session := &Session{}
	if err := db.Table("private.sessions").Where("id = ?", sessionID).First(session).Error; err != nil {
		return err
	}

	for infoType, count := range analyzedMetrics {
		err := db.Table("private.session_metrics").Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "session_id"}, {Name: "info_type"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"count_analyzed":   gorm.Expr("session_metrics.count_analyzed + ?", count),
				"session_ended_at": session.EndSession,
			}),
		}).Create(&SessionMetrics{
			SessionID: session.ID,
			OrgID:     session.OrgID,

			InfoType:      infoType,
			CountAnalyzed: count,

			ConnectionType:    session.ConnectionType,
			ConnectionSubtype: sql.NullString{String: session.ConnectionSubtype, Valid: session.ConnectionSubtype != ""},

			SessionCreatedAt: session.CreatedAt,
			SessionEndedAt:   session.EndSession,
		}).Error

		if err != nil {
			return err
		}
	}

	return nil
}

func SetSessionMetricsEndedAt(db *gorm.DB, sessionID string) error {
	session := &Session{}
	if err := db.Table("private.sessions").Where("id = ?", sessionID).First(session).Error; err != nil {
		return err
	}

	if session.EndSession == nil {
		return nil
	}

	return db.
		Table("private.session_metrics").
		Where("session_id = ?", sessionID).
		Update("session_ended_at", session.EndSession).
		Error
}

// SessionMetricsFilter represents the filter parameters for querying session metrics
type SessionMetricsFilter struct {
	// Resource filters
	ConnectionTypes    []string `form:"connection_type"`
	ConnectionSubtypes []string `form:"connection_subtype"`
	ConnectionNames    []string `form:"connection_name"`

	// Data type filters (Presidio entity types)
	InfoTypes []string `form:"info_type"`

	// Masked/unmasked differentiation
	OnlyMasked   bool `form:"only_masked"`
	OnlyUnmasked bool `form:"only_unmasked"`

	// Date filters
	StartDate *time.Time `form:"start_date" time_format:"2006-01-02"`
	EndDate   *time.Time `form:"end_date" time_format:"2006-01-02"`

	// Session filters
	SessionIDs          []string   `form:"session_id"`
	SessionStartDate    *time.Time `form:"session_start_date" time_format:"2006-01-02"`
	SessionEndDate      *time.Time `form:"session_end_date" time_format:"2006-01-02"`
	MinDurationSec      *int       `form:"min_duration_sec"`
	MaxDurationSec      *int       `form:"max_duration_sec"`
	IncludeOpenSessions bool       `form:"include_open_sessions"`

	// Logic operator (AND/OR)
	LogicOperator string `form:"logic_operator"` // "and" or "or", default "and"

	// Pagination
	Page  int `form:"page"`
	Limit int `form:"limit"`
}

func buildFilterConditions(filter SessionMetricsFilter, params map[string]interface{}) []string {
	conditions := []string{}

	// Connection type filter
	if len(filter.ConnectionTypes) > 0 {
		conditions = append(conditions, "sm.connection_type = ANY(@connection_types)")
		params["connection_types"] = filter.ConnectionTypes
	}

	// Connection subtype filter
	if len(filter.ConnectionSubtypes) > 0 {
		conditions = append(conditions, "sm.connection_subtype = ANY(@connection_subtypes)")
		params["connection_subtypes"] = filter.ConnectionSubtypes
	}

	// Connection name filter
	if len(filter.ConnectionNames) > 0 {
		conditions = append(conditions, "s.connection = ANY(@connection_names)")
		params["connection_names"] = filter.ConnectionNames
	}

	// Info type (data type) filter
	if len(filter.InfoTypes) > 0 {
		conditions = append(conditions, "sm.info_type = ANY(@info_types)")
		params["info_types"] = filter.InfoTypes
	}

	// Masked/unmasked filter
	if filter.OnlyMasked && !filter.OnlyUnmasked {
		conditions = append(conditions, "sm.count_masked > 0")
	} else if filter.OnlyUnmasked && !filter.OnlyMasked {
		conditions = append(conditions, "sm.count_masked = 0 AND sm.count_analyzed > 0")
	}

	// Date range filter (for session metrics created_at)
	if filter.StartDate != nil {
		conditions = append(conditions, "sm.session_created_at >= @start_date")
		params["start_date"] = filter.StartDate
	}
	if filter.EndDate != nil {
		// Add 1 day to include the entire end date
		endDate := filter.EndDate.Add(24 * time.Hour)
		conditions = append(conditions, "sm.session_created_at < @end_date")
		params["end_date"] = endDate
	}

	// Session ID filter
	if len(filter.SessionIDs) > 0 {
		conditions = append(conditions, "sm.session_id = ANY(@session_ids)")
		params["session_ids"] = filter.SessionIDs
	}

	// Session start date filter
	if filter.SessionStartDate != nil {
		conditions = append(conditions, "sm.session_created_at >= @session_start_date")
		params["session_start_date"] = filter.SessionStartDate
	}

	// Session end date filter
	if filter.SessionEndDate != nil {
		if filter.IncludeOpenSessions {
			conditions = append(conditions, "(sm.session_ended_at IS NULL OR sm.session_ended_at <= @session_end_date)")
		} else {
			conditions = append(conditions, "sm.session_ended_at IS NOT NULL AND sm.session_ended_at <= @session_end_date")
		}
		params["session_end_date"] = filter.SessionEndDate.Add(24 * time.Hour)
	} else if !filter.IncludeOpenSessions {
		// If not including open sessions and no end date filter, exclude null ended_at
		conditions = append(conditions, "sm.session_ended_at IS NOT NULL")
	}

	// Session duration filters
	if filter.MinDurationSec != nil {
		conditions = append(conditions, "EXTRACT(EPOCH FROM (sm.session_ended_at - sm.session_created_at)) >= @min_duration_sec")
		params["min_duration_sec"] = *filter.MinDurationSec
	}
	if filter.MaxDurationSec != nil {
		conditions = append(conditions, "EXTRACT(EPOCH FROM (sm.session_ended_at - sm.session_created_at)) <= @max_duration_sec")
		params["max_duration_sec"] = *filter.MaxDurationSec
	}

	return conditions
}

type SessionMetricsQueryResult struct {
	SessionID          string     `gorm:"column:session_id"`
	OrgID              string     `gorm:"column:org_id"`
	ConnectionType     string     `gorm:"column:connection_type"`
	ConnectionSubtype  *string    `gorm:"column:connection_subtype"`
	ConnectionName     string     `gorm:"column:connection_name"`
	InfoType           string     `gorm:"column:info_type"`
	CountMasked        int64      `gorm:"column:count_masked"`
	CountAnalyzed      int64      `gorm:"column:count_analyzed"`
	IsMasked           bool       `gorm:"column:is_masked"`
	SessionCreatedAt   time.Time  `gorm:"column:session_created_at"`
	SessionEndedAt     *time.Time `gorm:"column:session_ended_at"`
	SessionDurationSec *int       `gorm:"column:session_duration_sec"`
}

type SessionMetricsAggregatedResult struct {
	TotalSessions         int64    `gorm:"column:total_sessions"`
	UniqueInfoTypes       int64    `gorm:"column:unique_info_types"`
	TotalMasked           int64    `gorm:"column:total_masked"`
	TotalAnalyzed         int64    `gorm:"column:total_analyzed"`
	SessionsWithMasking   int64    `gorm:"column:sessions_with_masking"`
	AvgSessionDurationSec *float64 `gorm:"column:avg_session_duration_sec"`
}

func GetSessionMetrics(orgID string, filter SessionMetricsFilter) ([]SessionMetricsQueryResult, *openapi.Pagination, error) {
	// Set default limit if not provided
	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	if filter.Limit > 1000 {
		filter.Limit = 1000
	}

	if filter.Page <= 0 {
		filter.Page = 1
	}

	// Set default logic operator
	if filter.LogicOperator == "" {
		filter.LogicOperator = "and"
	}
	filter.LogicOperator = strings.ToLower(filter.LogicOperator)

	// Build the WHERE clause based on logic operator
	whereConditions := []string{"sm.org_id = @org_id"}
	params := map[string]interface{}{
		"org_id": orgID,
		"offset": (filter.Page - 1) * filter.Limit,
		"limit":  filter.Limit,
	}

	// Build conditions based on filters
	conditions := buildFilterConditions(filter, params)

	// Combine conditions with AND/OR
	if len(conditions) > 0 {
		if filter.LogicOperator == "or" {
			whereConditions = append(whereConditions, fmt.Sprintf("(%s)", strings.Join(conditions, " OR ")))
		} else {
			whereConditions = append(whereConditions, conditions...)
		}
	}

	whereClause := strings.Join(whereConditions, " AND ")

	// Query for total count
	var total int64
	countQuery := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM private.session_metrics sm
		INNER JOIN private.sessions s ON s.id = sm.session_id
		WHERE %s
	`, whereClause)

	if err := DB.Raw(countQuery, params).Count(&total).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to get total count: %w", err)
	}

	// Query for data
	query := fmt.Sprintf(`
		SELECT
			sm.session_id,
			sm.org_id,
			sm.connection_type,
			sm.connection_subtype,
			s.connection as connection_name,
			sm.info_type,
			sm.count_masked,
			sm.count_analyzed,
			CASE WHEN sm.count_masked > 0 THEN true ELSE false END as is_masked,
			sm.session_created_at,
			sm.session_ended_at,
			CASE
				WHEN sm.session_ended_at IS NOT NULL
				THEN EXTRACT(EPOCH FROM (sm.session_ended_at - sm.session_created_at))::INTEGER
				ELSE NULL
			END as session_duration_sec
		FROM private.session_metrics sm
		INNER JOIN private.sessions s ON s.id = sm.session_id
		WHERE %s
		ORDER BY sm.session_created_at DESC, sm.session_id, sm.info_type
		LIMIT @limit OFFSET @offset
	`, whereClause)

	var items []SessionMetricsQueryResult
	if err := DB.Raw(query, params).Scan(&items).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to query session metrics: %w", err)
	}

	totalPages := int((total + int64(filter.Limit) - 1) / int64(filter.Limit))

	pagination := &openapi.Pagination{
		Total: totalPages,
		Page:  filter.Page,
		Size:  filter.Limit,
	}

	return items, pagination, nil
}

func GetSessionMetricsAggregated(orgID string, filter SessionMetricsFilter) (*SessionMetricsAggregatedResult, error) {
	params := map[string]interface{}{
		"org_id": orgID,
	}
	conditions := buildFilterConditions(filter, params)
	whereConditions := []string{"sm.org_id = @org_id"}
	whereConditions = append(whereConditions, conditions...)
	whereClause := strings.Join(whereConditions, " AND ")

	query := fmt.Sprintf(`
		SELECT
			COUNT(DISTINCT sm.session_id) as total_sessions,
			COUNT(DISTINCT sm.info_type) as unique_info_types,
			SUM(sm.count_masked) as total_masked,
			SUM(sm.count_analyzed) as total_analyzed,
			COUNT(DISTINCT CASE WHEN sm.count_masked > 0 THEN sm.session_id END) as sessions_with_masking,
			AVG(CASE
				WHEN sm.session_ended_at IS NOT NULL
				THEN EXTRACT(EPOCH FROM (sm.session_ended_at - sm.session_created_at))
				ELSE NULL
			END) as avg_session_duration_sec
		FROM private.session_metrics sm
		INNER JOIN private.sessions s ON s.id = sm.session_id
		WHERE %s
	`, whereClause)

	var result SessionMetricsAggregatedResult
	if err := DB.Raw(query, params).Scan(&result).Error; err != nil {
		return nil, fmt.Errorf("failed to get aggregated metrics: %w", err)
	}

	return &result, nil
}

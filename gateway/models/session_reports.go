package models

type SessionReport struct {
	Items                 []SessionReportItem `gorm:"column:items"`
	TotalRedactCount      int64               `gorm:"column:total_redact_count"`
	TotalTransformedBytes int64               `gorm:"column:total_transformed_bytes"`
}

type SessionReportItem struct {
	ResourceName     string `gorm:"column:resource"`
	InfoType         string `gorm:"column:info_type"`
	RedactTotal      int64  `gorm:"column:redact_total"`
	TransformedBytes int64  `gorm:"column:transformed_bytes"`
}

func GetSessionReport(orgID string, opts map[string]any) (*SessionReport, error) {
	result := SessionReport{Items: []SessionReportItem{}}
	err := DB.Raw(`
	WITH info_types AS (
        SELECT id, info_type, redact_total::numeric
        FROM
            private.sessions,
            jsonb_each_text(metrics->'data_masking'->'info_types') AS kv(info_type, redact_total)
        WHERE org_id::TEXT = @org_id
        AND metrics is not null
    ), metrics AS (
        SELECT
            CASE @group_by
                WHEN 'connection_name' THEN s.connection
                WHEN 'id' THEN s.id::text
                WHEN 'user_email' THEN s.user_email
                WHEN 'connection_type' THEN s.connection_type::text
            END AS resource,
            i.info_type,
            SUM(i.redact_total) AS redact_total,
            SUM((metrics->'data_masking'->'transformed_bytes')::INT) AS transformed_bytes
        FROM private.sessions s
        INNER JOIN info_types i ON s.id = i.id
        WHERE s.org_id::TEXT = @org_id
        AND metrics is not null
        AND ended_at BETWEEN TO_TIMESTAMP(@start_date, 'YYYY-MM-DD') AND TO_TIMESTAMP(@end_date, 'YYYY-MM-DD')
        AND CASE WHEN @session_id != '' THEN s.id::TEXT = @session_id ELSE true END
        AND CASE WHEN @connection_name != '' THEN s.connection = @connection_name ELSE true END
        AND CASE WHEN @connection_type != '' THEN s.connection_type::TEXT = @connection_type ELSE true END
    	AND CASE WHEN @verb != '' THEN s.verb::TEXT = @verb ELSE true END
        AND CASE WHEN @user_email != '' THEN s.user_email = @user_email ELSE true END
        GROUP BY 1, 2
    ) SELECT * FROM metrics
	`, map[string]any{
		"org_id":          orgID,
		"group_by":        opts["group_by"],
		"start_date":      opts["start_date"],
		"end_date":        opts["end_date"],
		"session_id":      opts["id"],
		"connection_name": opts["connection_name"],
		"connection_type": opts["connection_type"],
		"verb":            opts["verb"],
		"user_email":      opts["user_email"],
	}).Find(&result.Items).
		Error

	for _, item := range result.Items {
		result.TotalRedactCount += item.RedactTotal
		result.TotalTransformedBytes += item.TransformedBytes
	}
	return &result, err
}

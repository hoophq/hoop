package analytics

import (
	"database/sql"
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
)

func TestSessionUsageProperties(t *testing.T) {
	tests := []struct {
		name       string
		session    *models.Session
		connection *models.Connection
		agent      *models.Agent
		expected   map[string]any
	}{
		{
			name: "Basic properties with no AI analysis or end session",
			session: &models.Session{
				OrgID:             "org-123",
				ID:                "session-456",
				ConnectionType:    "resource-type-1",
				ConnectionSubtype: "resource-subtype-1",
				Status:            "active",
				CreatedAt:         time.Date(2023, time.March, 26, 0, 0, 0, 0, time.UTC),
			},
			connection: &models.Connection{
				JiraIssueTemplateID: sql.NullString{Valid: true, String: "jira-template-123"},
			},
			agent: &models.Agent{
				Metadata: map[string]string{
					"version":  "1.0.0",
					"platform": "linux",
				},
			},
			expected: map[string]any{
				"org-id":                        "org-123",
				"session-id":                    "session-456",
				"resource-type":                 "resource-type-1",
				"resource-subtype":              "resource-subtype-1",
				"status":                        "active",
				"created-at":                    "2023-03-26 00:00:00 +0000 UTC",
				"ai-session-analyzer-activated": false,
				"agent-version":                 "1.0.0",
				"agent-platform":                "linux",
				"jira-template-activated":       true,
			},
		},
		{
			name: "With AI analysis",
			session: &models.Session{
				OrgID:             "org-123",
				ID:                "session-789",
				ConnectionType:    "resource-type-1",
				ConnectionSubtype: "resource-subtype-2",
				Status:            "completed",
				CreatedAt:         time.Date(2023, time.March, 26, 0, 0, 0, 0, time.UTC),
				AIAnalysis: &models.SessionAIAnalysis{
					RiskLevel: "high",
					Action:    "block",
				},
			},
			connection: &models.Connection{
				JiraIssueTemplateID: sql.NullString{Valid: false},
			},
			agent: &models.Agent{
				Metadata: map[string]string{
					"version":  "2.0.1",
					"platform": "macos",
				},
			},
			expected: map[string]any{
				"org-id":                              "org-123",
				"session-id":                          "session-789",
				"resource-type":                       "resource-type-1",
				"resource-subtype":                    "resource-subtype-2",
				"status":                              "completed",
				"created-at":                          "2023-03-26 00:00:00 +0000 UTC",
				"ai-session-analyzer-activated":       true,
				"ai-session-analyzer-identified-risk": "high",
				"ai-session-analyzer-action":          "block",
				"agent-version":                       "2.0.1",
				"agent-platform":                      "macos",
				"jira-template-activated":             false,
			},
		},
		{
			name: "With end session timestamp",
			session: &models.Session{
				OrgID:             "org-987",
				ID:                "session-end-123",
				ConnectionType:    "resource-type-end",
				ConnectionSubtype: "resource-sub-end",
				Status:            "ended",
				CreatedAt:         time.Date(2023, time.March, 26, 14, 0, 0, 0, time.UTC),
				EndSession:        func() *time.Time { t := time.Date(2023, time.March, 26, 16, 0, 0, 0, time.UTC); return &t }(),
			},
			connection: &models.Connection{
				JiraIssueTemplateID: sql.NullString{Valid: false},
			},
			agent: &models.Agent{
				Metadata: map[string]string{
					"version":  "3.1.4",
					"platform": "windows",
				},
			},
			expected: map[string]any{
				"org-id":                        "org-987",
				"session-id":                    "session-end-123",
				"resource-type":                 "resource-type-end",
				"resource-subtype":              "resource-sub-end",
				"status":                        "ended",
				"created-at":                    "2023-03-26 14:00:00 +0000 UTC",
				"finished-at":                   "2023-03-26 16:00:00 +0000 UTC",
				"ai-session-analyzer-activated": false,
				"agent-version":                 "3.1.4",
				"agent-platform":                "windows",
				"jira-template-activated":       false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sessionUsageProperties(tt.session, tt.connection, tt.agent)
			assert.Equal(t, tt.expected, result)
		})
	}
}

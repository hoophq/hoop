package analytics

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/lib/pq"
)

func TestSessionUsageProperties(t *testing.T) {
	now := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	endTime := now.Add(5 * time.Minute)
	minApprovals := 2

	baseSession := &models.Session{
		ID:                "sess-123",
		OrgID:             "org-456",
		ConnectionType:    "database",
		ConnectionSubtype: "postgres",
		Status:            "done",
		CreatedAt:         now,
	}

	baseConnection := &models.Connection{
		Name: "my-conn",
	}

	baseAgent := &models.Agent{
		Metadata: map[string]string{
			"version":  "1.2.3",
			"platform": "linux",
		},
	}

	t.Run("basic properties with no optional features", func(t *testing.T) {
		props := sessionUsageProperties(
			baseSession,
			baseConnection,
			baseAgent,
			nil,                   // no guardrails
			json.RawMessage("[]"), // empty data masking
			nil,                   // no access request rules
		)

		assertProp(t, props, "org-id", "org-456")
		assertProp(t, props, "session-id", "sess-123")
		assertProp(t, props, "resource-type", "database")
		assertProp(t, props, "resource-subtype", "postgres")
		assertProp(t, props, "status", "done")
		assertProp(t, props, "created-at", now.String())
		assertProp(t, props, "agent-version", "1.2.3")
		assertProp(t, props, "agent-platform", "linux")
		assertProp(t, props, "ai-session-analyzer-activated", false)
		assertProp(t, props, "mandatory-metadata-activated", false)
		assertProp(t, props, "jira-template-activated", false)
		assertProp(t, props, "jit-access-request-activated", false)
		assertProp(t, props, "command-access-request-activated", false)
		assertProp(t, props, "guardrails-activated", false)
		assertProp(t, props, "data-masking-activated", false)

		if _, ok := props["finished-at"]; ok {
			t.Error("finished-at should not be set when EndSession is nil")
		}
	})

	t.Run("session with EndSession set", func(t *testing.T) {
		s := *baseSession
		s.EndSession = &endTime

		props := sessionUsageProperties(&s, baseConnection, baseAgent, nil, json.RawMessage("[]"), nil)

		assertProp(t, props, "finished-at", endTime.String())
	})

	t.Run("session with AI analysis", func(t *testing.T) {
		s := *baseSession
		s.AIAnalysis = &models.SessionAIAnalysis{
			RiskLevel: "high",
			Action:    "block",
		}

		props := sessionUsageProperties(&s, baseConnection, baseAgent, nil, json.RawMessage("[]"), nil)

		assertProp(t, props, "ai-session-analyzer-activated", true)
		assertProp(t, props, "ai-session-analyzer-identified-risk", "high")
		assertProp(t, props, "ai-session-analyzer-action", "block")
	})

	t.Run("connection with mandatory metadata fields", func(t *testing.T) {
		c := *baseConnection
		c.MandatoryMetadataFields = pq.StringArray{"field1", "field2"}

		props := sessionUsageProperties(baseSession, &c, baseAgent, nil, json.RawMessage("[]"), nil)

		assertProp(t, props, "mandatory-metadata-activated", true)
	})

	t.Run("connection with jira template", func(t *testing.T) {
		c := *baseConnection
		c.JiraIssueTemplateID = sql.NullString{String: "TMPL-001", Valid: true}

		props := sessionUsageProperties(baseSession, &c, baseAgent, nil, json.RawMessage("[]"), nil)

		assertProp(t, props, "jira-template-activated", true)
	})

	t.Run("jira template with empty string is not activated", func(t *testing.T) {
		c := *baseConnection
		c.JiraIssueTemplateID = sql.NullString{String: "", Valid: true}

		props := sessionUsageProperties(baseSession, &c, baseAgent, nil, json.RawMessage("[]"), nil)

		assertProp(t, props, "jira-template-activated", false)
	})

	t.Run("guardrails activated with non-empty rules", func(t *testing.T) {
		gr := &models.ConnectionGuardRailRules{
			GuardRailInputRules:  []byte(`[{"rule":"no-drop"}]`),
			GuardRailOutputRules: []byte(`[{"rule":"mask-ssn"}]`),
		}

		props := sessionUsageProperties(baseSession, baseConnection, baseAgent, gr, json.RawMessage("[]"), nil)

		assertProp(t, props, "guardrails-activated", true)
	})

	t.Run("guardrails not activated with empty rules", func(t *testing.T) {
		gr := &models.ConnectionGuardRailRules{
			GuardRailInputRules:  []byte(`[]`),
			GuardRailOutputRules: []byte(`[]`),
		}

		props := sessionUsageProperties(baseSession, baseConnection, baseAgent, gr, json.RawMessage("[]"), nil)

		assertProp(t, props, "guardrails-activated", false)
	})

	t.Run("data masking activated", func(t *testing.T) {
		dm := json.RawMessage(`[{"type":"email"}]`)

		props := sessionUsageProperties(baseSession, baseConnection, baseAgent, nil, dm, nil)

		assertProp(t, props, "data-masking-activated", true)
	})

	t.Run("access request rules with jit and command", func(t *testing.T) {
		rules := []*models.AccessRequestRule{
			{
				ID:                   uuid.New(),
				AccessType:           "jit",
				ForceApprovalGroups:  pq.StringArray{"admins"},
				AllGroupsMustApprove: true,
				MinApprovals:         &minApprovals,
			},
			{
				ID:                   uuid.New(),
				AccessType:           "command",
				ForceApprovalGroups:  pq.StringArray{},
				AllGroupsMustApprove: false,
			},
		}

		props := sessionUsageProperties(baseSession, baseConnection, baseAgent, nil, json.RawMessage("[]"), rules)

		assertProp(t, props, "jit-access-request-activated", true)
		assertProp(t, props, "jit-access-request-force-approval", true)
		assertProp(t, props, "jit-access-request-all-groups-must-approve", true)
		assertPropExists(t, props, "jit-access-request-minimum-approval")

		assertProp(t, props, "command-access-request-activated", true)
		assertProp(t, props, "command-access-request-force-approval", false)
		assertProp(t, props, "command-access-request-all-groups-must-approve", false)

		if _, ok := props["command-access-request-minimum-approval"]; ok {
			t.Error("command-access-request-minimum-approval should not be set when MinApprovals is nil")
		}
	})

	t.Run("access request rules with nil entries are skipped", func(t *testing.T) {
		rules := []*models.AccessRequestRule{nil, nil}

		props := sessionUsageProperties(baseSession, baseConnection, baseAgent, nil, json.RawMessage("[]"), rules)

		assertProp(t, props, "jit-access-request-activated", false)
		assertProp(t, props, "command-access-request-activated", false)
	})

	t.Run("nil agent returns empty metadata", func(t *testing.T) {
		nilAgent := &models.Agent{}

		props := sessionUsageProperties(baseSession, baseConnection, nilAgent, nil, json.RawMessage("[]"), nil)

		assertProp(t, props, "agent-version", "")
		assertProp(t, props, "agent-platform", "")
	})
}

func assertProp(t *testing.T, props map[string]any, key string, expected any) {
	t.Helper()
	val, ok := props[key]
	if !ok {
		t.Errorf("expected property %q to exist", key)
		return
	}
	// Handle *int comparison
	if ep, ok := expected.(*int); ok {
		if vp, ok := val.(*int); ok {
			if *ep != *vp {
				t.Errorf("property %q = %v, want %v", key, *vp, *ep)
			}
			return
		}
	}
	if val != expected {
		t.Errorf("property %q = %v (%T), want %v (%T)", key, val, val, expected, expected)
	}
}

func assertPropExists(t *testing.T, props map[string]any, key string) {
	t.Helper()
	if _, ok := props[key]; !ok {
		t.Errorf("expected property %q to exist", key)
	}
}

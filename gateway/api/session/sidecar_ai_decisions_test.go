package sessionapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const aiDecisionsOrgID = "00000000-0000-0000-0000-0000000000f1"

// getSession runs GET /sessions/{id} as an admin of org.
func getSession(t *testing.T, orgID, sessionID string) map[string]any {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set(storagev2.ContextKey, storagev2.NewContext("user-1", orgID).
		WithUserInfo("User One", "user1@hoop.dev", "active", "", []string{types.GroupAdmin}))
	c.Params = gin.Params{{Key: "session_id", Value: sessionID}}
	c.Request = httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID+"?expand=event_stream", nil)
	Get(c)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

// The analyzer's decision on each statement reaches the session details, in
// metadata.sidecar, bounded, and once per event.
func TestASidecarSessionReturnsItsAIDecisions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	require.NoError(t, models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'ai-decisions')`,
		aiDecisionsOrgID).Error)
	sc := &models.Sidecar{OrgID: aiDecisionsOrgID, Name: "edge", KeyHash: models.HashAPIKey("hsc_ai_decisions"),
		CreatedBy: "tests@hoop.dev"}
	require.NoError(t, models.CreateSidecar(models.DB, sc))

	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	reviewID := uuid.NewString()
	event := func(seq int64, kind audit.Kind, md map[string]string) daemon.SessionEvent {
		return daemon.SessionEvent{Seq: seq, Event: audit.Event{
			Kind: kind, Timestamp: t0.Add(time.Duration(seq) * time.Second), SessionID: "s-1",
			Principal: "alice@example.com", Protocol: inspect.Postgres, Connection: "appdb",
			Statement: fmt.Sprintf("SELECT %d", seq), Metadata: md,
		}}
	}
	batch := []daemon.SessionEvent{
		{Seq: 1, Event: audit.Event{Kind: audit.KindSessionStart, Timestamp: t0, SessionID: "s-1",
			Principal: "alice@example.com", Protocol: inspect.Postgres, Connection: "appdb"}},
		event(2, audit.KindStatement, map[string]string{
			analyzer.MetadataRiskLevel: "medium", analyzer.MetadataAction: "ai-handoff",
			analyzer.MetadataHandoffDecision: "require_approval", analyzer.MetadataHandoffReason: "writes rows",
			analyzer.MetadataApprover: "human", analyzer.MetadataReviewID: reviewID,
		}),
		event(3, audit.KindViolation, map[string]string{
			analyzer.MetadataRiskLevel: "high", analyzer.MetadataAction: "require_review",
			analyzer.MetadataApprover: "ai_agent", analyzer.MetadataApprovalDecision: "rejected",
			analyzer.MetadataApprovalReason: "drops a table",
		}),
		// Not rated: no entry.
		event(4, audit.KindStatement, map[string]string{analyzer.MetadataAIStatus: "skipped"}),
	}
	_, err := services.ApplySidecarSessionEvents(models.DB, sc, batch)
	require.NoError(t, err)
	sessionID := services.SidecarSessionID(sc.ID, "s-1")

	decisions := func() ([]any, any) {
		t.Helper()
		got := getSession(t, aiDecisionsOrgID, sessionID)
		md, ok := got["metadata"].(map[string]any)
		require.True(t, ok, "the session has no metadata: %v", got)
		sidecar, ok := md["sidecar"].(map[string]any)
		require.True(t, ok, "metadata has no sidecar: %v", md)
		list, _ := sidecar["ai_decisions"].([]any)
		return list, sidecar["ai_decisions_dropped"]
	}
	list, dropped := decisions()
	require.Len(t, list, 2)
	assert.Nil(t, dropped)
	assert.Equal(t, map[string]any{
		"seq": float64(2), "timestamp": "2026-10-01T12:00:02Z", "risk_level": "medium", "risk_action": "ai-handoff",
		"handoff_decision": "require_approval", "handoff_reason": "writes rows", "approver": "human",
		"review_id": reviewID,
	}, list[0])
	assert.Equal(t, map[string]any{
		"seq": float64(3), "timestamp": "2026-10-01T12:00:03Z", "risk_level": "high", "risk_action": "require_review",
		"approver": "ai_agent", "approval_decision": "rejected", "approval_reason": "drops a table",
	}, list[1])

	// A resend adds nothing.
	_, err = services.ApplySidecarSessionEvents(models.DB, sc, batch)
	require.NoError(t, err)
	list, _ = decisions()
	assert.Len(t, list, 2, "a duplicate seq adds no second entry")

	// Past the cap, the rest are counted.
	var more []daemon.SessionEvent
	for seq := int64(5); seq < 5+200; seq++ {
		more = append(more, event(seq, audit.KindStatement, map[string]string{analyzer.MetadataRiskLevel: "low"}))
	}
	_, err = services.ApplySidecarSessionEvents(models.DB, sc, more)
	require.NoError(t, err)
	list, dropped = decisions()
	assert.Len(t, list, 200)
	assert.EqualValues(t, 2, dropped)
	last, _ := list[199].(map[string]any)
	assert.EqualValues(t, 202, last["seq"], "the first 200 are kept")
}

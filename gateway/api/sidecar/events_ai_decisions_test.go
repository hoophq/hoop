package apisidecar

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const eventsAIDecisionsOrgID = "00000000-0000-0000-0000-0000000000ed"

// A token holder controls every metadata value. Oversized fields across
// several batches must not grow metadata.sidecar.ai_decisions past its byte
// ceiling, and the entries it leaves out are counted.
func TestPostEventsBoundsTheAIDecisions(t *testing.T) {
	startEventsDB(t, eventsAIDecisionsOrgID)
	enableSessionEvents(t, eventsAIDecisionsOrgID)
	sc := seedEventsSidecar(t, eventsAIDecisionsOrgID, "ai-decisions-edge")

	big := strings.Repeat("x", 50_000)
	reason := strings.Repeat("é", 5000)
	seq := int64(1)
	const batches, perBatch = 4, 10
	for b := range batches {
		var events []daemon.SessionEvent
		if b == 0 {
			events = append(events, sessionEvent(seq, "s-big", 0, audit.KindSessionStart, nil))
			seq++
		}
		for range perBatch {
			events = append(events, sessionEvent(seq, "s-big", int(seq), audit.KindStatement, func(e *audit.Event) {
				e.Statement = "SELECT 1"
				e.Metadata = map[string]string{
					analyzer.MetadataRiskLevel: "high", analyzer.MetadataAction: big,
					analyzer.MetadataHandoffDecision: big, analyzer.MetadataApprover: big,
					analyzer.MetadataApprovalDecision: big, analyzer.MetadataReviewID: big,
					analyzer.MetadataHandoffReason: reason, analyzer.MetadataApprovalReason: reason,
				}
			}))
			seq++
		}
		rec := postEvents(sc, eventsBody(t, events...))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	}

	id := services.SidecarSessionID(sc.ID, "s-big")
	var stored struct {
		Bytes   int
		Count   int
		Dropped int
	}
	require.NoError(t, models.DB.Raw(`SELECT octet_length((metadata->'sidecar'->'ai_decisions')::text) AS bytes,
		jsonb_array_length(metadata->'sidecar'->'ai_decisions') AS count,
		(metadata->'sidecar'->>'ai_decisions_dropped')::int AS dropped
		FROM private.sessions WHERE org_id = ? AND id = ?`, eventsAIDecisionsOrgID, id).Scan(&stored).Error)
	assert.LessOrEqual(t, stored.Bytes, 256<<10, "the list stays under its byte ceiling")
	assert.Positive(t, stored.Count)
	assert.Less(t, stored.Count, batches*perBatch)
	assert.Equal(t, batches*perBatch, stored.Count+stored.Dropped, "every rated statement is kept or counted")

	sidecar := sidecarMetadata(t, getSidecarSessionRow(t, eventsAIDecisionsOrgID, id))
	list, ok := sidecar["ai_decisions"].([]any)
	require.True(t, ok)
	for _, raw := range list {
		entry, _ := raw.(map[string]any)
		keys := make([]string, 0, len(entry))
		for k := range entry {
			keys = append(keys, k)
		}
		assert.ElementsMatch(t, []string{"seq", "timestamp", "risk_level", "handoff_reason", "approval_reason"}, keys,
			"no unknown vocabulary value is stored")
		assert.Len(t, []rune(entry["handoff_reason"].(string)), 4096)
	}

	// A resend of the last batch changes nothing.
	before, err := json.Marshal(list)
	require.NoError(t, err)
	var last []daemon.SessionEvent
	for s := seq - perBatch; s < seq; s++ {
		last = append(last, sessionEvent(s, "s-big", int(s), audit.KindStatement, func(e *audit.Event) {
			e.Statement = "SELECT 1"
			e.Metadata = map[string]string{analyzer.MetadataRiskLevel: "high"}
		}))
	}
	rec := postEvents(sc, eventsBody(t, last...))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	after, err := json.Marshal(sidecarMetadata(t, getSidecarSessionRow(t, eventsAIDecisionsOrgID, id))["ai_decisions"])
	require.NoError(t, err)
	assert.JSONEq(t, string(before), string(after))
}

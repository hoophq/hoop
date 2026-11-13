package models_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLastUsedGate(t *testing.T) {
	models.ResetLastUsedGates()
	t.Cleanup(models.ResetLastUsedGates)
	now := time.Now()

	assert.True(t, models.LastUsedGateAllow("a", now))
	assert.False(t, models.LastUsedGateAllow("a", now.Add(models.LastUsedInterval-time.Nanosecond)))
	assert.True(t, models.LastUsedGateAllow("b", now))
	assert.True(t, models.LastUsedGateAllow("a", now.Add(models.LastUsedInterval)))
}

func readLastUsed(t *testing.T, table, id string) *time.Time {
	t.Helper()
	var out struct {
		LastUsedAt *time.Time `gorm:"column:last_used_at"`
	}
	require.NoError(t, models.DB.Raw(`SELECT last_used_at FROM `+table+` WHERE id = ?`, id).Scan(&out).Error)
	return out.LastUsedAt
}

func setLastUsed(t *testing.T, table, id string, at time.Time) {
	t.Helper()
	require.NoError(t, models.DB.Exec(`UPDATE `+table+` SET last_used_at = ? WHERE id = ?`, at, id).Error)
}

func TestUpdateLastUsedWritesOncePerInterval(t *testing.T) {
	startTestDB(t)

	apiKey := &models.APIKey{OrgID: testOrgID, Name: "tf", KeyHash: "h1", MaskedKey: "hpk_****", Status: "active", CreatedBy: "test"}
	require.NoError(t, models.CreateAPIKey(apiKey))
	aiAgent := &models.AIAgent{OrgID: testOrgID, Name: "bot", KeyHash: "h2", MaskedKey: "hpa_****", Status: "active", CreatedBy: "test"}
	require.NoError(t, models.CreateAIAgent(aiAgent))

	for _, tc := range []struct {
		name, table, id string
		update          func(string)
	}{
		{"api key", "private.api_keys", apiKey.ID, models.UpdateAPIKeyLastUsed},
		{"ai agent", "private.ai_agents", aiAgent.ID, models.UpdateAIAgentLastUsed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			models.ResetLastUsedGates()
			t.Cleanup(models.ResetLastUsedGates)

			tc.update(tc.id)
			first := readLastUsed(t, tc.table, tc.id)
			require.NotNil(t, first)
			assert.WithinDuration(t, time.Now(), *first, 5*time.Second)

			// Same replica, inside the interval: no statement reaches the row.
			stale := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
			setLastUsed(t, tc.table, tc.id, stale)
			for range 100 {
				tc.update(tc.id)
			}
			assert.True(t, stale.Equal(*readLastUsed(t, tc.table, tc.id)))

			// Another replica, inside the interval: the WHERE clause skips the row.
			recent := time.Now().UTC().Add(-models.LastUsedInterval / 2).Truncate(time.Microsecond)
			setLastUsed(t, tc.table, tc.id, recent)
			models.ResetLastUsedGates()
			tc.update(tc.id)
			assert.True(t, recent.Equal(*readLastUsed(t, tc.table, tc.id)))

			// Another replica, after the interval: the row is written.
			setLastUsed(t, tc.table, tc.id, stale)
			models.ResetLastUsedGates()
			tc.update(tc.id)
			assert.WithinDuration(t, time.Now(), *readLastUsed(t, tc.table, tc.id), 5*time.Second)
		})
	}

	// An unknown id is not an error and writes nothing.
	models.UpdateAPIKeyLastUsed(uuid.NewString())
}

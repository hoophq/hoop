package models_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
)

func TestPersistRDPGuardrailViolationIsAtomicAndIdempotent(t *testing.T) {
	startTestDB(t)

	sessionID := uuid.NewString()
	if err := models.DB.Exec(`
		INSERT INTO private.sessions
			(id, org_id, connection, connection_type, verb, status)
		VALUES (?, ?, 'rdp-guard-test', 'custom', 'connect', 'open')`,
		sessionID, testOrgID,
	).Error; err != nil {
		t.Fatalf("seed session: %v", err)
	}

	reportID := uuid.NewString()
	info, err := json.Marshal([]models.SessionGuardRailsInfo{{
		ReportID: reportID,
		RuleName: "rdp_pii_guard",
		Rule: models.SessionGuardRailMatchedRule{
			Type: "pii_detection",
		},
		Direction:    "server_to_client",
		MatchedWords: []string{"PERSON"},
	}})
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	detections := []models.RDPEntityDetection{{
		SessionID:  sessionID,
		EntityType: "PERSON",
		Score:      0.95,
		X:          10,
		Y:          20,
		Width:      30,
		Height:     40,
	}}

	for attempt := 0; attempt < 2; attempt++ {
		if err := models.PersistRDPGuardrailViolation(
			context.Background(),
			testOrgID,
			sessionID,
			reportID,
			info,
			detections,
		); err != nil {
			t.Fatalf("persist attempt %d: %v", attempt+1, err)
		}
	}

	var reports int64
	if err := models.DB.Raw(`
		SELECT jsonb_array_length(COALESCE(guardrails_info, '[]'::jsonb))
		FROM private.sessions
		WHERE org_id = ? AND id = ?`, testOrgID, sessionID).Scan(&reports).Error; err != nil {
		t.Fatalf("count reports: %v", err)
	}
	if reports != 1 {
		t.Fatalf("guardrail reports=%d, want 1", reports)
	}

	var detectionCount int64
	if err := models.DB.Table("private.rdp_entity_detections").
		Where("session_id = ?", sessionID).
		Count(&detectionCount).Error; err != nil {
		t.Fatalf("count detections: %v", err)
	}
	if detectionCount != 1 {
		t.Fatalf("detections=%d, want 1", detectionCount)
	}
}

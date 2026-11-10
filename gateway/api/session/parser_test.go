package sessionapi

import (
	"encoding/json"
	"testing"

	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/models"
)

func TestSessionRecordingFormatInAPI(t *testing.T) {
	old := &models.Session{
		ConnectionType: "custom", ConnectionSubtype: "kubernetes",
		Verb: proto.ClientVerbConnect,
	}
	oldJSON, err := json.Marshal(toOpenApiSession(old, false))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(oldJSON, &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["recording_format"]; ok {
		t.Fatalf("historical session acquired a format from today's resolver: %s", oldJSON)
	}

	format := proto.RecordingFormatRaw
	current := &models.Session{
		ConnectionType: "custom", ConnectionSubtype: "kubernetes",
		Verb: proto.ClientVerbConnect, RecordingFormat: &format,
	}
	currentJSON, err := json.Marshal(toOpenApiSession(current, false))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(currentJSON, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["recording_format"] != "raw" {
		t.Fatalf("current session recording_format = %v, want raw", payload["recording_format"])
	}
}

// NB12: an older review, or one not run yet, carries no executor and the
// response has no executed_by key; a run review names its executor.
func TestReviewExecutedByField(t *testing.T) {
	encode := func(r *models.SessionReview) map[string]any {
		t.Helper()
		raw, err := json.Marshal(topOpenApiReview(r))
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if _, ok := encode(&models.SessionReview{ID: "r1", Status: "APPROVED"})["executed_by"]; ok {
		t.Error("a review with no executor must not send executed_by")
	}
	executor := "admin@example.com"
	if got := encode(&models.SessionReview{ID: "r2", Status: "EXECUTED", ExecutedBy: &executor})["executed_by"]; got != executor {
		t.Errorf("executed_by = %v, want %s", got, executor)
	}
}

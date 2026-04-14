package cmd

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestEmitJSONEvent(t *testing.T) {
	var buf bytes.Buffer
	emitJSONEvent(&buf, JSONEvent{
		Status:  "waiting_approval",
		Message: "waiting command to be approved",
		Data: map[string]string{
			"review_url": "https://app.hoop.dev/reviews/abc-123",
			"session_id": "sid-456",
		},
	})

	var got JSONEvent
	if err := json.NewDecoder(&buf).Decode(&got); err != nil {
		t.Fatalf("failed to decode JSON event: %v", err)
	}
	if got.Status != "waiting_approval" {
		t.Errorf("expected status 'waiting_approval', got %q", got.Status)
	}
	if got.Data["review_url"] != "https://app.hoop.dev/reviews/abc-123" {
		t.Errorf("expected review_url in data, got %v", got.Data)
	}
	if got.Data["session_id"] != "sid-456" {
		t.Errorf("expected session_id in data, got %v", got.Data)
	}
}

func TestEmitJSONEventExitCode(t *testing.T) {
	var buf bytes.Buffer
	exitCode := 1
	emitJSONEvent(&buf, JSONEvent{
		Status:   "completed",
		Message:  "session closed",
		ExitCode: &exitCode,
	})

	var got JSONEvent
	if err := json.NewDecoder(&buf).Decode(&got); err != nil {
		t.Fatalf("failed to decode JSON event: %v", err)
	}
	if got.ExitCode == nil || *got.ExitCode != 1 {
		t.Errorf("expected exit_code 1, got %v", got.ExitCode)
	}
}

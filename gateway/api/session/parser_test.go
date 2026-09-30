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

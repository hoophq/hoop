package wire

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/url"
	"strings"
	"testing"
)

// The envelope is the one artifact all four component workstreams build
// against, so these tests pin the properties they are each allowed to assume.

func TestNewSetsVersionAndEncodesPayload(t *testing.T) {
	e, err := New("msg-1", TypeConfigApply, ConfigApplyPayload{
		Generation: 7,
		Config:     json.RawMessage(`{"listen":":5432"}`),
	})
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	if e.V != Version {
		t.Errorf("V = %d, want %d", e.V, Version)
	}
	if e.Type != TypeConfigApply {
		t.Errorf("Type = %q, want %q", e.Type, TypeConfigApply)
	}
	if e.Re != "" {
		t.Errorf("Re = %q, want empty on a non-reply", e.Re)
	}

	var got ConfigApplyPayload
	if err := DecodePayload(e, &got); err != nil {
		t.Fatalf("DecodePayload returned an error: %v", err)
	}
	if got.Generation != 7 {
		t.Errorf("Generation = %d, want 7", got.Generation)
	}
}

// Generation must survive the round trip as a sibling of config, never nested
// inside it. The sidecar parses the config document with
// DisallowUnknownFields, so a generation key that leaked into the document
// would fail every push. This test is what makes that regression visible.
func TestConfigApplyKeepsGenerationOutsideTheConfigDocument(t *testing.T) {
	doc := json.RawMessage(`{"listen":":5432"}`)
	e, err := New("msg-1", TypeConfigApply, ConfigApplyPayload{Generation: 42, Config: doc})
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}

	var payload struct {
		Generation int64           `json:"generation"`
		Config     json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(e.Payload, &payload); err != nil {
		t.Fatalf("payload did not unmarshal: %v", err)
	}
	if payload.Generation != 42 {
		t.Errorf("generation = %d, want 42", payload.Generation)
	}

	var inner map[string]any
	if err := json.Unmarshal(payload.Config, &inner); err != nil {
		t.Fatalf("config did not unmarshal: %v", err)
	}
	if _, found := inner["generation"]; found {
		t.Error("config document carries a generation key; the sidecar parses with DisallowUnknownFields and would reject the whole document")
	}
}

func TestReplyLinksToTheMessageItAnswers(t *testing.T) {
	apply, err := New("apply-1", TypeConfigApply, ConfigApplyPayload{Generation: 3})
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	nack, err := Reply("nack-1", TypeConfigNack, apply.ID, ConfigNackPayload{
		Generation:        3,
		RunningGeneration: 2,
		Reason:            "unknown rule kind",
	})
	if err != nil {
		t.Fatalf("Reply returned an error: %v", err)
	}
	if nack.Re != apply.ID {
		t.Errorf("Re = %q, want %q", nack.Re, apply.ID)
	}
}

// An envelope carrying a type this build does not know must still decode, so
// the receiver can answer TypeUnsupported. Failing at the parse step instead
// would make version skew fatal, and customers never upgrade a fleet in
// lockstep.
func TestUnknownTypeStillDecodes(t *testing.T) {
	e, err := Decode([]byte(`{"v":1,"type":"approval.request","id":"x-1","payload":{"anything":true}}`))
	if err != nil {
		t.Fatalf("an unknown type failed to decode: %v", err)
	}
	if e.Type != TypeApprovalRequest {
		t.Errorf("Type = %q, want %q", e.Type, TypeApprovalRequest)
	}

	answer, err := Reply("y-1", TypeUnsupported, e.ID, UnsupportedPayload{Type: e.Type})
	if err != nil {
		t.Fatalf("Reply returned an error: %v", err)
	}
	if answer.Re != "x-1" {
		t.Errorf("Re = %q, want %q", answer.Re, "x-1")
	}
}

// Re is what makes a NACK attributable to the config that caused it. A reply
// built without one is not a reply, and it must not be possible to make one
// by accident.
func TestReplyRefusesAnEmptyRe(t *testing.T) {
	if _, err := Reply("nack-1", TypeConfigNack, "", ConfigNackPayload{}); err == nil {
		t.Error("Reply accepted an empty re")
	}
}

func TestDecodeRejectsMalformedEnvelopes(t *testing.T) {
	cases := map[string]string{
		"wrong version": `{"v":2,"type":"status","id":"s-1"}`,
		"no version":    `{"type":"status","id":"s-1"}`,
		"no type":       `{"v":1,"id":"s-1"}`,
		"no id":         `{"v":1,"type":"status"}`,
		"not json":      `{`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode([]byte(raw)); err == nil {
				t.Errorf("Decode accepted %s", raw)
			}
		})
	}
}

// The credential must not reach a log record even when a caller hands slog
// the whole payload.
func TestHelloPayloadRedactsTheCredential(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.Info("hello", "payload", HelloPayload{
		Name:       "pg-prod",
		Credential: "super-secret-token",
		Version:    "1.2.3",
		Generation: 4,
	})

	line := buf.String()
	if strings.Contains(line, "super-secret-token") {
		t.Errorf("credential reached the log record: %s", line)
	}
	if !strings.Contains(line, "pg-prod") {
		t.Errorf("redaction dropped the fields worth logging: %s", line)
	}
}

func TestNewIDIsUniqueAndURLSafe(t *testing.T) {
	seen := make(map[string]bool, 512)
	for range 512 {
		id := NewID()
		if id == "" {
			t.Fatal("NewID returned an empty string")
		}
		if seen[id] {
			t.Fatalf("NewID repeated %q", id)
		}
		seen[id] = true
		if url.QueryEscape(id) != id {
			t.Errorf("NewID produced %q, which does not survive a URL unescaped", id)
		}
	}
}

// A newer peer sending a field this build has never heard of is an upgrade,
// not an error. This is the deliberate opposite of how the sidecar parses a
// config document, and the asymmetry is easy to "fix" by mistake.
func TestDecodePayloadIgnoresUnknownFields(t *testing.T) {
	e := &Envelope{
		V:       Version,
		Type:    TypeStatus,
		ID:      "s-1",
		Payload: json.RawMessage(`{"generation":9,"uptime_seconds":120,"field_from_the_future":"x"}`),
	}
	var got StatusPayload
	if err := DecodePayload(e, &got); err != nil {
		t.Fatalf("DecodePayload rejected an unknown field: %v", err)
	}
	if got.Generation != 9 {
		t.Errorf("Generation = %d, want 9", got.Generation)
	}
}

func TestDecodePayloadRejectsEmptyInput(t *testing.T) {
	if err := DecodePayload(nil, &StatusPayload{}); err == nil {
		t.Error("decoding a nil envelope returned no error")
	}
	if err := DecodePayload(&Envelope{Type: TypeStatus}, &StatusPayload{}); err == nil {
		t.Error("decoding an empty payload returned no error")
	}
}

func TestNilPayloadIsOmittedNotNull(t *testing.T) {
	e, err := New("m-1", TypeStatus, nil)
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	encoded, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal returned an error: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("Unmarshal returned an error: %v", err)
	}
	if _, found := fields["payload"]; found {
		t.Errorf("payload present on a bodyless message: %s", encoded)
	}
}

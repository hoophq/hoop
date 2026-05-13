package events

import "sort"

type EventType struct {
	Name          string
	Category      string
	Description   string
	Schema        []SchemaField
	SamplePayload map[string]any
}

type SchemaField struct {
	Name     string
	Type     string
	Required bool
}

var Catalog = map[string]EventType{
	"ai.sensitive_data_detected": {
		Name:        "ai.sensitive_data_detected",
		Category:    "AI",
		Description: "Fires when an AI guardrail detects PII or regulated data in a prompt sent to a model.",
		Schema: []SchemaField{
			{Name: "user", Type: "string(email)", Required: true},
			{Name: "model", Type: "string", Required: true},
			{Name: "detector", Type: "string", Required: true},
			{Name: "categories", Type: "string[]", Required: true},
			{Name: "prompt_excerpt", Type: "string", Required: false},
			{Name: "occurred_at", Type: "string(ISO 8601)", Required: true},
		},
		SamplePayload: map[string]any{
			"user":           "alex.morgan@acme.com",
			"model":          "claude-opus-4-7",
			"detector":       "presidio.v2",
			"categories":     []string{"EMAIL", "US_SSN"},
			"prompt_excerpt": "Please redact for ********",
			"occurred_at":    "2026-05-04T14:22:08Z",
		},
	},
	"ai.prompt_blocked": {
		Name:        "ai.prompt_blocked",
		Category:    "AI",
		Description: "An outbound AI prompt was blocked by an active guardrail before reaching the model.",
		Schema: []SchemaField{
			{Name: "user", Type: "string(email)", Required: true},
			{Name: "model", Type: "string", Required: true},
			{Name: "guardrail_name", Type: "string", Required: true},
			{Name: "reason", Type: "string", Required: true},
			{Name: "occurred_at", Type: "string(ISO 8601)", Required: true},
		},
		SamplePayload: map[string]any{
			"user":           "drew.k@acme.com",
			"model":          "gpt-5.5",
			"guardrail_name": "pii-blocker",
			"reason":         "prompt contains SSN",
			"occurred_at":    "2026-05-04T15:10:33Z",
		},
	},
	"alert.pii_detected": {
		Name:        "alert.pii_detected",
		Category:    "Alert",
		Description: "PII was detected in session output streamed to a user terminal.",
		Schema: []SchemaField{
			{Name: "session_id", Type: "string", Required: true},
			{Name: "user", Type: "string(email)", Required: true},
			{Name: "connection", Type: "string", Required: true},
			{Name: "types_detected", Type: "string[]", Required: true},
			{Name: "total_detections", Type: "int", Required: true},
			{Name: "occurred_at", Type: "string(ISO 8601)", Required: true},
		},
		SamplePayload: map[string]any{
			"session_id":       "ses_01HX9A2BCD",
			"user":             "alex.morgan@acme.com",
			"connection":       "conn-prod-pg",
			"types_detected":   []string{"EMAIL", "PHONE_NUMBER"},
			"total_detections": 3,
			"occurred_at":      "2026-05-04T14:30:00Z",
		},
	},
	"alert.policy_violation": {
		Name:        "alert.policy_violation",
		Category:    "Alert",
		Description: "A configured access policy was violated by a user.",
		Schema: []SchemaField{
			{Name: "user", Type: "string(email)", Required: true},
			{Name: "policy_name", Type: "string", Required: true},
			{Name: "connection", Type: "string", Required: false},
			{Name: "action", Type: "string", Required: true},
			{Name: "occurred_at", Type: "string(ISO 8601)", Required: true},
		},
		SamplePayload: map[string]any{
			"user":        "drew.k@acme.com",
			"policy_name": "no-prod-access-weekends",
			"connection":  "conn-prod-pg",
			"action":      "connect",
			"occurred_at": "2026-05-04T22:05:17Z",
		},
	},
	"access.jit_request_anomalous": {
		Name:        "access.jit_request_anomalous",
		Category:    "Access",
		Description: "A Just-in-Time access request was flagged as anomalous by location, time, or pattern.",
		Schema: []SchemaField{
			{Name: "user", Type: "string(email)", Required: true},
			{Name: "connection", Type: "string", Required: true},
			{Name: "reason", Type: "string", Required: true},
			{Name: "occurred_at", Type: "string(ISO 8601)", Required: true},
		},
		SamplePayload: map[string]any{
			"user":        "alex.morgan@acme.com",
			"connection":  "conn-prod-pg",
			"reason":      "first-time access from unusual geo",
			"occurred_at": "2026-05-04T03:12:44Z",
		},
	},
	"access.jit_approved": {
		Name:        "access.jit_approved",
		Category:    "Access",
		Description: "A JIT access request was approved by a reviewer.",
		Schema: []SchemaField{
			{Name: "session_id", Type: "string", Required: true},
			{Name: "user", Type: "string(email)", Required: true},
			{Name: "connection", Type: "string", Required: true},
			{Name: "reviewer", Type: "string(email)", Required: true},
			{Name: "review_id", Type: "string", Required: true},
			{Name: "occurred_at", Type: "string(ISO 8601)", Required: true},
		},
		SamplePayload: map[string]any{
			"session_id":  "ses_01HX9C1EFG",
			"user":        "drew.k@acme.com",
			"connection":  "conn-prod-pg",
			"reviewer":    "alex.morgan@acme.com",
			"review_id":   "rev_01HX9C1EFH",
			"occurred_at": "2026-05-04T14:45:00Z",
		},
	},
	"access.jit_denied": {
		Name:        "access.jit_denied",
		Category:    "Access",
		Description: "A JIT access request was denied by a reviewer or auto-policy.",
		Schema: []SchemaField{
			{Name: "session_id", Type: "string", Required: true},
			{Name: "user", Type: "string(email)", Required: true},
			{Name: "connection", Type: "string", Required: true},
			{Name: "reviewer", Type: "string(email)", Required: true},
			{Name: "review_id", Type: "string", Required: true},
			{Name: "reason", Type: "string", Required: false},
			{Name: "occurred_at", Type: "string(ISO 8601)", Required: true},
		},
		SamplePayload: map[string]any{
			"session_id":  "ses_01HX9C2IJK",
			"user":        "drew.k@acme.com",
			"connection":  "conn-prod-pg",
			"reviewer":    "alex.morgan@acme.com",
			"review_id":   "rev_01HX9C2IJL",
			"reason":      "not authorized for production",
			"occurred_at": "2026-05-04T14:50:00Z",
		},
	},
	"session.guardrail_violation": {
		Name:        "session.guardrail_violation",
		Category:    "Session",
		Description: "A session triggered a configured query or command guardrail.",
		Schema: []SchemaField{
			{Name: "session_id", Type: "string", Required: true},
			{Name: "user", Type: "string(email)", Required: true},
			{Name: "connection", Type: "string", Required: true},
			{Name: "rule", Type: "string", Required: true},
			{Name: "query_excerpt", Type: "string", Required: false},
			{Name: "occurred_at", Type: "string(ISO 8601)", Required: true},
		},
		SamplePayload: map[string]any{
			"session_id":    "ses_01HX9DJ3KE",
			"user":          "drew.k@acme.com",
			"connection":    "conn-prod-pg",
			"rule":          "block-truncate-prod",
			"query_excerpt": "TRUNCATE TABLE customers",
			"occurred_at":   "2026-05-04T14:55:01Z",
		},
	},
	"session.pci_scope_entered": {
		Name:        "session.pci_scope_entered",
		Category:    "Session",
		Description: "A session began touching a resource tagged as in PCI scope.",
		Schema: []SchemaField{
			{Name: "session_id", Type: "string", Required: true},
			{Name: "user", Type: "string(email)", Required: true},
			{Name: "connection", Type: "string", Required: true},
			{Name: "tags", Type: "string[]", Required: true},
			{Name: "occurred_at", Type: "string(ISO 8601)", Required: true},
		},
		SamplePayload: map[string]any{
			"session_id":  "ses_01HX9E4LMN",
			"user":        "alex.morgan@acme.com",
			"connection":  "conn-pci-db",
			"tags":        []string{"pci-dss", "cardholder-data"},
			"occurred_at": "2026-05-04T15:00:00Z",
		},
	},
	"session.anomaly_detected": {
		Name:        "session.anomaly_detected",
		Category:    "Session",
		Description: "Statistical anomaly detected in session activity (volume, query shape, exfil risk).",
		Schema: []SchemaField{
			{Name: "session_id", Type: "string", Required: true},
			{Name: "user", Type: "string(email)", Required: true},
			{Name: "connection", Type: "string", Required: true},
			{Name: "risk_level", Type: "string", Required: true},
			{Name: "reason", Type: "string", Required: true},
			{Name: "occurred_at", Type: "string(ISO 8601)", Required: true},
		},
		SamplePayload: map[string]any{
			"session_id":  "ses_01HX9F5OPQ",
			"user":        "drew.k@acme.com",
			"connection":  "conn-prod-pg",
			"risk_level":  "high",
			"reason":      "query volume 10x above baseline",
			"occurred_at": "2026-05-04T15:10:00Z",
		},
	},
	"connection.health_degraded": {
		Name:        "connection.health_degraded",
		Category:    "Connection",
		Description: "Connection health crossed the degraded threshold.",
		Schema: []SchemaField{
			{Name: "connection", Type: "string", Required: true},
			{Name: "agent_id", Type: "string", Required: true},
			{Name: "status", Type: "string", Required: true},
			{Name: "reason", Type: "string", Required: false},
			{Name: "occurred_at", Type: "string(ISO 8601)", Required: true},
		},
		SamplePayload: map[string]any{
			"connection":  "conn-prod-pg",
			"agent_id":    "agent-us-east-1",
			"status":      "degraded",
			"reason":      "latency > 500ms for 5 min",
			"occurred_at": "2026-05-04T15:20:00Z",
		},
	},
	"session.started": {
		Name:        "session.started",
		Category:    "Session",
		Description: "A new session was opened.",
		Schema: []SchemaField{
			{Name: "session_id", Type: "string", Required: true},
			{Name: "user", Type: "string(email)", Required: true},
			{Name: "connection", Type: "string", Required: true},
			{Name: "verb", Type: "string", Required: true},
			{Name: "occurred_at", Type: "string(ISO 8601)", Required: true},
		},
		SamplePayload: map[string]any{
			"session_id":  "ses_01HX9G6RST",
			"user":        "alex.morgan@acme.com",
			"connection":  "conn-prod-pg",
			"verb":        "exec",
			"occurred_at": "2026-05-04T15:30:00Z",
		},
	},
	"session.closed": {
		Name:        "session.closed",
		Category:    "Session",
		Description: "A session was closed.",
		Schema: []SchemaField{
			{Name: "session_id", Type: "string", Required: true},
			{Name: "user", Type: "string(email)", Required: true},
			{Name: "connection", Type: "string", Required: true},
			{Name: "exit_code", Type: "string", Required: true},
			{Name: "duration_ms", Type: "int", Required: true},
			{Name: "occurred_at", Type: "string(ISO 8601)", Required: true},
		},
		SamplePayload: map[string]any{
			"session_id":  "ses_01HX9G6RST",
			"user":        "alex.morgan@acme.com",
			"connection":  "conn-prod-pg",
			"exit_code":   "0",
			"duration_ms": 45200,
			"occurred_at": "2026-05-04T15:30:45Z",
		},
	},
}

// SchemaFieldNames returns the set of root field names for a given event type.
func (et EventType) SchemaFieldNames() map[string]bool {
	fields := make(map[string]bool, len(et.Schema))
	for _, f := range et.Schema {
		fields[f.Name] = true
	}
	return fields
}

func Categories() []string {
	seen := make(map[string]bool)
	for _, et := range Catalog {
		seen[et.Category] = true
	}
	cats := make([]string, 0, len(seen))
	for c := range seen {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	return cats
}

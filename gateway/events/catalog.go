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
	"alert.sensitive_data_detected": {
		Name:     "alert.sensitive_data_detected",
		Category: "Alert",
		Description: "Fires at session close when the DLP analyzer flagged one or more sensitive " +
			"entities in session output. Detection is heuristic and does not guarantee a redaction " +
			"was applied — see `alert.data_masked` for that.",
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
			"types_detected":   []string{"EMAIL_ADDRESS", "PHONE_NUMBER", "PERSON"},
			"total_detections": 12,
			"occurred_at":      "2026-05-04T14:30:00Z",
		},
	},
	"alert.data_masked": {
		Name:     "alert.data_masked",
		Category: "Alert",
		Description: "Fires at session close when the redactor replaced one or more sensitive " +
			"values in session output. Only emitted when at least one redaction was actually applied.",
		Schema: []SchemaField{
			{Name: "session_id", Type: "string", Required: true},
			{Name: "user", Type: "string(email)", Required: true},
			{Name: "connection", Type: "string", Required: true},
			{Name: "types_masked", Type: "string[]", Required: true},
			{Name: "total_redactions", Type: "int", Required: true},
			{Name: "occurred_at", Type: "string(ISO 8601)", Required: true},
		},
		SamplePayload: map[string]any{
			"session_id":       "ses_01HX9A2BCD",
			"user":             "alex.morgan@acme.com",
			"connection":       "conn-prod-pg",
			"types_masked":     []string{"EMAIL_ADDRESS", "US_BANK_NUMBER"},
			"total_redactions": 12,
			"occurred_at":      "2026-05-04T14:30:00Z",
		},
	},
	"access.jit_approved": {
		Name:     "access.jit_approved",
		Category: "Access",
		Description: "Fires when a review is approved via the API, Slack, or MCP. `reviewer` is " +
			"the email of the approving group; if several groups approve, the first one is reported.",
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
		Name:     "access.jit_denied",
		Category: "Access",
		Description: "Fires when a review is rejected via the API, Slack, or MCP. `reason` is the " +
			"free-form text supplied at decision time (empty when none was given). Does not fire " +
			"on auto-expiration or force-rejection.",
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
		Name:     "session.guardrail_violation",
		Category: "Session",
		Description: "Fires at session close, once per guardrail rule the session tripped. A " +
			"session that trips multiple rules emits multiple events. `query_excerpt` is a " +
			"best-effort preview of the matched words and may be empty for regex rules.",
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
		Name:     "session.pci_scope_entered",
		Category: "Session",
		Description: "Fires at session open when the target connection is tagged `pci` or " +
			"`pci-scope` (case-insensitive). Tag-driven, not content-driven — for content matches " +
			"on cardholder data, use `alert.sensitive_data_detected` with a `CREDIT_CARD` filter.",
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
		Name:     "session.anomaly_detected",
		Category: "Session",
		Description: "Fires at session close when the post-session AI analyzer rates the session " +
			"risk as `high`. Offline scoring, not real-time detection — does not fire when AI " +
			"analysis is disabled or the result was `low`/`medium`.",
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
	"session.started": {
		Name:     "session.started",
		Category: "Session",
		Description: "Fires once per session at open, before any input or output has flowed. " +
			"Emitted for every session verb (`exec`, `connect`, `run`); use `verb` to discriminate. " +
			"Open does not imply success or authorization — pair with `session.closed` for outcome.",
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
		Name:     "session.closed",
		Category: "Session",
		Description: "Fires once per session after the audit plugin finalizes the session row. " +
			"`exit_code` is empty for sessions that ended without one (early disconnect, stream " +
			"abort). `duration_ms` is 0 if no end timestamp was recorded.",
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

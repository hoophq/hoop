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
		Description: "Fires at session close when the DLP analyzer has flagged one or more " +
			"sensitive data entities in session output (e.g. EMAIL_ADDRESS, PERSON, PHONE_NUMBER, " +
			"LOCATION, US_BANK_NUMBER). Detection is heuristic, based on confidence scores from the " +
			"configured DLP provider (Presidio or GCP DLP). A match does not guarantee the value is " +
			"regulated PII. The event fires whether or not the entity was subsequently masked; see " +
			"alert.data_masked for an event that signals an actual redaction was applied. It does " +
			"not fire when the analyzer reported zero entities, or when DLP analysis was disabled " +
			"for the connection.",
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
		Description: "Fires at session close when the redactor actually replaced one or more " +
			"sensitive values in session output. Distinct from alert.sensitive_data_detected: this " +
			"event only fires when at least one redaction was applied (DLP masking mode is enabled " +
			"and at least one entity met the redaction threshold), whereas sensitive_data_detected " +
			"fires whenever the analyzer flagged any entity regardless of whether it was masked. " +
			"Counts come from the per-info-type totals recorded in private.session_metrics by the " +
			"redactor pipeline.",
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
		Description: "Fires when a review transitions to status APPROVED via any supported path: " +
			"the HTTP API (PUT /api/reviews/:id), a Slack interactive approval, or the MCP " +
			"`reviews_update` tool. `reviewer` is the email recorded on the approving review group; " +
			"when several groups approve, the first one encountered is reported. `session_id` is " +
			"the session that originated the review request. Does not fire for revoke or " +
			"force-approval flows that bypass review transitions.",
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
		Description: "Fires when a review transitions to status REJECTED via the HTTP API, " +
			"Slack interactive rejection, or the MCP `reviews_update` tool. `reason` is the " +
			"free-form rejection reason supplied at decision time (empty when the caller did not " +
			"provide one). `reviewer` is the email recorded on the rejecting review group. Does " +
			"not fire on auto-expiration, revoke, or admin force-rejection that bypasses a " +
			"review group transition.",
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
		Description: "Fires at session close, once per guardrail rule recorded on the session. A " +
			"single session that trips multiple rules produces multiple events (each with the same " +
			"`session_id` but a different `rule`). This is a post-mortem signal. It is not emitted " +
			"at the moment the gateway blocks the input or output stream, only after the session " +
			"row is finalized. `query_excerpt` is a comma-joined preview of the matched words, " +
			"truncated at 256 characters; it is best-effort context, not a verbatim slice of the " +
			"original query, and may be empty for regex pattern-match rules where no matched words " +
			"were captured.",
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
		Description: "Fires at session open when the target connection carries a tag equal to " +
			"`pci` or `pci-scope` (case-insensitive). The `tags` field contains the full tag list " +
			"of the connection so subscribers can apply additional classification. This event is " +
			"tag-driven, not content-driven: it does not fire if a non-PCI-tagged connection " +
			"happens to return cardholder data. For that scenario, subscribe to " +
			"alert.sensitive_data_detected and filter for a `CREDIT_CARD` entity.",
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
			"risk as `high` (case-insensitive). `risk_level` and `reason` are passed through " +
			"verbatim from the analyzer output (session.ai_analysis.risk_level and .explanation). " +
			"This is offline scoring of a completed session, not real-time anomaly detection. " +
			"The event does not fire when AI analysis is disabled, when the analyzer call failed, " +
			"or when the result was rated `low` or `medium`.",
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
		Description: "Fires once per session immediately after the audit plugin persists the " +
			"SessionOpen row, before any user input or output has flowed. Emitted for every " +
			"session verb (`exec`, `connect`, `run`); use the `verb` field to discriminate. " +
			"Being opened does not imply the session was successful, or that the user was " +
			"authorized to read or write data. Pair with session.closed (and the access.jit_* " +
			"events when reviews are involved) for a complete picture.",
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
		Description: "Fires once per session after the audit plugin runs SessionClose and " +
			"finalizes the session row. `exit_code` is the agent-reported process exit code " +
			"serialized as a string and may be empty when the session ended without one " +
			"(e.g. early disconnect or stream abort). `duration_ms` is computed as " +
			"`end_session - created_at` in milliseconds and will be 0 if the gateway never " +
			"recorded a session end timestamp.",
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

package audit

import (
	"encoding/json"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// ResourceType identifies the kind of resource being audited.
type ResourceType string

const (
	ResourceDataMasking        ResourceType = "data_masking"
	ResourceGuardrails         ResourceType = "guardrails"
	ResourceUser               ResourceType = "users"
	ResourceUserGroup          ResourceType = "user_groups"
	ResourceServiceAccount     ResourceType = "service_accounts"
	ResourceConnection         ResourceType = "connections"
	ResourceResource           ResourceType = "resources"
	ResourceAgent              ResourceType = "agents"
	ResourceAuthConfig         ResourceType = "auth_config"
	ResourceServerConfig       ResourceType = "server_config"
	ResourceOrgKey             ResourceType = "org_keys"
	ResourceApiKey             ResourceType = "api_keys"
	ResourceAgentSPIFFEMapping ResourceType = "agent_spiffe_mappings"
	ResourceFeatureFlag        ResourceType = "feature_flags"
)

// Action is the operation performed.
type Action string

const (
	ActionCreate Action = "create"
	ActionUpdate Action = "update"
	ActionDelete Action = "delete"
	ActionRevoke Action = "revoke"
)

// outcome represents success or failure (stored as boolean in DB).
type outcome bool

const (
	outcomeSuccess outcome = true
	outcomeFailure outcome = false
)

// Event is a builder for a single audit log entry.
// Callers chain methods to set fields and call .Log(c) to write the event.
// Failures (errors or panics) are only logged; they never panic or stop the caller's flow.
//
// Deprecated: Use the AuditMiddleware instead. This API will be removed in a future release.
type Event struct {
	resourceType ResourceType
	action       Action
	resourceID   string
	resourceName string
	payload      map[string]any
	err          error
}

// NewEvent starts an audit event builder.
//
// Deprecated: Use the AuditMiddleware instead. This API will be removed in a future release.
func NewEvent(resourceType ResourceType, action Action) *Event {
	return &Event{
		resourceType: resourceType,
		action:       action,
		payload:      make(map[string]any),
	}
}

// Resource sets the resource ID and name.
func (e *Event) Resource(id, name string) *Event {
	e.resourceID = id
	e.resourceName = name
	return e
}

// Set adds a key-value pair to the audit payload.
func (e *Event) Set(key string, value any) *Event {
	e.payload[key] = value
	return e
}

// setMap merges all entries from m into the audit payload.
func (e *Event) setMap(m map[string]any) *Event {
	for k, v := range m {
		e.payload[k] = v
	}
	return e
}

// SetStruct merges exported fields of v into the audit payload via JSON round-trip.
// Fields with sensitive keys (password, secret, etc.) are redacted later by Redact.
func (e *Event) SetStruct(v any) *Event {
	data, err := json.Marshal(v)
	if err != nil {
		return e
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return e
	}
	return e.setMap(m)
}

// Err sets the error; if non-nil, outcome becomes failure and ErrorMessage is populated.
func (e *Event) Err(err error) *Event {
	e.err = err
	return e
}

// Log writes the event. Panics and errors are recovered/logged; the caller's flow is never interrupted.
//
// Deprecated: Use the AuditMiddleware instead. This API will be removed in a future release.
func (e *Event) Log(c *gin.Context) {
	o := outcomeSuccess
	errMsg := ""
	if e.err != nil {
		o = outcomeFailure
		errMsg = e.err.Error()
	}

	var payload map[string]any
	if len(e.payload) > 0 {
		payload = Redact(e.payload)
	}

	ctx := storagev2.ParseContext(c)
	row := &models.SecurityAuditLog{
		OrgID:                  ctx.OrgID,
		ActorSubject:           ctx.UserID,
		ActorEmail:             ctx.UserEmail,
		ActorName:              ctx.UserName,
		CreatedAt:              time.Now().UTC(),
		ResourceType:           string(e.resourceType),
		Action:                 string(e.action),
		HttpMethod:             c.Request.Method,
		HttpStatus:             200, // Default, actual status unknown in old API
		HttpPath:               c.Request.URL.Path,
		ClientIP:               c.ClientIP(),
		RequestPayloadRedacted: payload,
		Outcome:                bool(o),
		ErrorMessage:           errMsg,
	}
	if err := models.CreateSecurityAuditLog(row); err != nil {
		log.Errorf("security audit log write failed: %v", err)
	}
}

// LogFromMiddleware is the new API for recording audit logs from the middleware.
// It captures HTTP request/response details automatically and writes the audit log asynchronously.
func LogFromMiddleware(
	ctx *storagev2.Context,
	httpMethod string,
	httpStatus int,
	httpPath string,
	clientIP string,
	resourceType ResourceType,
	action Action,
	requestBody []byte,
	errorMessage string,
) {
	// Parse request body into map for redaction
	var payload map[string]any
	if len(requestBody) > 0 {
		if err := json.Unmarshal(requestBody, &payload); err != nil {
			log.Warnf("failed to unmarshal request body for audit log: %v", err)
			// Store as raw string if unmarshal fails
			payload = map[string]any{"_raw": string(requestBody)}
		}
	}

	// Redact sensitive fields
	if payload != nil {
		payload = Redact(payload)
	}

	// Determine outcome based on HTTP status
	outcome := httpStatus >= 200 && httpStatus < 400

	// Create audit log entry
	row := &models.SecurityAuditLog{
		OrgID:                  ctx.OrgID,
		ActorSubject:           ctx.UserID,
		ActorEmail:             ctx.UserEmail,
		ActorName:              ctx.UserName,
		CreatedAt:              time.Now().UTC(),
		ResourceType:           string(resourceType),
		Action:                 string(action),
		HttpMethod:             httpMethod,
		HttpStatus:             httpStatus,
		HttpPath:               httpPath,
		ClientIP:               clientIP,
		RequestPayloadRedacted: payload,
		Outcome:                outcome,
		ErrorMessage:           errorMessage,
	}

	// Write asynchronously to not block the request
	go func() {
		if err := models.CreateSecurityAuditLog(row); err != nil {
			log.Errorf("security audit log write failed: %v", err)
		}
	}()
}

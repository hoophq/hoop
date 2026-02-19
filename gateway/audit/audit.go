package audit

import (
	"encoding/json"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"gorm.io/gorm"
)

// ResourceType identifies the kind of resource being audited.
type ResourceType string

const (
	ResourceDataMasking    ResourceType = "data_masking"
	ResourceGuardrails     ResourceType = "guardrails"
	ResourceUser           ResourceType = "users"
	ResourceUserGroup      ResourceType = "user_groups"
	ResourceServiceAccount ResourceType = "service_accounts"
	ResourceConnection     ResourceType = "connections"
	ResourceResource       ResourceType = "resources"
	ResourceAgent          ResourceType = "agents"
	ResourceAuthConfig     ResourceType = "auth_config"
	ResourceServerConfig   ResourceType = "server_config"
	ResourceOrgKey         ResourceType = "org_keys"
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
// Callers chain methods to set fields and call .Log(db, c) to write the event.
type Event struct {
	resourceType ResourceType
	action       Action
	resourceID   string
	resourceName string
	payload      map[string]any
	err          error
}

// NewEvent starts an audit event builder.
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

// Write inserts the audit event using the provided *gorm.DB handle and returns
// any error. Use this inside a transaction so the audit row participates in the
// same commit/rollback as the business operation.
func (e *Event) Write(db *gorm.DB, c *gin.Context) error {
	return e.buildAndInsert(db, c)
}

// Log is a fire-and-forget wrapper around Write. It logs errors but never
// returns them. Use this for standalone failure events outside a transaction.
func (e *Event) Log(db *gorm.DB, c *gin.Context) {
	if err := e.buildAndInsert(db, c); err != nil {
		log.Errorf("security audit log write failed: %v", err)
	}
}

func (e *Event) buildAndInsert(db *gorm.DB, c *gin.Context) error {
	o := outcomeSuccess
	errMsg := ""
	if e.err != nil {
		o = outcomeFailure
		errMsg = e.err.Error()
	}

	var resourceID *uuid.UUID
	if e.resourceID != "" {
		if u, err := uuid.Parse(e.resourceID); err == nil {
			resourceID = &u
		}
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
		ResourceType:           string(e.resourceType),
		Action:                 string(e.action),
		ResourceID:             resourceID,
		ResourceName:           e.resourceName,
		RequestPayloadRedacted: payload,
		Outcome:                bool(o),
		ErrorMessage:           errMsg,
	}
	return models.CreateSecurityAuditLog(db, row)
}

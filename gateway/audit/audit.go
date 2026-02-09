package audit

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
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
	ResourceAgent          ResourceType = "agents"
	ResourceAuthConfig     ResourceType = "auth_config"
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

// Outcome represents success or failure (stored as boolean in DB).
type Outcome bool

const (
	OutcomeSuccess Outcome = true
	OutcomeFailure Outcome = false
)

// PayloadFn returns the raw request/context payload for an event. Implement per event;
// the result is redacted before storage. May be nil to store no payload.
type PayloadFn func() map[string]any

// LogParams is the minimal, reusable input for every audit event.
// Who and When are filled from context and time; pass only what changes per call.
// RequestPayloadFn is the per-event function that returns the payload to store (redacted).
type LogParams struct {
	ResourceType    ResourceType
	Action          Action
	Outcome         Outcome
	ResourceID      string
	ResourceName    string
	ErrorMessage    string
	RequestPayloadFn PayloadFn
}

// Logger writes audit events to storage.
type Logger interface {
	Log(ctx context.Context, p LogParams, actorSubject, actorEmail, actorName, orgID string) error
}

type defaultLogger struct{}

func (defaultLogger) Log(ctx context.Context, p LogParams, actorSubject, actorEmail, actorName, orgID string) error {
	var resourceID *uuid.UUID
	if p.ResourceID != "" {
		if u, err := uuid.Parse(p.ResourceID); err == nil {
			resourceID = &u
		}
	}
	var payload map[string]any
	if p.RequestPayloadFn != nil {
		payload = Redact(p.RequestPayloadFn())
	}
	row := &models.SecurityAuditLog{
		OrgID:                   orgID,
		ActorSubject:            actorSubject,
		ActorEmail:              actorEmail,
		ActorName:               actorName,
		CreatedAt:               time.Now().UTC(),
		ResourceType:            string(p.ResourceType),
		Action:                  string(p.Action),
		ResourceID:              resourceID,
		ResourceName:            p.ResourceName,
		RequestPayloadRedacted:  payload,
		Outcome:                 bool(p.Outcome),
		ErrorMessage:            p.ErrorMessage,
	}
	return models.CreateSecurityAuditLog(row)
}

// DefaultLogger is the logger used by LogFromContext. Set at startup or leave as default.
var DefaultLogger Logger = defaultLogger{}

// LogFromContext records one audit event. Who (actor, org) and When come from c and now.
func LogFromContext(c *gin.Context, p LogParams) {
	ctx := storagev2.ParseContext(c)
	if err := DefaultLogger.Log(c.Request.Context(), p, ctx.UserID, ctx.UserEmail, ctx.UserName, ctx.OrgID); err != nil {
		log.Warnf("security audit log write failed: %v", err)
	}
}

// LogFromContextErr logs one audit event and sets Outcome + ErrorMessage from err.
// If err != nil: Outcome = OutcomeFailure, ErrorMessage = err.Error().
// If err == nil: Outcome = OutcomeSuccess.
// payloadFn is the per-event raw payload function; implement for each event type. May be nil.
func LogFromContextErr(c *gin.Context, resourceType ResourceType, action Action, resourceID, resourceName string, payloadFn PayloadFn, err error) {
	outcome := OutcomeSuccess
	errMsg := ""
	if err != nil {
		outcome = OutcomeFailure
		errMsg = err.Error()
	}
	LogFromContext(c, LogParams{
		ResourceType:     resourceType,
		Action:           action,
		Outcome:          outcome,
		ResourceID:       resourceID,
		ResourceName:     resourceName,
		ErrorMessage:     errMsg,
		RequestPayloadFn: payloadFn,
	})
}

package pgaudit

import (
	"fmt"

	"github.com/runopsio/hoop/gateway/pgrest"
)

const (
	FeatureAskAiEnabled  string = "feature-ask-ai-enabled"
	FeatureAskAiDisabled string = "feature-ask-ai-disabled"
)

type audit struct{}

func New() *audit { return &audit{} }

func (a *audit) Create(ctx pgrest.AuditContext) error {
	return pgrest.New("/audit").Create(map[string]any{
		"org_id":     ctx.GetOrgID(),
		"event":      ctx.GetEventName(),
		"metadata":   ctx.GetMetadata(),
		"created_by": ctx.GetUserEmail(),
	}).Error()
}

func (a *audit) IsFeatureAskAiEnabled(ctx pgrest.OrgContext) (bool, error) {
	out := map[string]any{}
	err := pgrest.New("/audit?org_id=eq.%v&or=(event.eq.%s,event.eq.%s)&limit=1&order=created_at.desc",
		ctx.GetOrgID(), FeatureAskAiEnabled, FeatureAskAiDisabled).
		FetchOne().
		DecodeInto(&out)
	if err == pgrest.ErrNotFound {
		return false, nil
	}
	event, ok := out["event"]
	if !ok {
		return false, fmt.Errorf("event attribute not found")
	}
	return fmt.Sprintf("%v", event) == FeatureAskAiEnabled, nil
}

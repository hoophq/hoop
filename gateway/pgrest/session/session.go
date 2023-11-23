package pgsession

import (
	"github.com/runopsio/hoop/gateway/pgrest"
)

type session struct{}

func New() *session { return &session{} }

func (s *session) UpdateStatus(ctx pgrest.OrgContext, sessionID, status string) error {
	return pgrest.New("/sessions?org_id=eq.%s&id=eq.%s", ctx.GetOrgID(), sessionID).
		Patch(map[string]any{"status": status}).
		Error()
}

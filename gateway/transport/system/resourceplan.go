package transportsystem

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/proto"
	pbsystem "github.com/hoophq/hoop/common/proto/system"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/transport/streamclient"
	streamtypes "github.com/hoophq/hoop/gateway/transport/streamclient/types"
)

// ResourcePlanJobResponse holds the result of a synchronous plan execution.
type ResourcePlanJobResponse struct {
	PlanID  string
	Status  string
	Message string
}

// RunResourcePlan creates an audited session in the database, dispatches a
// ResourceManagerRequest with the plan script to the agent synchronously, updates
// the session with the result, and returns the plan outcome to the caller.
func RunResourcePlan(agentID string, req *pbsystem.ResourceManagerRequest) (*ResourcePlanJobResponse, error) {
	sid := uuid.NewString()
	req.SID = sid

	tags := map[string]string{
		"system":   "true",
		"resource": req.ResourceName,
		"role":     req.RoleName,
		"plan":     "true",
	}

	session := models.Session{
		ID:             sid,
		OrgID:          req.OrgID,
		Connection:     req.ConnectionName,
		ConnectionType: req.ResourceType,
		ConnectionTags: tags,
		Verb:           proto.ClientVerbExec,
		Status:         "open",
		BlobInput:      models.BlobInputType(req.Script),
		UserID:         req.UserID,
		UserName:       req.UserName,
		UserEmail:      req.UserEmail,
		CreatedAt:      time.Now().UTC(),
	}

	if err := models.UpsertSession(session); err != nil {
		return nil, fmt.Errorf("failed creating plan session: %v", err)
	}

	st := streamclient.GetAgentStream(streamtypes.NewStreamID(agentID, ""))
	if st == nil {
		closeResourceManagerSession(req.OrgID, sid)
		return nil, fmt.Errorf("agent stream not found for %v", agentID)
	}

	resp := dispatchResourceManager(st, req)

	exitCode := 0
	if resp.Status == pbsystem.StatusFailedType {
		exitCode = 1
	}
	endTime := time.Now().UTC()
	if err := models.UpdateSessionEventStream(models.SessionDone{
		ID:         sid,
		OrgID:      req.OrgID,
		Metrics:    map[string]any{},
		BlobStream: toEventStream(resp.Message),
		Status:     "done",
		ExitCode:   &exitCode,
		EndSession: &endTime,
	}); err != nil {
		log.With("sid", sid).Errorf("failed closing resource plan session: %v", err)
	}

	return &ResourcePlanJobResponse{
		PlanID:  sid,
		Status:  resp.Status,
		Message: resp.Message,
	}, nil
}

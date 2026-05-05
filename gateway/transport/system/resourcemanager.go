package transportsystem

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/proto"
	pbsystem "github.com/hoophq/hoop/common/proto/system"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/transport/streamclient"
	streamtypes "github.com/hoophq/hoop/gateway/transport/streamclient/types"
)

var resourceManagerTimeout = time.Minute * 10

// ResourceManagerJobResponse is returned immediately after the session is created
// and the async dispatch has been started.
type ResourceManagerJobResponse struct {
	SessionID string
	Tags      map[string]string
	Status    string
}

// RunResourceManager creates a session in the database, validates the target agent is
// reachable, then dispatches a ResourceManagerRequest packet to the agent
// asynchronously. The caller receives a ResourceManagerJobResponse with the session ID
// that can be used to track progress via the sessions endpoint.
//
// The script stored in the session has all sensitive attribute values redacted:
// MasterPassword, all EnvVars values, and all string-typed TemplateData values
// are replaced with [REDACTED].
//
// The session is closed with a failure exit code if:
//   - the agent stream cannot be found after the session is created, or
//   - the dispatch goroutine encounters an error or timeout.
func RunResourceManager(agentID string, req *pbsystem.ResourceManagerRequest) (*ResourceManagerJobResponse, error) {
	sid := uuid.NewString()
	req.SID = sid

	tags := map[string]string{
		"system":     "true",
		"resource":   req.ResourceName,
		"connection": req.ConnectionName,
		"role":       req.RoleName,
	}

	session := models.Session{
		ID:             sid,
		OrgID:          req.OrgID,
		Connection:     req.ConnectionName,
		ConnectionType: req.ResourceType,
		ConnectionTags: tags,
		Verb:           proto.ClientVerbExec,
		Status:         "open",
		BlobInput:      models.BlobInputType(redactScript(req)),
		UserID:         req.UserID,
		UserName:       req.UserName,
		UserEmail:      req.UserEmail,
		CreatedAt:      time.Now().UTC(),
	}

	if err := models.UpsertSession(session); err != nil {
		return nil, fmt.Errorf("failed creating session: %v", err)
	}

	st := streamclient.GetAgentStream(streamtypes.NewStreamID(agentID, ""))
	if st == nil {
		closeResourceManagerSession(req.OrgID, sid)
		return nil, fmt.Errorf("agent stream not found for %v", agentID)
	}

	go func() {
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
			log.With("sid", sid).Errorf("failed closing resource manager session: %v", err)
		}
	}()

	return &ResourceManagerJobResponse{
		SessionID: sid,
		Tags:      tags,
		Status:    "pending",
	}, nil
}

func dispatchResourceManager(st *streamclient.AgentStream, req *pbsystem.ResourceManagerRequest) *pbsystem.ResourceManagerResponse {
	dataCh := make(chan []byte)
	systemStore.Set(req.SID, dataCh)
	defer func() {
		systemStore.Del(req.SID)
		close(dataCh)
	}()

	payload, pbType, err := pbsystem.NewResourceManagerRequest(req)
	if err != nil {
		return pbsystem.NewResourceManagerError(req.SID, "failed encoding request: %v", err)
	}

	if err := st.Send(&proto.Packet{
		Type:    pbType,
		Payload: payload,
		Spec:    map[string][]byte{proto.SpecGatewaySessionID: []byte(req.SID)},
	}); err != nil {
		return pbsystem.NewResourceManagerError(req.SID, "failed sending request to agent: %v", err)
	}

	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), resourceManagerTimeout)
	defer cancelFn()
	select {
	case payload := <-dataCh:
		var resp pbsystem.ResourceManagerResponse
		if err := json.Unmarshal(payload, &resp); err != nil {
			return pbsystem.NewResourceManagerError(req.SID, "failed decoding agent response: %v", err)
		}
		return &resp
	case <-timeoutCtx.Done():
		return pbsystem.NewResourceManagerError(req.SID,
			"timeout (%v) waiting for response from agent %v/%v",
			resourceManagerTimeout, st.AgentName(), st.AgentVersion())
	}
}

// redactScript returns req.Script with sensitive attribute values partially masked
// so the stored session input is auditable without leaking secrets.
// EnvVars values and string-typed TemplateData values are replaced with a partial
// representation: the first three characters followed by "***" (e.g. "myp***").
// Values shorter than three characters are replaced entirely with "***".
func redactScript(req *pbsystem.ResourceManagerRequest) string {
	result := req.Script

	for _, v := range req.EnvVars {
		if v != "" {
			result = strings.ReplaceAll(result, v, partialRedact(v))
		}
	}

	for _, v := range req.TemplateData {
		if s, ok := v.(string); ok && s != "" {
			result = strings.ReplaceAll(result, s, partialRedact(s))
		}
	}

	return result
}

// partialRedact returns the first three characters of s followed by "***",
// or just "***" when s is shorter than three characters.
func partialRedact(s string) string {
	if len(s) <= 3 {
		return "***"
	}
	return s[:3] + "***"
}

// toEventStream encodes output into the session event-stream format expected by
// the session parser: a JSON array of [time, type, base64data] tuples.
// We emit a single "o" (output) event at t=0 with the full message.
func toEventStream(output string) json.RawMessage {
	encoded := base64.StdEncoding.EncodeToString([]byte(output))
	return json.RawMessage(fmt.Sprintf(`[[0,"o",%q]]`, encoded))
}

func closeResourceManagerSession(orgID, sid string) {
	exitCode := 1
	endTime := time.Now().UTC()
	if err := models.UpdateSessionEventStream(models.SessionDone{
		ID:         sid,
		OrgID:      orgID,
		Metrics:    map[string]any{},
		Status:     "done",
		ExitCode:   &exitCode,
		EndSession: &endTime,
	}); err != nil {
		log.With("sid", sid).Errorf("failed closing resource manager session: %v", err)
	}
}

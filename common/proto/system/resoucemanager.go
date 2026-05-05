package pbsystem

import (
	"encoding/json"
	"fmt"
)

const (
	ResourceManagerRequestType  string = "SysResourceManagerRequest"
	ResourceManagerResponseType string = "SysResourceManagerResponse"
)

// ResourceManagerRequest is the packet payload sent to the agent to provision
// an external role on a target system.
type ResourceManagerRequest struct {
	SID            string `json:"sid"`
	OrgID          string `json:"org_id"`
	UserID         string `json:"user_id"`
	UserName       string `json:"user_name"`
	UserEmail      string `json:"user_email"`
	ResourceName   string `json:"resource_name"`
	ResourceType   string `json:"resource_type"`
	ConnectionName string `json:"connection_name"`
	RoleName       string `json:"role_name"`
	// Script is a Go template string that the agent executes to provision the role.
	Script string `json:"script"`
	// TemplateData holds arbitrary key-value data rendered into Script at execution time.
	TemplateData map[string]any `json:"template_data"`
	// Command is the entrypoint used to run the rendered script (e.g. ["bash", "-c"]).
	Command []string `json:"command"`
	// EnvVars are environment variables injected into the script runtime.
	EnvVars map[string]string `json:"env_vars"`
}

// ResourceManagerResponse is the packet payload sent back by the agent after
// attempting to provision the external role(s).
type ResourceManagerResponse struct {
	SessionID string `json:"sid"`
	Status    string `json:"status"`
	Message   string `json:"message"`
}

func (r *ResourceManagerResponse) Encode() ([]byte, string, error) {
	payload, err := json.Marshal(r)
	if err != nil {
		return nil, "", err
	}
	return payload, ResourceManagerResponseType, nil
}

func NewResourceManagerRequest(req *ResourceManagerRequest) ([]byte, string, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, "", err
	}
	return payload, ResourceManagerRequestType, nil
}

func NewResourceManagerError(sid, format string, a ...any) *ResourceManagerResponse {
	return &ResourceManagerResponse{
		SessionID: sid,
		Status:    StatusFailedType,
		Message:   fmt.Sprintf(format, a...),
	}
}

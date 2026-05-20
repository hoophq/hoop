package pbsystem

import (
	"encoding/json"
)

const (
	BareExecRequestType  string = "SysBareExecRequest"
	BareExecResponseType string = "SysBareExecResponse"
)

type BareExecRequest struct {
	SID          string            `json:"sid"`
	AgentID      string            `json:"agent_id"`
	Script       string            `json:"script"`
	TemplateData map[string]any    `json:"template_data"`
	Command      []string          `json:"command"`
	EnvVars      map[string]string `json:"env_vars"`
}

type BareExecResponse struct {
	SessionID string `json:"sid"`
	Status    string `json:"status"`
	Output    string `json:"output"`
}

func (r *BareExecResponse) Encode() ([]byte, string, error) {
	payload, err := json.Marshal(r)
	if err != nil {
		return nil, "", err
	}
	return payload, BareExecResponseType, nil
}

func NewBareExecRequest(req *BareExecRequest) ([]byte, string, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, "", err
	}
	return payload, BareExecRequestType, nil
}

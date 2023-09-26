package apitypes

type Connection struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Type      string         `json:"type"`
	Command   []string       `json:"command"`
	Secrets   map[string]any `json:"secrets"`
	AgentId   string         `json:"agentId"`
	CreatedAt string         `json:"createdAt"`
	UpdatedAt string         `json:"updatedAt"`
	Agent     Agent          `json:"agent"`
}

type AgentAuthMetadata struct {
	Hostname      string `json:"hostname"`
	Platform      string `json:"platform"`
	MachineID     string `json:"machineId"`
	KernelVersion string `json:"kernelVersion"`
	Version       string `json:"version"`
	GoVersion     string `json:"goVersion"`
	Compiler      string `json:"compiler"`
}

type AgentAuthRequest struct {
	Status   string             `json:"status"`
	Metadata *AgentAuthMetadata `json:"metadata"`
}

type Agent struct {
	ID       string            `json:"id"`
	OrgID    string            `json:"orgId"`
	Name     string            `json:"name"`
	Mode     string            `json:"mode"`
	Metadata AgentAuthMetadata `json:"metadata"`
}

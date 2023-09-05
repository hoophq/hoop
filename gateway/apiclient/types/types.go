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
}

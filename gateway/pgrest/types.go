package pgrest

type Login struct {
	ID       string `json:"id"`
	Outcome  string `json:"outcome"`
	Redirect string `json:"redirect"`
	SlackID  string `json:"slack_id"`
}

type Org struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type User struct {
	ID      string   `json:"id"`
	OrgID   string   `json:"org_id"`
	Name    string   `json:"name"`
	Email   string   `json:"email"`
	Status  string   `json:"status"`
	SlackID string   `json:"slack_id"`
	Groups  []string `json:"groups"`

	Org *Org `json:"org"`
}

type EnvVar struct {
	ID    string            `json:"id"`
	OrgID string            `json:"org_id"`
	Envs  map[string]string `json:"envs"`
}

type Agent struct {
	ID       string            `json:"id"`
	OrgID    string            `json:"org_id"`
	Name     string            `json:"name"`
	Mode     string            `json:"mode"`
	Token    string            `json:"token"`
	Status   string            `json:"status"`
	Metadata map[string]string `json:"metadata"`

	Org Org `json:"org"`
}

type Connection struct {
	ID      string            `json:"id"`
	OrgID   string            `json:"org_id"`
	AgentID string            `json:"agent_id"`
	Name    string            `json:"name"`
	Command []string          `json:"command"`
	Type    string            `json:"type"`
	Envs    map[string]string `json:"envs"`

	Org Org `json:"org"`
	// Agent Agent  `json:"agent"`
}

type Plugin struct {
	ID     string  `json:"id"`
	OrgID  string  `json:"org_id"`
	Name   string  `json:"name"`
	Source *string `json:"source"`

	EnvVar *EnvVar `json:"env_vars"`
}

type PluginConnection struct {
	ID               string   `json:"id"`
	OrgID            string   `json:"org_id"`
	PluginID         string   `json:"plugin_id"`
	ConnectionID     string   `json:"connection_id"`
	Enabled          bool     `json:"enabled"`
	ConnectionConfig []string `json:"config"`

	Plugin     Plugin     `json:"plugins"`
	EnvVar     EnvVar     `json:"env_vars"`
	Connection Connection `json:"connections"`
}

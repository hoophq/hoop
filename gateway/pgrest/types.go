package pgrest

type Context interface {
	OrgContext
	UserContext
}

type OrgContext interface {
	GetOrgID() string
}

type UserContext interface {
	GetUserID() string
}

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
	ID            string            `json:"id"`
	OrgID         string            `json:"org_id"`
	AgentID       string            `json:"agent_id"`
	LegacyAgentID string            `json:"legacy_agent_id"`
	Name          string            `json:"name"`
	Command       []string          `json:"command"`
	Type          string            `json:"type"`
	Envs          map[string]string `json:"envs"`

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

type Blob struct {
	ID         string `json:"id"`
	OrgID      string `json:"org_id"`
	Type       string `json:"type"`
	Size       int64  `json:"size"`
	BlobStream []any  `json:"blob_stream"`
}

type Session struct {
	ID             string            `json:"id"`
	OrgID          string            `json:"org_id"`
	Labels         map[string]string `json:"labels"`
	Connection     string            `json:"connection"`
	ConnectionType string            `json:"connection_type"`
	Verb           string            `json:"verb"`
	UserID         string            `json:"user_id"`
	UserName       string            `json:"user_name"`
	UserEmail      string            `json:"user_email"`
	Status         string            `json:"status"`
	BlobInputID    string            `json:"blob_input_id"`
	BlobStreamID   string            `json:"blob_stream_id"`
	BlobInput      *Blob             `json:"blob_input"`
	BlobStream     *Blob             `json:"blob_stream"`
	Metadata       map[string]any    `json:"metadata"`
	// TODO: convert to time.Time
	CreatedAt string  `json:"created_at"`
	EndedAt   *string `json:"ended_at"`
}

type ServiceAccount struct {
	ID        string   `json:"id"`
	OrgID     string   `json:"org_id"`
	Subject   string   `json:"subject"`
	Name      string   `json:"name"`
	Status    string   `json:"status"`
	Groups    []string `json:"groups"`
	CreatedAt string   `json:"created_at"`
	UpdateAt  string   `json:"updated_at"`
}

type ProxyManagerState struct {
	ID             string            `json:"id"`
	OrgID          string            `json:"org_id"`
	Status         string            `json:"status"`
	Connection     string            `json:"connection"`
	Port           string            `json:"port"`
	AccessDuration int               `json:"access_duration"`
	ClientMetadata map[string]string `json:"metadata"`
	ConnectedAt    string            `json:"connected_at"`
}

type ClientKey struct {
	ID      string `json:"id"`
	OrgID   string `json:"org_id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	DSNHash string `json:"dsn_hash"`
}

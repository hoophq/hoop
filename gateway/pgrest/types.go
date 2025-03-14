package pgrest

import (
	"encoding/json"
)

type Context interface {
	OrgContext
	IsAdmin() bool
	GetUserGroups() []string
}

type OrgContext interface {
	GetOrgID() string
}

type LicenseContext interface {
	OrgContext
	GetLicenseName() string
}

type AuditContext interface {
	OrgContext
	GetEventName() string
	GetUserEmail() string
	GetMetadata() map[string]any
}

type UserContext interface {
	GetOrgID() string
	GetUserID() string
}
type Login struct {
	ID       string `json:"id"`
	Outcome  string `json:"outcome"`
	Redirect string `json:"redirect"`
	SlackID  string `json:"slack_id"`
}

type Org struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	License     string           `json:"license"`
	LicenseData *json.RawMessage `json:"license_data"`
}

type User struct {
	ID       string   `json:"id"`
	OrgID    string   `json:"org_id"`
	Subject  string   `json:"subject"`
	Name     string   `json:"name"`
	Picture  string   `json:"picture"`
	Email    string   `json:"email"`
	Verified bool     `json:"verified"`
	Status   string   `json:"status"`
	SlackID  string   `json:"slack_id"`
	Groups   []string `json:"groups"`
	Org      *Org     `json:"orgs"`
	// used for local auth only
	HashedPassword string `json:"hashed_password"`
}

type EnvVar struct {
	ID    string            `json:"id"`
	OrgID string            `json:"org_id"`
	Envs  map[string]string `json:"envs"`
}

const (
	AgentStatusConnected    = "CONNECTED"
	AgentStatusDisconnected = "DISCONNECTED"
)

type Agent struct {
	ID        string            `json:"id"`
	OrgID     string            `json:"org_id"`
	Name      string            `json:"name"`
	Mode      string            `json:"mode"`
	Key       string            `json:"key"`
	KeyHash   string            `json:"key_hash"`
	Status    string            `json:"status"`
	Metadata  map[string]string `json:"metadata"`
	UpdatedAt *string           `json:"updated_at"`

	Org Org `json:"orgs"`
}

const (
	ConnectionStatusOnline  = "online"
	ConnectionStatusOffline = "offline"
)

type Connection struct {
	ID                 string            `json:"id"`
	OrgID              string            `json:"org_id"`
	AgentID            string            `json:"agent_id"`
	Name               string            `json:"name"`
	Command            []string          `json:"command"`
	Type               string            `json:"type"`
	SubType            string            `json:"subtype"`
	Envs               map[string]string `json:"envs"`
	Status             string            `json:"status"` // read only field
	ManagedBy          *string           `json:"managed_by"`
	Tags               []string          `json:"tags"`
	AccessModeRunbooks string            `json:"access_mode_runbooks"`
	AccessModeExec     string            `json:"access_mode_exec"`
	AccessModeConnect  string            `json:"access_mode_connect"`
	AccessSchema       string            `json:"access_schema"`

	// read only attributes
	Org              Org                `json:"orgs"`
	PluginConnection []PluginConnection `json:"plugin_connections"`
	Agent            Agent              `json:"agents"`
}

func (c Connection) AsSecrets() map[string]any {
	dst := map[string]any{}
	for k, v := range c.Envs {
		dst[k] = v
	}
	return dst
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
	JiraIssue      string            `json:"jira_issue"`
	BlobInputID    string            `json:"blob_input_id"`
	BlobStreamID   string            `json:"blob_stream_id"`
	BlobInput      *Blob             `json:"blob_input"`
	BlobStream     *Blob             `json:"blob_stream"`
	Metadata       map[string]any    `json:"metadata"`
	Metrics        map[string]any    `json:"metrics"`
	// TODO: convert to time.Time
	CreatedAt string  `json:"created_at"`
	EndedAt   *string `json:"ended_at"`
}

type SessionReport struct {
	Items                 []SessionReportItem `json:"items"`
	TotalRedactCount      int64               `json:"total_redact_count"`
	TotalTransformedBytes int64               `json:"total_transformed_bytes"`
}

type SessionReportItem struct {
	ResourceName     string `json:"resource"`
	InfoType         string `json:"info_type"`
	RedactTotal      int64  `json:"redact_total"`
	TransformedBytes int64  `json:"transformed_bytes"`
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

	Org Org `json:"orgs"`
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

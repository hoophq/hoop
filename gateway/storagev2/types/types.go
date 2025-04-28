package types

import (
	"encoding/json"
	"time"
)

type APIContext struct {
	OrgID          string           `json:"org_id"`
	OrgName        string           `json:"org_name"`
	OrgLicense     string           `json:"org_license"`
	OrgLicenseData *json.RawMessage `json:"org_license_data"`
	UserID         string           `json:"user_id"`
	UserName       string           `json:"user_name"`
	UserEmail      string           `json:"user_email"`
	UserGroups     []string         `json:"user_groups"`
	UserStatus     string           `json:"user_status"`
	SlackID        string           `json:"slack_id"`
	UserPicture    string           `json:"picture"`

	UserAnonSubject       string
	UserAnonEmail         string
	UserAnonProfile       string
	UserAnonPicture       string
	UserAnonEmailVerified *bool

	ApiURL  string `json:"-"`
	GrpcURL string `json:"-"`
}

type Plugin struct {
	ID             string              `json:"id"`
	OrgID          string              `json:"-"`
	Name           string              `json:"name"`
	Connections    []*PluginConnection `json:"connections"`
	ConnectionsIDs []string            `json:"-"`
	Config         *PluginConfig       `json:"config"`
	ConfigID       *string             `json:"-"`
	Source         *string             `json:"source"`
	Priority       int                 `json:"priority"`
	InstalledById  string              `json:"-"`
}

type PluginConfig struct {
	ID      string            `json:"id"`
	OrgID   string            `json:"-"`
	EnvVars map[string]string `json:"envvars"`
}

type PluginConnection struct {
	ID           string   `json:"-"`
	ConnectionID string   `json:"id"`
	Name         string   `json:"name"`
	Config       []string `json:"config"`

	Connection Connection `json:"-"`
}

type Login struct {
	ID       string
	Redirect string
	Outcome  string
	SlackID  string
}

type Client struct {
	ID                    string
	OrgID                 string
	Status                ClientStatusType
	RequestConnectionName string
	RequestPort           string
	RequestAccessDuration time.Duration
	ClientMetadata        map[string]string
	ConnectedAt           time.Time
}

type Connection struct {
	Id                 string         `json:"id"`
	OrgId              string         `json:"-"`
	Name               string         `json:"name"`
	IconName           string         `json:"icon_name"`
	Command            []string       `json:"command"`
	Type               string         `json:"type"`
	SubType            string         `json:"subtype"`
	Secret             map[string]any `json:"secret"`
	AgentId            string         `json:"agent_id"`
	AccessModeRunbooks string         `json:"access_mode_runbooks"`
	AccessModeExec     string         `json:"access_mode_exec"`
	AccessModeConnect  string         `json:"access_mode_connect"`
	AccessSchema       string         `json:"access_schema"`
}

type ConnectionInfo struct {
	ID                               string
	Name                             string
	Type                             string
	SubType                          string
	Command                          []string
	Secrets                          map[string]any
	Tags                             map[string]string
	AgentID                          string
	AgentName                        string
	AgentMode                        string
	AccessModeRunbooks               string
	AccessModeExec                   string
	AccessModeConnect                string
	AccessSchema                     string
	JiraTransitionNameOnSessionClose string
}

type ReviewOwner struct {
	Id      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Email   string `json:"email"`
	SlackID string `json:"slack_id"`
}

type ReviewConnection struct {
	Id   string `json:"id,omitempty"`
	Name string `json:"name"`
}

type ReviewGroup struct {
	Id         string       `json:"id"`
	Group      string       `json:"group"`
	Status     ReviewStatus `json:"status"`
	ReviewedBy *ReviewOwner `json:"reviewed_by"`
	ReviewDate *string      `json:"review_date"`
}

type Review struct {
	Id               string
	OrgId            string
	CreatedAt        time.Time
	Type             string
	Session          string
	Input            string
	InputEnvVars     map[string]string
	InputClientArgs  []string
	AccessDuration   time.Duration
	Status           ReviewStatus
	RevokeAt         *time.Time
	CreatedBy        any
	ReviewOwner      ReviewOwner
	ConnectionId     any
	Connection       ReviewConnection
	ReviewGroupsIds  []string
	ReviewGroupsData []ReviewGroup
}

type ReviewJSON struct {
	Id        string    `json:"id"`
	OrgId     string    `json:"org"`
	CreatedAt time.Time `json:"created_at"`
	Type      string    `json:"type"`
	Session   string    `json:"session"`
	Input     string    `json:"input"`
	// Redacted for now
	// InputEnvVars     map[string]string `json:"input_envvars"`
	InputClientArgs  []string         `json:"input_clientargs"`
	AccessDuration   time.Duration    `json:"access_duration"`
	Status           ReviewStatus     `json:"status"`
	RevokeAt         *time.Time       `json:"revoke_at"`
	ReviewOwner      ReviewOwner      `json:"review_owner"`
	Connection       ReviewConnection `json:"review_connection"`
	ReviewGroupsData []ReviewGroup    `json:"review_groups_data"`
}

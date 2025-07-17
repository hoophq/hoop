package types

import (
	"encoding/json"

	idptypes "github.com/hoophq/hoop/gateway/idp/types"
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

	ApiURL       string                `json:"-"`
	GrpcURL      string                `json:"-"`
	ProviderType idptypes.ProviderType `json:"-"`
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

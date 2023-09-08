package types

import (
	"time"

	"olympos.io/encoding/edn"
)

// TxObject must be a struct containing edn or a raw edn string.
// See https://github.com/go-edn/edn.
type TxObject any

type TxResponse struct {
	TxID   int64     `edn:"xtdb.api/tx-id"`
	TxTime time.Time `edn:"xtdb.api/tx-time"`
}

type APIContext struct {
	OrgID      string   `json:"org_id"`
	OrgName    string   `json:"org_name"`
	UserID     string   `json:"user_id"`
	UserName   string   `json:"user_name"`
	UserEmail  string   `json:"user_email"`
	UserGroups []string `json:"user_groups"`
	UserStatus string   `json:"user_status"`
	SlackID    string   `json:"slack_id"`

	ApiURL  string `json:"-"`
	GrpcURL string `json:"-"`
}

type DSNContext struct {
	EntityID      string
	OrgID         string
	ClientKeyName string
}

type Plugin struct {
	ID             string              `json:"id"          edn:"xt/id"`
	OrgID          string              `json:"-"           edn:"plugin/org"`
	Name           string              `json:"name"        edn:"plugin/name"`
	Connections    []*PluginConnection `json:"connections" edn:"plugin/connections,omitempty"`
	ConnectionsIDs []string            `json:"-"           edn:"plugin/connection-ids"`
	Config         *PluginConfig       `json:"config"      edn:"plugin/config,omitempty"`
	ConfigID       *string             `json:"-"           edn:"plugin/config-id"`
	Source         *string             `json:"source"      edn:"plugin/source"`
	Priority       int                 `json:"priority"    edn:"plugin/priority"`
	InstalledById  string              `json:"-"           edn:"plugin/installed-by"`
}

type PluginConfig struct {
	ID      string            `json:"id"      edn:"xt/id"`
	OrgID   string            `json:"-"       edn:"pluginconfig/org"`
	EnvVars map[string]string `json:"envvars" edn:"pluginconfig/envvars"`
}

type PluginConnection struct {
	ID           string   `json:"-"      edn:"xt/id"`
	ConnectionID string   `json:"id"     edn:"plugin-connection/id"`
	Name         string   `json:"name"   edn:"plugin-connection/name"`
	Config       []string `json:"config" edn:"plugin-connection/config"`

	Connection Connection `json:"-" edn:"connection,omitempty"`
}

type Login struct {
	ID       string `edn:"xt/id"`
	Redirect string `edn:"login/redirect"`
	Outcome  string `edn:"login/outcome"`
	SlackID  string `edn:"login/slack-id"`
}

type Client struct {
	ID                    string            `edn:"xt/id"`
	OrgID                 string            `edn:"client/org"`
	Status                ClientStatusType  `edn:"client/status"`
	RequestConnectionName string            `edn:"client/request-connection"`
	RequestPort           string            `edn:"client/request-port"`
	RequestAccessDuration time.Duration     `edn:"client/access-duration"`
	ClientMetadata        map[string]string `edn:"client/metadata"`
	ConnectedAt           time.Time         `edn:"client/connected-at"`
}

type Connection struct {
	Id             string   `edn:"xt/id"`
	OrgId          string   `edn:"connection/org"`
	Name           string   `edn:"connection/name"`
	IconName       string   `edn:"connection/icon-name"`
	Command        []string `edn:"connection/command"`
	Type           string   `edn:"connection/type"`
	SecretProvider string   `edn:"connection/secret-provider"`
	SecretId       string   `edn:"connection/secret"`
	CreatedById    string   `edn:"connection/created-by"`
	AgentId        string   `edn:"connection/agent"`
}

type ConnectionInfo struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Type          string         `json:"type"`
	CmdEntrypoint []string       `json:"cmd"`
	Secrets       map[string]any `json:"secrets"`
	AgentID       string         `json:"agent_id"`
	AgentName     string         `json:"agent_name"`
	AgentMode     string         `json:"agent_mode"`
}

type ReviewOwner struct {
	Id      string `json:"id,omitempty"   edn:"xt/id"`
	Name    string `json:"name,omitempty" edn:"review-user/name"`
	Email   string `json:"email"          edn:"review-user/email"`
	SlackID string `json:"slack_id"       edn:"review-user/slack-id"`
}

type ReviewConnection struct {
	Id   string `json:"id,omitempty" edn:"xt/id"`
	Name string `json:"name"         edn:"review-connection/name"`
}

type ReviewGroup struct {
	Id         string       `json:"id"          edn:"xt/id"`
	Group      string       `json:"group"       edn:"review-group/group"`
	Status     ReviewStatus `json:"status"      edn:"review-group/status"`
	ReviewedBy *ReviewOwner `json:"reviewed_by" edn:"review-group/reviewed-by"`
	ReviewDate *string      `json:"review_date" edn:"review-group/review_date"`
}

type Review struct {
	Id               string            `edn:"xt/id"`
	OrgId            string            `edn:"review/org"`
	CreatedAt        time.Time         `edn:"review/created-at"`
	Type             string            `edn:"review/type"`
	Session          string            `edn:"review/session"`
	Input            string            `edn:"review/input"`
	InputEnvVars     map[string]string `edn:"review/input-envvars"`
	InputClientArgs  []string          `edn:"review/input-clientargs"`
	AccessDuration   time.Duration     `edn:"review/access-duration"`
	Status           ReviewStatus      `edn:"review/status"`
	RevokeAt         *time.Time        `edn:"review/revoke-at"`
	CreatedBy        any               `edn:"review/created-by"`
	ReviewOwner      ReviewOwner       `edn:"review/review-owner"`
	ConnectionId     any               `edn:"review/connection"`
	Connection       ReviewConnection  `edn:"review/review-connection"`
	ReviewGroupsIds  []string          `edn:"review/review-groups"`
	ReviewGroupsData []ReviewGroup     `edn:"review/review-groups-data"`
}

type ReviewJSON struct {
	Id               string            `json:"id"`
	OrgId            string            `json:"org"`
	CreatedAt        time.Time         `json:"created_at"`
	Type             string            `json:"type"`
	Session          string            `json:"session"`
	Input            string            `json:"input"`
	InputEnvVars     map[string]string `json:"input_envvars"`
	InputClientArgs  []string          `json:"input_clientargs"`
	AccessDuration   time.Duration     `json:"access_duration"`
	Status           ReviewStatus      `json:"status"`
	RevokeAt         *time.Time        `json:"revoke_at"`
	ReviewOwner      ReviewOwner       `json:"review_owner"`
	Connection       ReviewConnection  `json:"review_connection"`
	ReviewGroupsData []ReviewGroup     `json:"review_groups_data"`
}

type SessionEventStream []any
type SessionNonIndexedEventStreamList map[edn.Keyword][]SessionEventStream
type SessionScript map[edn.Keyword]string
type SessionLabels map[string]string

type Session struct {
	ID          string             `json:"id"           edn:"xt/id"`
	OrgID       string             `json:"-"            edn:"session/org-id"`
	Script      SessionScript      `json:"script"       edn:"session/script"`
	Labels      SessionLabels      `json:"labels"       edn:"session/labels"`
	UserEmail   string             `json:"user"         edn:"session/user"`
	UserID      string             `json:"user_id"      edn:"session/user-id"`
	UserName    string             `json:"user_name"    edn:"session/user-name"`
	Type        string             `json:"type"         edn:"session/type"`
	Connection  string             `json:"connection"   edn:"session/connection"`
	Review      *ReviewJSON        `json:"review"       edn:"session/review"`
	Verb        string             `json:"verb"         edn:"session/verb"`
	Status      string             `json:"status"       edn:"session/status"`
	DlpCount    int64              `json:"dlp_count"    edn:"session/dlp-count"`
	EventStream SessionEventStream `json:"event_stream" edn:"session/event-stream"`
	// Must NOT index streams (all top keys are indexed in xtdb)
	NonIndexedStream SessionNonIndexedEventStreamList `json:"-"          edn:"session/xtdb-stream"`
	EventSize        int64                            `json:"event_size" edn:"session/event-size"`
	StartSession     time.Time                        `json:"start_date" edn:"session/start-date"`
	EndSession       *time.Time                       `json:"end_date"   edn:"session/end-date"`
}

type User struct {
	Id      string         `json:"id"       edn:"xt/id"`
	Org     string         `json:"-"        edn:"user/org"`
	Name    string         `json:"name"     edn:"user/name"`
	Email   string         `json:"email"    edn:"user/email"`
	Status  UserStatusType `json:"status"   edn:"user/status"`
	SlackID string         `json:"slack_id" edn:"user/slack-id"`
	Groups  []string       `json:"groups"   edn:"user/groups"`
}

type InvitedUser struct {
	ID      string   `json:"id"       edn:"xt/id"`
	OrgID   string   `json:"-"        edn:"invited-user/org"`
	Email   string   `json:"email"    edn:"invited-user/email"`
	Name    string   `json:"name"     end:"invited-user/name"`
	SlackID string   `json:"slack_id" edn:"invited-user/slack-id"`
	Groups  []string `json:"groups"   edn:"invited-user/groups"`
}

type ClientKey struct {
	ID        string `json:"id"         edn:"xt/id"`
	OrgID     string `json:"-"          edn:"clientkey/org"`
	Name      string `json:"name"       edn:"clientkey/name"`
	AgentMode string `json:"agent_mode" edn:"clientkey/agent-mode"`
	Active    bool   `json:"active"     edn:"clientkey/enabled"`
	DSNHash   string `json:"-"          edn:"clientkey/dsnhash"`
}

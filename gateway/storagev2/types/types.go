package types

import (
	"time"

	"olympos.io/encoding/edn"
)

// TxEdnStruct must be a struct containing edn fields.
// See https://github.com/go-edn/edn.
type TxEdnStruct any

type TxResponse struct {
	TxID   int64     `edn:"xtdb.api/tx-id"`
	TxTime time.Time `edn:"xtdb.api/tx-time"`
}

type APIContext struct {
	OrgID  string
	UserID string

	UserName   string
	UserEmail  string
	UserGroups []string
}

// Plugin for now is an auxiliar type
// this information should always be derived from gateway/plugin/service.go#Plugin
type Plugin struct {
	OrgID       string
	Name        string
	Connections []PluginConnection
	Config      *PluginConfig
}

// PluginConfig for now is an auxiliar type
// this information should always be derived from gateway/plugin/service.go#PluginConfig
type PluginConfig struct {
	EnvVars map[string]string
}

// PluginConfig for now is an auxiliar type
// this information should always be derived from gateway/plugin/service.go#Connection
type PluginConnection struct {
	ID           string
	ConnectionID string
	Name         string
	Config       []string
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
	Id             string           `json:"id"                      edn:"xt/id"`
	OrgId          string           `json:"org" 				 	edn:"review/org"`
	CreatedAt      time.Time        `json:"created_at"              edn:"review/created-at"`
	Type           string           `json:"type"                    edn:"review/type"`
	Session        string           `json:"session"                 edn:"review/session"`
	Input          string           `json:"input"                   edn:"review/input"`
	AccessDuration time.Duration    `json:"access_duration"         edn:"review/access-duration"`
	Status         ReviewStatus     `json:"status"                  edn:"review/status"`
	RevokeAt       *time.Time       `json:"revoke_at"               edn:"review/revoke-at"`
	CreatedBy      string           `json:"created_by"              edn:"review/created-by"`
	ReviewOwner    ReviewOwner      `json:"review_owner"			edn:"review/review-owner"`
	ConnectionId   string           `json:"connection"              edn:"review/connection"`
	Connection     ReviewConnection `json:"review_connection"       edn:"review/review-connection"`
	ReviewGroups   []ReviewGroup    `json:"review_groups,omitempty" edn:"review/review-groups"`
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
	Review      *Review            `json:"review" 		edn:"session/review"`
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

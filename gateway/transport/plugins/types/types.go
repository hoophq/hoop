package plugintypes

import (
	"context"
	"fmt"
	"slices"
	"time"

	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

type GenericMap map[string]any

type PacketErr struct {
	exitCode *int
	msg      string
}

func (e PacketErr) Error() string  { return e.msg }
func (e PacketErr) ExitCode() *int { return e.exitCode }

func NewPacketErr(msg string, exitCode *int) error {
	return &PacketErr{msg: msg, exitCode: exitCode}
}

type Context struct {
	Context context.Context
	// Session ID
	SID string

	// Use Attributes
	OrgID          string
	OrgName        string
	OrgLicenseType string
	UserID         string
	UserName       string
	UserEmail      string
	UserSlackID    string
	UserGroups     []string

	// Connection attributes
	ConnectionID                        string
	ConnectionName                      string
	ConnectionType                      string
	ConnectionSubType                   string
	ConnectionCommand                   []string
	ConnectionSecret                    map[string]any
	ConnectionTags                      map[string]string
	ConnectionJiraTransitionNameOnClose string

	// Agent attributes
	AgentID   string
	AgentName string
	AgentMode string

	// Plugin attributes
	PluginConnectionConfig []string

	// Gateway client attributes
	ClientVerb   string
	ClientOrigin string

	ParamsData GenericMap
}

type PluginResource interface {
	GetName() string
	GetOrgID() string
	GetEnvVars() map[string]string
}

type Plugin interface {
	Name() string
	OnStartup(pctx Context) error
	OnUpdate(oldState, newState PluginResource) error
	OnConnect(pctx Context) error
	OnReceive(pctx Context, pkt *pb.Packet) (*ConnectResponse, error)
	OnDisconnect(pctx Context, errMsg error) error
}

type ConnectResponse struct {
	// The new context to propagate to the client transport layer
	Context context.Context
	// When this attribute is set, the packet will be sent to the client.
	// The transport layer must stop processing any further logic and
	// just wait for new client packets to arrive.
	//
	// This is useful when a plugin needs to intercept the current flow and
	// send a packet back to client.
	ClientPacket *pb.Packet
}

func (c Context) GetOrgID() string        { return c.OrgID }
func (c Context) GetUserID() string       { return c.UserID }
func (c Context) GetUserGroups() []string { return c.UserGroups }
func (c Context) IsAdmin() bool           { return slices.Contains(c.UserGroups, types.GroupAdmin) }
func (c *Context) Validate() error {
	if c.SID == "" ||
		c.ConnectionID == "" || c.ConnectionName == "" || c.ConnectionType == "" ||
		c.AgentID == "" || c.OrgID == "" || c.UserID == "" ||
		c.ClientVerb == "" || c.ClientOrigin == "" {
		return fmt.Errorf("missing required context attributes")
	}
	return nil
}

func (m GenericMap) Get(key string) any { return m[key] }
func (m GenericMap) GetString(key string) string {
	val, ok := m[key]
	if ok {
		return fmt.Sprintf("%v", val)
	}
	return ""
}

func (m GenericMap) Int64(key string) int64 {
	val, ok := m[key]
	if !ok {
		return -1
	}
	v, _ := val.(int64)
	return v
}

func (m GenericMap) GetTime(key string) *time.Time {
	val, ok := m[key]
	if !ok {
		return nil
	}
	return val.(*time.Time)
}

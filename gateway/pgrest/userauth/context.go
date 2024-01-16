package pguserauth

import (
	"slices"

	"github.com/runopsio/hoop/gateway/storagev2/types"
)

type Context struct {
	OrgID       string
	OrgName     string
	UserUUID    string
	UserSubject string
	UserEmail   string
	UserName    string
	UserStatus  string
	UserSlackID string
	UserGroups  []string
}

func (c *Context) IsEmpty() bool           { return c.OrgID == "" && c.UserSubject == "" }
func (c *Context) GetSubject() string      { return c.UserSubject }
func (c *Context) GetOrgID() string        { return c.OrgID }
func (c *Context) GetUserGroups() []string { return c.UserGroups }
func (c *Context) IsAdmin() bool           { return slices.Contains(c.UserGroups, types.GroupAdmin) }

func (c Context) ToAPIContext() *types.APIContext {
	return &types.APIContext{
		OrgID:      c.OrgID,
		OrgName:    c.OrgName,
		UserID:     c.UserSubject,
		UserName:   c.UserName,
		UserEmail:  c.UserEmail,
		UserGroups: c.UserGroups,
		UserStatus: string(c.UserStatus),
		SlackID:    c.UserSlackID,
	}
}

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

	UserAnonSubject       string
	UserAnonProfile       string
	UserAnonEmail         string
	UserAnonEmailVerified *bool
}

// IsEmpty returns true if the user is not logged in and has not signed up yet.
// The user is considered empty if the OrgID and UserSubject is not set.
func (c *Context) IsEmpty() bool { return c.OrgID == "" && c.UserSubject == "" }

// IsAnonymous returns true if the user is not logged in and has not signed up yet.
// The user is considered anonymous if the UserAnonSubject and UserAnonEmail are not set.
func (c *Context) IsAnonymous() bool { return c.UserAnonSubject != "" && c.UserAnonEmail != "" }

// func (c *Context) GetUserEmail() string    { return c.UserEmail }
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

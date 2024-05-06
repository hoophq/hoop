package storagev2

import (
	"slices"

	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/gateway/analytics"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

const ContextKey string = "storagev2"

type Context struct {
	*types.APIContext
	segment *analytics.Segment
}

func ParseContext(c *gin.Context) *Context {
	obj, ok := c.Get(ContextKey)
	if !ok {
		log.Warnf("failed obtaing context from *gin.Context for key %q", ContextKey)
		return &Context{
			APIContext: &types.APIContext{},
			segment:    nil}
	}
	ctx, _ := obj.(*Context)
	if ctx == nil {
		log.Warnf("failed type casting value to *Context")
		return &Context{
			APIContext: &types.APIContext{},
			segment:    nil}
	}
	return ctx
}

func NewContext(userID, orgID string) *Context {
	return &Context{
		APIContext: &types.APIContext{UserID: userID, OrgID: orgID},
		segment:    nil}
}

// NewOrganizationContext returns a context without a user
func NewOrganizationContext(orgID string) *Context {
	return NewContext("", orgID)
}

func (c *Context) WithUserInfo(name, email, status, picture string, groups []string) *Context {
	c.UserName = name
	c.UserEmail = email
	c.UserGroups = groups
	c.UserStatus = status
	c.UserPicture = picture
	return c
}

func (c *Context) WithAnonymousInfo(profileName, email, subject, picture string, emailVerified *bool) *Context {
	c.UserAnonEmail = email
	c.UserAnonProfile = profileName
	c.UserAnonPicture = picture
	c.UserAnonSubject = subject
	c.UserAnonEmailVerified = emailVerified
	return c
}

func (c *Context) WithSlackID(slackID string) *Context {
	c.SlackID = slackID
	return c
}

func (c *Context) WithOrgName(orgName string) *Context {
	c.OrgName = orgName
	return c
}

func (c *Context) WithOrgLicense(orgLicense string) *Context {
	c.OrgLicense = orgLicense
	return c
}

func (c *Context) WithApiURL(apiURL string) *Context {
	c.ApiURL = apiURL
	return c
}

func (c *Context) WithGrpcURL(grpcURL string) *Context {
	c.GrpcURL = grpcURL
	return c
}

func (c *Context) Analytics() *analytics.Segment {
	if c.segment == nil {
		c.segment = analytics.New()
		return c.segment
	}
	return c.segment
}

func (c *Context) GetOrgID() string        { return c.OrgID }
func (c *Context) GetUserID() string       { return c.UserID }
func (c *Context) GetLicenseName() string  { return c.OrgLicense }
func (c *Context) GetUserGroups() []string { return c.UserGroups }
func (c *Context) IsAdmin() bool           { return slices.Contains(c.UserGroups, types.GroupAdmin) }
func (c *Context) GetSubject() string      { return c.UserID }
func (c *Context) IsAnonymous() bool       { return c.UserAnonEmail != "" && c.UserAnonSubject != "" }

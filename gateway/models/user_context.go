package models

import (
	"encoding/json"
	"slices"

	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type UserContext interface {
	GetOrgID() string
	IsAdmin() bool
	GetUserGroups() []string
}

// NewAdminContext creates a new UserContext with admin privileges.
// It should when a component needs to perform actions that require admin access.
func NewAdminContext(orgID string) UserContext {
	return &Context{
		OrgID:      orgID,
		UserGroups: []string{types.GroupAdmin},
	}
}

type Context struct {
	OrgID          string          `gorm:"column:org_id"`
	OrgName        string          `gorm:"column:org_name"`
	OrgLicenseData json.RawMessage `gorm:"column:org_license_data"`
	UserID         string          `gorm:"column:user_id"`
	UserSubject    string          `gorm:"column:user_subject"`
	UserEmail      string          `gorm:"column:user_email"`
	UserName       string          `gorm:"column:user_name"`
	UserStatus     string          `gorm:"column:user_status"`
	UserSlackID    string          `gorm:"column:user_slack_id"`
	UserPicture    string          `gorm:"column:user_picture"`
	UserGroups     pq.StringArray  `gorm:"column:user_groups;type:text[]"`
}

// IsEmpty returns true if the user is not logged in and has not signed up yet.
// The user is considered empty if the OrgID and UserSubject is not set.
func (c *Context) IsEmpty() bool           { return c.OrgID == "" && c.UserSubject == "" }
func (c *Context) GetOrgID() string        { return c.OrgID }
func (c *Context) GetUserGroups() []string { return c.UserGroups }
func (c *Context) IsAdmin() bool           { return slices.Contains(c.UserGroups, types.GroupAdmin) }
func (c *Context) IsAuditor() bool         { return slices.Contains(c.UserGroups, types.GroupAuditor) }

// GetUserContext retrieves user context data based on the subject claim or OIDC information.
//
// After access token verification, it's safe to obtain user context using only the subject attribute.
//
// This method queries both the users and service accounts tables to retrieve the existing user context information.
func GetUserContext(subject string) (*Context, error) {
	var ctx Context
	err := DB.Raw(`
	WITH usr AS (
		SELECT id, org_id, subject, email, name, status::TEXT, slack_id, picture, created_at, updated_at
		FROM private.users
		UNION
		SELECT id, org_id, subject, subject AS email, name, status::TEXT, '', '', created_at, updated_at
		FROM private.service_accounts
	) SELECT
		o.id AS org_id,
		o.name AS org_name,
		o.license_data AS org_license_data,
		u.id AS user_id,
		u.subject AS user_subject,
		u.email AS user_email,
		u.name AS user_name,
		u.status AS user_status,
		u.slack_id AS user_slack_id,
		u.picture AS user_picture,
		COALESCE((
			SELECT array_agg(ug.name::TEXT) FROM private.user_groups ug
			WHERE ug.user_id = u.id OR ug.service_account_id = u.id
		), ARRAY[]::TEXT[]) AS user_groups
	FROM usr u
	JOIN private.orgs o ON u.org_id = o.id
	WHERE u.subject = ?`, subject).
		Scan(&ctx).
		Error
	if err == gorm.ErrRecordNotFound {
		return &Context{}, nil
	}
	return &ctx, err
}

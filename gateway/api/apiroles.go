package api

import "github.com/gin-gonic/gin"

var roleContextKey = "ginrole"

type roleType string

const (
	// RoleAdminOnly must allow only admins to access certain resources
	RoleAdminOnly roleType = "admin-only"
	// RoleFullAccess must grant access to admin, regular and anonymous users
	RoleFullAccess roleType = "full-access"
	// RoleDefaultAccess must grant access to admin and regular users
	RoleDefaultAccess roleType = "default-access"
)

// AdminOnlyAccessPermission is a middleware that checks if the user has admin access.
func AdminOnlyAccessRole(c *gin.Context) {
	c.Set(roleContextKey, RoleAdminOnly)
	c.Next()
}

// FullAccessRole grants access to admin, regular and anonymous users
func FullAccessRole(c *gin.Context) {
	c.Set(roleContextKey, RoleFullAccess)
	c.Next()
}

// StandardAccessRole grants access to admin and regular users
func StandardAccessRole(c *gin.Context) {
	c.Set(roleContextKey, RoleDefaultAccess)
	c.Next()
}

// RoleFromContext returns the role from the given context.
func RoleFromContext(c *gin.Context) roleType {
	obj, ok := c.Get(roleContextKey)
	if !ok {
		return ""
	}
	role, _ := obj.(roleType)
	return role
}

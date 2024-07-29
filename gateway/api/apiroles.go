package api

import (
	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/openapi"
)

var roleContextKey = "ginrole"

// AdminOnlyAccessPermission is a middleware that checks if the user has admin access.
func AdminOnlyAccessRole(c *gin.Context) {
	c.Set(roleContextKey, openapi.RoleAdminType)
	c.Next()
}

// AnonAccessRole grants access to admin, regular and anonymous users
func AnonAccessRole(c *gin.Context) {
	c.Set(roleContextKey, openapi.RoleUnregisteredType)
	c.Next()
}

// StandardAccessRole grants access to admin and regular users
func StandardAccessRole(c *gin.Context) {
	c.Set(roleContextKey, openapi.RoleStandardType)
	c.Next()
}

// RoleFromContext returns the role from the given context.
func RoleFromContext(c *gin.Context) openapi.RoleType {
	obj, ok := c.Get(roleContextKey)
	if !ok {
		return ""
	}
	role, _ := obj.(openapi.RoleType)
	return role
}

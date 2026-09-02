package apiroutes

import (
	"slices"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

const roleContextKey string = "hoop-roles"

func rolesFromContext(c *gin.Context) []openapi.RoleType {
	obj, ok := c.Get(roleContextKey)
	if !ok {
		return nil
	}
	roles, _ := obj.([]openapi.RoleType)
	return roles
}

// privilegedRoles maps a reserved group name to the route role it satisfies.
var privilegedRoles = map[string]openapi.RoleType{
	types.GroupAuditor:  openapi.RoleAuditorType,
	types.GroupApprover: openapi.RoleApproverType,
}

// isGroupAllowed validates if the groups of a user is allowed to access a route
func isGroupAllowed(userGroups []string, roleNames ...openapi.RoleType) (valid bool) {
	if slices.Contains(userGroups, types.GroupAdmin) {
		// admin can access any route
		return true
	}

	// Every privileged group is weighed, not just the first one: a user who is
	// both auditor and approver must pass a route that names either.
	var isPrivileged bool
	for _, groupName := range userGroups {
		role, ok := privilegedRoles[groupName]
		if !ok {
			continue
		}
		isPrivileged = true
		if slices.Contains(roleNames, role) {
			return true
		}
	}
	// A privileged group does not also inherit standard access.
	if isPrivileged {
		return false
	}

	// this condition matches against a privileged access
	// and maintain the default behavior of allowing access to regular users
	// that doesn't belong to any group.
	//
	// if a route doesn't have any role, it's also a standard access
	return len(roleNames) == 0 || slices.Contains(roleNames, openapi.RoleStandardType)
}

// AdminOnlyAccessRole allows only admin users to access this role
func AdminOnlyAccessRole(c *gin.Context) {
	c.Set(roleContextKey, []openapi.RoleType{openapi.RoleAdminType})
	c.Next()
}

// AdminAndAuditorAccessRole allows admin and auditor users to access this route
func AdminAndAuditorAccessRole(c *gin.Context) {
	c.Set(roleContextKey, []openapi.RoleType{openapi.RoleAdminType, openapi.RoleAuditorType})
	c.Next()
}

// AdminAndApproverAccessRole allows admin and approver users to access this route
func AdminAndApproverAccessRole(c *gin.Context) {
	c.Set(roleContextKey, []openapi.RoleType{openapi.RoleAdminType, openapi.RoleApproverType})
	c.Next()
}

// ReadOnlyAccessRole allows standard, admin, auditor and approver roles to access it.
// It guards /userinfo, /serverinfo and /feature-flags, which every session needs to boot.
func ReadOnlyAccessRole(c *gin.Context) {
	c.Set(roleContextKey, []openapi.RoleType{
		openapi.RoleStandardType,
		openapi.RoleAuditorType,
		openapi.RoleApproverType,
	})
	c.Next()
}

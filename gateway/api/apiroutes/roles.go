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

// isGroupAllowed validates if the groups of a user is allowed to access a route
func isGroupAllowed(userGroups []string, roleNames ...openapi.RoleType) (valid bool) {
	if slices.Contains(userGroups, types.GroupAdmin) {
		// admin can access any route
		return true
	}

	// it performs validation of route based roles
	// in case the group exists it must match against a route role
	for _, groupName := range userGroups {
		switch groupName {
		case types.GroupAuditor:
			// auditor can access only assigned route roles
			return slices.Contains(roleNames, openapi.RoleAuditorType)
		}
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

// ReadOnlyAccessRole allows standard, admin and auditor roles to access it
func ReadOnlyAccessRole(c *gin.Context) {
	c.Set(roleContextKey, []openapi.RoleType{
		openapi.RoleStandardType,
		openapi.RoleAuditorType,
	})
	c.Next()
}

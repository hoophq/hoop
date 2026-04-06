package apiroutes

import (
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

const (
	roleContextKey          string = "hoop-roles"
	excludeAuditorContextKey string = "hoop-exclude-auditor"
)

func rolesFromContext(c *gin.Context) []openapi.RoleType {
	obj, ok := c.Get(roleContextKey)
	if !ok {
		return nil
	}
	roles, _ := obj.([]openapi.RoleType)
	return roles
}

func isAuditorExcluded(c *gin.Context) bool {
	v, _ := c.Get(excludeAuditorContextKey)
	excluded, _ := v.(bool)
	return excluded
}

// isGroupAllowed validates if the groups of a user is allowed to access a route.
// The httpMethod is used to grant auditors read-only access to all routes unless
// the route is explicitly excluded via ExcludeAuditorRole.
func isGroupAllowed(httpMethod string, excludeAuditor bool, userGroups []string, roleNames ...openapi.RoleType) (valid bool) {
	if slices.Contains(userGroups, types.GroupAdmin) {
		return true
	}

	for _, groupName := range userGroups {
		switch groupName {
		case types.GroupAuditor:
			if excludeAuditor {
				return false
			}
			// auditors have read-only (GET/HEAD) access to all routes
			return httpMethod == http.MethodGet || httpMethod == http.MethodHead
		}
	}

	// maintain the default behavior of allowing access to regular users
	// that don't belong to any group.
	// if a route doesn't have any role, it's also a standard access.
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

// ExcludeAuditorRole denies auditor access to the route even for read-only requests
func ExcludeAuditorRole(c *gin.Context) {
	c.Set(excludeAuditorContextKey, true)
	c.Next()
}

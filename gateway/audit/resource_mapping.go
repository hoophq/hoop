package audit

import (
	"strings"
)

// PathMapping maps URL paths to ResourceType and Action
type PathMapping struct {
	ResourceType ResourceType
	Action       Action
}

// DeriveResourceAndAction automatically derives the resource type and action from HTTP path and method
func DeriveResourceAndAction(path, method string) (ResourceType, Action) {
	resourceType := deriveResourceType(path)
	action := deriveAction(method)
	return resourceType, action
}

// deriveResourceType extracts the resource type from the URL path
// Examples:
//   /api/users/123 -> ResourceUser
//   /api/connections -> ResourceConnection
func deriveResourceType(path string) ResourceType {
	// Remove trailing slash
	path = strings.TrimSuffix(path, "/")
	
	// Split path into segments
	parts := strings.Split(path, "/")
	
	// Find the resource segment (usually after /api/)
	for i, part := range parts {
		if part == "api" && i+1 < len(parts) {
			resourceSegment := parts[i+1]
			
			// Map path segments to ResourceType constants
			switch resourceSegment {
			case "users":
				// Check if it's a user group operation
				if i+2 < len(parts) && parts[i+2] == "groups" {
					return ResourceUserGroup
				}
				return ResourceUser
			case "connections":
				return ResourceConnection
			case "agents":
				return ResourceAgent
			case "resources":
				return ResourceResource
			case "guardrails":
				return ResourceGuardrails
			case "datamasking", "data-masking":
				return ResourceDataMasking
			case "serviceaccounts", "service-accounts":
				return ResourceServiceAccount
			case "serverconfig", "server-config":
				return ResourceServerConfig
			case "authconfig", "auth-config":
				return ResourceAuthConfig
			case "orgkeys", "org-keys":
				return ResourceOrgKey
			}
			
			// Return the segment as-is if no mapping found
			return ResourceType(resourceSegment)
		}
	}
	
	// Default fallback
	return ResourceType("unknown")
}

// deriveAction maps HTTP methods to audit actions
func deriveAction(method string) Action {
	switch strings.ToUpper(method) {
	case "POST":
		return ActionCreate
	case "PUT", "PATCH":
		return ActionUpdate
	case "DELETE":
		return ActionDelete
	default:
		// For GET, HEAD, OPTIONS, etc., we typically don't audit
		// But if we do, we can use a default action
		return Action("read")
	}
}

// ShouldAudit determines if a request should be audited based on method and path
func ShouldAudit(method, path string) bool {
	// Skip read operations (GET, HEAD, OPTIONS)
	if method == "GET" || method == "HEAD" || method == "OPTIONS" {
		return false
	}
	
	// Skip health check endpoints
	if strings.Contains(path, "/healthz") || strings.Contains(path, "/health") {
		return false
	}
	
	// Skip metrics endpoints
	if strings.Contains(path, "/metrics") {
		return false
	}
	
	// Skip public endpoints that don't require audit
	if strings.Contains(path, "/login") || strings.Contains(path, "/signup") {
		return false
	}
	
	// Audit all write operations (POST, PUT, PATCH, DELETE)
	return method == "POST" || method == "PUT" || method == "PATCH" || method == "DELETE"
}

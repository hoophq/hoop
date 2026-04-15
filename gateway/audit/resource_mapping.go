package audit

import (
	"strings"
)

// resourceRoute is a node in a prefix tree of path segments.
type resourceRoute struct {
	resource ResourceType              // matched resource at this node (empty = no match here)
	children map[string]*resourceRoute // next segment → child node
}

// root builds the routing tree. Each path from root to a node with a non-empty
// resource defines a mapping. Add new routes here.
var root = buildRoutes([]struct {
	segments []string
	resource ResourceType
}{
	{[]string{"users", "groups"}, ResourceUserGroup},
	{[]string{"users"}, ResourceUser},
	{[]string{"connections"}, ResourceConnection},
	{[]string{"agents"}, ResourceAgent},
	{[]string{"resources"}, ResourceResource},
	{[]string{"guardrails"}, ResourceGuardrails},
	{[]string{"datamasking"}, ResourceDataMasking},
	{[]string{"data-masking"}, ResourceDataMasking},
	{[]string{"serviceaccounts"}, ResourceServiceAccount},
	{[]string{"service-accounts"}, ResourceServiceAccount},
	{[]string{"machineidentities"}, ResourceServiceAccount},
	{[]string{"machine-identities"}, ResourceServiceAccount},
	{[]string{"serverconfig"}, ResourceServerConfig},
	{[]string{"server-config"}, ResourceServerConfig},
	{[]string{"authconfig"}, ResourceAuthConfig},
	{[]string{"auth-config"}, ResourceAuthConfig},
	{[]string{"orgkeys"}, ResourceOrgKey},
	{[]string{"org-keys"}, ResourceOrgKey},
})

func buildRoutes(entries []struct {
	segments []string
	resource ResourceType
}) *resourceRoute {
	r := &resourceRoute{children: make(map[string]*resourceRoute)}
	for _, e := range entries {
		node := r
		for _, seg := range e.segments {
			child, ok := node.children[seg]
			if !ok {
				child = &resourceRoute{children: make(map[string]*resourceRoute)}
				node.children[seg] = child
			}
			node = child
		}
		node.resource = e.resource
	}
	return r
}

var methodToAction = map[string]Action{
	"POST":   ActionCreate,
	"PUT":    ActionUpdate,
	"PATCH":  ActionUpdate,
	"DELETE": ActionDelete,
}

var skipPaths = []string{"/healthz", "/health", "/metrics", "/login", "/signup"}

func DeriveResourceAndAction(path, method string) (ResourceType, Action) {
	return deriveResourceType(path), deriveAction(method)
}

// deriveResourceType walks the path segments after "/api/" through the route
// tree, greedily matching the longest known prefix. Segments that look like
// in the future if you want add IDs (e.g. UUIDs, numeric IDs) are skipped so that
// /api/users/123/groups/456/events still resolves correctly.
// just need to add {[]string{"users", "groups", "events"}, ResourceUserGroupEvent}, in the list buildroutes
func deriveResourceType(path string) ResourceType {
	parts := strings.Split(strings.TrimSuffix(path, "/"), "/")

	// Find the "api" segment.
	start := -1
	for i, p := range parts {
		if p == "api" {
			start = i + 1
			break
		}
	}
	if start == -1 || start >= len(parts) {
		return ResourceType("unknown")
	}

	var best ResourceType
	node := root

	for _, seg := range parts[start:] {
		child, ok := node.children[seg]
		if !ok {
			// Skip segments that aren't in the tree (IDs, etc.)
			continue
		}
		node = child
		if node.resource != "" {
			best = node.resource
		}
	}

	if best != "" {
		return best
	}
	// Fall back to the first segment after /api/ as a raw resource name.
	return ResourceType(parts[start])
}

func deriveAction(method string) Action {
	if a, ok := methodToAction[strings.ToUpper(method)]; ok {
		return a
	}
	return Action("read")
}

func ShouldAudit(method, path string) bool {
	if _, isWrite := methodToAction[strings.ToUpper(method)]; !isWrite {
		return false
	}
	for _, s := range skipPaths {
		if strings.Contains(path, s) {
			return false
		}
	}
	return true
}

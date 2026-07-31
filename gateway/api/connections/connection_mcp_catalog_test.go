package apiconnections

import (
	"testing"
)

// The catalog is what the connection form's server picker renders. An empty
// or malformed embedded catalog would leave the picker blank with no error
// visible to the admin, so assert the conversion produces usable entries.
func TestMCPCatalogEntriesArePreFillable(t *testing.T) {
	entries, err := mcpCatalog()
	if err != nil {
		t.Fatalf("loading mcp catalog: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("mcp catalog is empty; the connection form picker would render nothing")
	}

	validTransport := map[string]bool{"streamable-http": true, "sse": true}
	validAuth := map[string]bool{"none": true, "static": true, "oauth": true}
	seen := map[string]bool{}
	for _, e := range entries {
		if e.Name == "" {
			t.Fatal("catalog entry has no name")
		}
		if seen[e.Name] {
			t.Fatalf("duplicate catalog entry %q", e.Name)
		}
		seen[e.Name] = true

		// Every field below is written straight into a connection env var, so
		// a value the agent rejects (validateMCPProxyEnv) must not reach the
		// form as a pre-fill.
		if e.URL == "" {
			t.Fatalf("entry %q has no url; REMOTE_URL would be empty", e.Name)
		}
		if !validTransport[e.Transport] {
			t.Fatalf("entry %q has transport %q, which the agent rejects as MCP_TRANSPORT", e.Name, e.Transport)
		}
		if !validAuth[e.Auth] {
			t.Fatalf("entry %q has auth %q, which is not a valid MCP_AUTH", e.Name, e.Auth)
		}
	}
}

// Sorted output keeps the picker order stable across requests; the underlying
// catalog is a map, whose iteration order is randomized.
func TestMCPCatalogIsSortedByName(t *testing.T) {
	entries, err := mcpCatalog()
	if err != nil {
		t.Fatalf("loading mcp catalog: %v", err)
	}
	for i := 1; i < len(entries); i++ {
		if entries[i-1].Name > entries[i].Name {
			t.Fatalf("catalog not sorted: %q precedes %q", entries[i-1].Name, entries[i].Name)
		}
	}
}

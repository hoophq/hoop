package apiconnections

import (
	"strings"
	"testing"

	"github.com/hoophq/mcpproxy/catalog"
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

// Every mode the form can offer must be one the agent accepts, and the
// documented default must come first: the form seeds its selection from
// AuthModes[0].
func TestMCPCatalogAuthModesAreUsable(t *testing.T) {
	entries, err := mcpCatalog()
	if err != nil {
		t.Fatalf("loading mcp catalog: %v", err)
	}
	validAuth := map[string]bool{"none": true, "static": true, "oauth": true}
	for _, e := range entries {
		if len(e.AuthModes) == 0 {
			t.Fatalf("entry %q offers no auth mode; the form would render no way to authenticate", e.Name)
		}
		if e.AuthModes[0] != e.Auth {
			t.Fatalf("entry %q lists %v first, want the documented default %q",
				e.Name, e.AuthModes, e.Auth)
		}
		seen := map[string]bool{}
		for _, m := range e.AuthModes {
			if !validAuth[m] {
				t.Fatalf("entry %q offers auth mode %q, which is not a valid MCP_AUTH", e.Name, m)
			}
			if seen[m] {
				t.Fatalf("entry %q lists auth mode %q twice", e.Name, m)
			}
			seen[m] = true
		}
	}
}

// A second mode is offered only where the provider documents one. Offering
// OAuth on a server that publishes no authorization server sends the admin
// into RFC 9728 discovery that cannot resolve, and the failure only surfaces
// after they click through a login they cannot finish.
func TestMCPCatalogOffersASecondModeOnlyWhereDocumented(t *testing.T) {
	entries, err := mcpCatalog()
	if err != nil {
		t.Fatalf("loading mcp catalog: %v", err)
	}
	for _, e := range entries {
		_, extra := extraAuthModes[e.Name]
		if got, want := len(e.AuthModes) > 1, extra; got != want {
			t.Fatalf("entry %q offers modes %v (multi=%v), want multi=%v",
				e.Name, e.AuthModes, got, want)
		}
	}
}

// extraAuthModes is the machine-readable half of a fact the catalog states in
// prose. If a provider's notes stop advertising the second credential, or a
// server is renamed out from under the table, the table is wrong and the form
// offers a flow that no longer works.
func TestExtraAuthModesMatchTheirCatalogNotes(t *testing.T) {
	cat, err := catalog.Builtin()
	if err != nil {
		t.Fatalf("loading catalog: %v", err)
	}
	// Phrases the notes use to advertise each mode. Loose on purpose: the
	// test guards against a note that stops mentioning the credential at all,
	// not against rewording.
	advertises := map[string][]string{
		"oauth":  {"oauth"},
		"static": {"api key", "token", "bearer"},
		"none":   {"anonymous", "optional", "without"},
	}
	for name, extra := range extraAuthModes {
		e, ok := cat.Servers[name]
		if !ok {
			t.Fatalf("extraAuthModes names %q, which is not in the catalog", name)
		}
		if extra == e.Auth {
			t.Fatalf("%q lists %q as an extra mode, but that is already its default", name, extra)
		}
		notes := strings.ToLower(e.Notes)
		if notes == "" {
			t.Fatalf("%q is marked multi-mode but its entry documents no second credential", name)
		}
		var advertised bool
		for _, phrase := range advertises[extra] {
			if strings.Contains(notes, phrase) {
				advertised = true
				break
			}
		}
		if !advertised {
			t.Fatalf("%q is marked as also accepting %q, but its notes no longer say so: %q",
				name, extra, e.Notes)
		}
	}
}

// The catalog states a second credential in prose for exactly these servers.
// A new dual-mode entry that nobody adds to extraAuthModes silently loses its
// second mode in the form, which is the failure this catches: the notes are
// the source, the table is the copy.
func TestEveryNoteAdvertisingASecondModeIsInTheTable(t *testing.T) {
	cat, err := catalog.Builtin()
	if err != nil {
		t.Fatalf("loading catalog: %v", err)
	}
	for name, e := range cat.Servers {
		if e.Notes == "" {
			continue
		}
		notes := strings.ToLower(e.Notes)
		// A note that mentions a credential flow other than the entry's own
		// mode is advertising a second one.
		var suggests string
		switch {
		case e.Auth != "oauth" && strings.Contains(notes, "oauth"):
			suggests = "oauth"
		case e.Auth != "static" && (strings.Contains(notes, "api key") ||
			strings.Contains(notes, "bearer")):
			suggests = "static"
		case e.Auth != "none" && strings.Contains(notes, "anonymous"):
			suggests = "none"
		}
		if suggests == "" {
			continue
		}
		if got := extraAuthModes[name]; got != suggests {
			t.Fatalf("%q notes advertise %q (%q) but extraAuthModes says %q; "+
				"the form would not offer it",
				name, suggests, e.Notes, got)
		}
	}
}

// A static mode needs somewhere to put the token. Entries that name no header
// fall back to Authorization, which is only correct for a bearer credential —
// assert the ones that need a specific name still carry it.
func TestStaticCapableEntriesCanCarryACredential(t *testing.T) {
	entries, err := mcpCatalog()
	if err != nil {
		t.Fatalf("loading mcp catalog: %v", err)
	}
	for _, e := range entries {
		staticCapable := false
		for _, m := range e.AuthModes {
			if m == "static" {
				staticCapable = true
			}
		}
		if !staticCapable {
			continue
		}
		// Either the entry names its header, or the bearer default applies.
		if e.Header != "" && !strings.Contains(e.Header, ":") {
			t.Fatalf("entry %q has header %q, which carries no name; the token would be sent under a garbage header",
				e.Name, e.Header)
		}
	}
}

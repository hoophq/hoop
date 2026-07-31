// MCP server catalog (ADR-0004).
//
// mcpproxy embeds a curated list of publicly hosted remote MCP servers
// (Linear, Stripe, Notion, …) with the endpoint, transport and auth mode each
// one needs. Exposing it here lets the connection create page open with a
// server picker that pre-fills REMOTE_URL / MCP_TRANSPORT / MCP_AUTH instead
// of asking an admin to look those values up; "custom" falls back to the raw
// form.
//
// The catalog is static build-time data, so the response is assembled once and
// reused. It carries no tenant data, which is why the endpoint is read-only
// role and not admin-gated like the OAuth flow beside it.
package apiconnections

import (
	"net/http"
	"sort"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/mcpproxy/catalog"
)

// mcpCatalogOnce guards the one-time conversion of the embedded catalog into
// the API representation. Builtin() re-parses the embedded YAML on every call.
var (
	mcpCatalogOnce    sync.Once
	mcpCatalogEntries []openapi.MCPCatalogEntry
	mcpCatalogErr     error
)

// ListMCPCatalog
//
//	@Summary		List MCP Server Catalog
//	@Description	List the built-in catalog of publicly hosted remote MCP servers. Used by the connection create page to pre-fill an `mcpproxy` connection's endpoint, transport and auth mode from a picker.
//	@Tags			Connections
//	@Produce		json
//	@Success		200	{array}		openapi.MCPCatalogEntry
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/mcp-catalog [get]
func ListMCPCatalog(c *gin.Context) {
	entries, err := mcpCatalog()
	if err != nil {
		log.Errorf("failed loading mcp server catalog, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed loading mcp server catalog"})
		return
	}
	c.JSON(http.StatusOK, entries)
}

// extraAuthModes names the catalog servers that accept a mode beyond the one
// their entry records, and which mode that is.
//
// The catalog schema carries a single `auth` per server, so a provider that
// takes more than one credential can only record its default. The rest of
// that fact lives in the entry's free-text `notes`:
//
//	context7     auth: static  "anonymous access works with reduced rate limits"
//	github       auth: static  "OAuth also supported; a fine-grained PAT is ..."
//	huggingface  auth: none    "optional: Authorization: Bearer ${HF_TOKEN} ..."
//	linear       auth: oauth   "personal API keys also work as a static bearer"
//	stripe       auth: static  "use a restricted API key; OAuth also supported"
//
// Those notes are written for humans, and parsing English to decide which
// credential flow to offer would break on the next wording change. This table
// is the machine-readable half, kept beside the test that holds it to the
// notes it came from.
//
// Servers absent here support exactly one mode. That matters: offering an
// OAuth login for google-maps sends the admin into RFC 9728 discovery against
// an endpoint that publishes no authorization server, and the dead end only
// shows up after they click.
var extraAuthModes = map[string]string{
	"context7":    "none",
	"github":      "oauth",
	"huggingface": "static",
	"linear":      "static",
	"stripe":      "oauth",
}

// authModesFor lists every mode a server accepts, documented default first so
// the form can seed its selection from AuthModes[0].
func authModesFor(name string, e catalog.Entry) []string {
	modes := []string{e.Auth}
	if extra, ok := extraAuthModes[name]; ok && extra != e.Auth {
		modes = append(modes, extra)
	}
	return modes
}

// mcpCatalog converts the embedded catalog into the API shape, sorted by name
// so the picker renders in a stable order. The catalog's own types carry only
// yaml tags, so the conversion is explicit rather than a re-marshal.
func mcpCatalog() ([]openapi.MCPCatalogEntry, error) {
	mcpCatalogOnce.Do(func() {
		cat, err := catalog.Builtin()
		if err != nil {
			mcpCatalogErr = err
			return
		}
		entries := make([]openapi.MCPCatalogEntry, 0, len(cat.Servers))
		for name, e := range cat.Servers {
			entries = append(entries, openapi.MCPCatalogEntry{
				Name:        name,
				Description: e.Description,
				URL:         e.URL,
				Transport:   e.Transport,
				Auth:        e.Auth,
				AuthModes:   authModesFor(name, e),
				Header:      e.Header,
				Notes:       e.Notes,
			})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		mcpCatalogEntries = entries
	})
	return mcpCatalogEntries, mcpCatalogErr
}

import { isFreeFormCustomSubtype } from '@/utils/connectionPolicy'
import CatalogRenderer from '@/pages/Roles/Configure/sections/credentials/CatalogRenderer'
import SshRenderer from '@/pages/Roles/Configure/sections/credentials/SshRenderer'
import ClaudeCodeRenderer from '@/pages/Roles/Configure/sections/credentials/ClaudeCodeRenderer'
import HttpProxyRenderer from '@/pages/Roles/Configure/sections/credentials/HttpProxyRenderer'
import McpRenderer from '@/pages/Roles/Configure/sections/credentials/McpRenderer'
import McpProxyRenderer from '@/pages/Roles/Configure/sections/credentials/McpProxyRenderer'
import KubernetesTokenRenderer from '@/pages/Roles/Configure/sections/credentials/KubernetesTokenRenderer'
import FreeFormCustomRenderer from '@/pages/Roles/Configure/sections/credentials/FreeFormCustomRenderer'

// Dispatch table — order matters: the first matching rule wins. Each
// rule declares whether its renderer depends on connections-metadata.json
// (drives the loader gate in CredentialsTab) and how it renders.
//
// Adding a new bespoke connection shape: create a renderer file
// alongside the existing five, append an entry here, mark
// `requiresCatalog: false`. Removing one: delete both. This is the
// only place routing is encoded — `pickRendererRule` is the single
// source of truth.
//
// The catalog rule splits matching from rendering: `matches` is
// type-only so the loader gate can fire before the JSON arrives;
// `render` does the schema lookup and falls back to free-form when
// the subtype isn't in the catalog (legacy custom/cloudwatch case).
const RENDERER_RULES = [
  {
    name: 'application-ssh',
    requiresCatalog: false,
    // ssh-local is plain SSH running on the agent host; the same
    // renderer serves both subtypes and owns the proxy/local toggle.
    matches: (c) =>
      c.type === 'application' &&
      (c.subtype === 'ssh' || c.subtype === 'ssh-local'),
    render: (props) => <SshRenderer {...props} />,
  },
  {
    name: 'httpproxy-claude-code',
    requiresCatalog: false,
    matches: (c) => c.type === 'httpproxy' && c.subtype === 'claude-code',
    render: (props) => <ClaudeCodeRenderer {...props} />,
  },
  {
    name: 'httpproxy-mcp',
    requiresCatalog: false,
    matches: (c) => c.type === 'httpproxy' && c.subtype === 'mcp',
    render: (props) => <McpRenderer {...props} />,
  },
  {
    // MCP Gateway — the protocol-aware MCP type (ADR-0004), distinct from
    // the legacy `mcp` byte relay above. It must be matched before the
    // httpproxy catch-all: falling through renders a Remote URL and a
    // headers list, which hides the transport, tool policy, limits and
    // stdio child environment that are the whole point of the subtype, and
    // marks REMOTE_URL required on a stdio connection that has none.
    name: 'httpproxy-mcpproxy',
    requiresCatalog: false,
    matches: (c) => c.type === 'httpproxy' && c.subtype === 'mcpproxy',
    render: (props) => <McpProxyRenderer {...props} />,
  },
  {
    name: 'httpproxy-generic',
    requiresCatalog: false,
    matches: (c) => c.type === 'httpproxy',
    render: (props) => <HttpProxyRenderer {...props} />,
  },
  {
    name: 'custom-kubernetes-token',
    requiresCatalog: false,
    matches: (c) => c.type === 'custom' && c.subtype === 'kubernetes-token',
    render: (props) => <KubernetesTokenRenderer {...props} />,
  },
  {
    name: 'custom-linux-vm',
    requiresCatalog: false,
    matches: (c) => c.type === 'custom' && c.subtype === 'linux-vm',
    render: (props) => <FreeFormCustomRenderer {...props} />,
  },
  // Catalog: matches any non-bespoke shape that could carry a schema.
  // Schema lookup happens in render(); a missing schema for
  // type=custom falls through to free-form so the user can still see
  // and edit their envvars (legacy `custom/cloudwatch`).
  {
    name: 'catalog',
    requiresCatalog: true,
    matches: (c) => {
      if (c.type === 'database') return true
      if (
        c.type === 'application' &&
        c.subtype &&
        c.subtype !== 'ssh' &&
        c.subtype !== 'ssh-local'
      ) return true
      if (
        c.type === 'custom' &&
        c.subtype &&
        !isFreeFormCustomSubtype(c.subtype)
      ) return true
      return false
    },
    render: (props, { getSchema }) => {
      const fields = getSchema(props.connection.subtype)
      if (fields) return <CatalogRenderer {...props} fields={fields} />
      if (props.connection.type === 'custom') {
        return <FreeFormCustomRenderer {...props} />
      }
      return null
    },
  },
  // Custom catch-all: empty subtype + the legacy free-form exclusion
  // list (custom/tcp, custom/ssh, custom/httpproxy, custom/claude-code).
  {
    name: 'custom-freeform',
    requiresCatalog: false,
    matches: (c) => c.type === 'custom',
    render: (props) => <FreeFormCustomRenderer {...props} />,
  },
]

export function pickRendererRule(connection) {
  if (!connection) return null
  return RENDERER_RULES.find((r) => r.matches(connection)) || null
}

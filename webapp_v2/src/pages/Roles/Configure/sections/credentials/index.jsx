import { isFreeFormCustomSubtype } from '@/utils/connectionPolicy'
import CatalogRenderer from './CatalogRenderer'
import SshRenderer from './SshRenderer'
import ClaudeCodeRenderer from './ClaudeCodeRenderer'
import HttpProxyRenderer from './HttpProxyRenderer'
import KubernetesTokenRenderer from './KubernetesTokenRenderer'
import FreeFormCustomRenderer from './FreeFormCustomRenderer'

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
    matches: (c) => c.type === 'application' && c.subtype === 'ssh',
    render: (props) => <SshRenderer {...props} />,
  },
  {
    name: 'httpproxy-claude-code',
    requiresCatalog: false,
    matches: (c) => c.type === 'httpproxy' && c.subtype === 'claude-code',
    render: (props) => <ClaudeCodeRenderer {...props} />,
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
      if (c.type === 'application' && c.subtype && c.subtype !== 'ssh') return true
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

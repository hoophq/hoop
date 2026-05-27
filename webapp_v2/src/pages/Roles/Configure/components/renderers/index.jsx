import { isFreeFormCustomSubtype } from '@/utils/connectionPolicy'
import CatalogRenderer from './CatalogRenderer'
import SshRenderer from './SshRenderer'
import ClaudeCodeRenderer from './ClaudeCodeRenderer'
import HttpProxyRenderer from './HttpProxyRenderer'
import KubernetesTokenRenderer from './KubernetesTokenRenderer'
import FreeFormCustomRenderer from './FreeFormCustomRenderer'

// Dispatch table — order matters: the first matching entry wins.
//
// Mirrors the CLJS dispatch in credentials_tab.cljs (each branch maps
// to its own form id there) and the backend's `shouldRoundTripSecrets`
// rule. Add a new bespoke connection shape by creating a renderer file
// alongside the existing five and appending an entry here.
//
// `getSchema(subtype)` looks up the catalog credential schema from the
// metadata store. `CatalogRenderer` depends on it; the other renderers
// own their schemas inline because the catalog either omits them
// (claude-code, linux-vm, kubernetes-token) or doesn't capture the full
// shape they need (ssh's auth-method toggle, httpproxy's HTTP headers).
export function buildRenderers(getSchema) {
  return [
    // 1. application/ssh → bespoke (auth-method radio drives field set).
    {
      name: 'application-ssh',
      match: (c) => c.type === 'application' && c.subtype === 'ssh',
      render: (props) => <SshRenderer {...props} />,
    },

    // 2. httpproxy/claude-code → bespoke (Anthropic URL + API Key + HTTP headers).
    {
      name: 'httpproxy-claude-code',
      match: (c) => c.type === 'httpproxy' && c.subtype === 'claude-code',
      render: (props) => <ClaudeCodeRenderer {...props} />,
    },

    // 3. httpproxy/* → bespoke (REMOTE_URL + HTTP headers + insecure SSL).
    {
      name: 'httpproxy-generic',
      match: (c) => c.type === 'httpproxy',
      render: (props) => <HttpProxyRenderer {...props} />,
    },

    // 4. custom/kubernetes-token → bespoke (Bearer prefix + cluster URL).
    {
      name: 'custom-kubernetes-token',
      match: (c) => c.type === 'custom' && c.subtype === 'kubernetes-token',
      render: (props) => <KubernetesTokenRenderer {...props} />,
    },

    // 5. custom/linux-vm → free-form (env vars + config files + command).
    {
      name: 'custom-linux-vm',
      match: (c) => c.type === 'custom' && c.subtype === 'linux-vm',
      render: (props) => <FreeFormCustomRenderer {...props} />,
    },

    // 6. Catalog-driven write-only — any connection whose subtype has
    // a credentials schema in connections-metadata.json. The CLJS
    // legacy exclusion list (`isFreeFormCustomSubtype`) only takes
    // effect for type=custom — application/tcp etc. still render via
    // the catalog.
    {
      name: 'catalog',
      match: (c) => {
        if (!getSchema(c.subtype)) return false
        if (c.type === 'custom' && isFreeFormCustomSubtype(c.subtype)) return false
        return true
      },
      render: (props) => (
        <CatalogRenderer
          {...props}
          fields={getSchema(props.connection.subtype)}
        />
      ),
    },

    // 7. Custom fallback: catches custom/(empty subtype), the legacy
    // free-form exclusion subtypes (custom/tcp etc.), and custom
    // subtypes the catalog doesn't know about (legacy cloudwatch).
    // Diverges from CLJS, which would render nil on the catalog branch
    // when schema is absent; the free-form editor at least lets the
    // user inspect the existing envvars instead of staring at a blank
    // form.
    {
      name: 'custom-freeform',
      match: (c) => c.type === 'custom',
      render: (props) => <FreeFormCustomRenderer {...props} />,
    },
  ]
}

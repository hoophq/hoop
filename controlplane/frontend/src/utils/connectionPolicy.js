// Connection-shape policies that the React frontend needs but
// connections-metadata.json doesn't carry. CLJS keeps the equivalent
// rules hardcoded too (see references inline).
//
// Consolidated here so the next person who wants to push these into the
// JSON metadata sees the whole set in one place. Adding a new policy
// flag? Add it here, not scattered across page components.

// Every subtype the gateway proxies over HTTP. Mirrors the CLJS
// http-proxy-subtypes set at
// webapp/src/webapp/resources/constants.cljs:24.
const HTTP_PROXY_SUBTYPES = new Set([
  'httpproxy',
  'kibana',
  'grafana',
  'claude-code',
  'mcp',
  'mcpproxy',
])

// Which connections can launch the native-client flow. Mirrors
// can-access-native-client? in webapp/src/webapp/resources/helpers.cljs, which
// in turn reproduces the gateway's validConnectionTypes allowlist
// (gateway/api/connections/connection_credentials.go).
const NATIVE_CLIENT_DIRECT_SUBTYPES = new Set([
  'postgres',
  'ssh',
  'ssh-local',
  'github',
  'git',
])
// Matches HTTP_PROXY_SUBTYPES above, and must: the gateway's
// validConnectionTypes allowlist now carries "mcp" AND "mcpproxy"
// (connection_credentials.go), so both can be served natively and both belong
// in the drawer. mcpproxy is the protocol-aware MCP type main introduced.
const NATIVE_CLIENT_HTTP_PROXY_SUBTYPES = new Set([
  'httpproxy',
  'kibana',
  'grafana',
  'claude-code',
  'mcp',
  'mcpproxy',
])
const NATIVE_CLIENT_CUSTOM_SUBTYPES = new Set(['rdp', 'aws-ssm'])
const NATIVE_CLIENT_KUBERNETES_SUBTYPES = new Set([
  'kubernetes-token',
  'kubernetes',
  'kubernetes-eks',
])

/**
 * Whether the connection's protocol can be served natively at all, ignoring
 * whether the org currently allows it.
 *
 * Split out from canAccessNativeClient so the Native Connections drawer can
 * keep listing a role whose access_mode_connect was switched off while the user
 * still holds a live credential — otherwise the only way to disconnect it
 * would disappear.
 *
 * `postgresProxyEnabled` comes from /serverinfo. Without a Postgres proxy
 * listen address the gateway cannot serve a native postgres session. Defaults
 * to false so a failed /serverinfo hides those roles rather than offering an
 * action the gateway would reject.
 */
function isNativeCapableSubtype(connection, { postgresProxyEnabled = false } = {}) {
  if (!connection) return false
  const { subtype, type } = connection
  const isNativeSubtype =
    NATIVE_CLIENT_DIRECT_SUBTYPES.has(subtype) ||
    NATIVE_CLIENT_HTTP_PROXY_SUBTYPES.has(subtype) ||
    NATIVE_CLIENT_KUBERNETES_SUBTYPES.has(subtype) ||
    (type === 'custom' && NATIVE_CLIENT_CUSTOM_SUBTYPES.has(subtype))
  if (!isNativeSubtype) return false
  return subtype !== 'postgres' || postgresProxyEnabled
}

export function canAccessNativeClient(connection, options = {}) {
  if (!connection || connection.access_mode_connect !== 'enabled') return false
  return isNativeCapableSubtype(connection, options)
}

// Subtypes that speak a wire protocol the browser terminal can't drive; they
// are reachable through a native client or the CLI instead. Mirrors the
// exclusion list in the CLJS can-open-web-terminal? predicate
// (webapp/src/webapp/resources/helpers.cljs).
const NON_TERMINAL_SUBTYPES = new Set([
  'tcp',
  'ssh',
  'ssh-local',
  'rdp',
  'github',
  'git',
])

// Whether the connection can be driven from the browser terminal, which is
// what an access request of type "command" runs against.
export function canOpenWebTerminal(connection) {
  if (!connection) return false
  const { subtype } = connection
  if (NON_TERMINAL_SUBTYPES.has(subtype) || HTTP_PROXY_SUBTYPES.has(subtype)) {
    return false
  }
  return (
    connection.access_mode_runbooks === 'enabled' ||
    connection.access_mode_exec === 'enabled'
  )
}

// Whether the connection can be reached with `hoop connect`. Everything the
// CLI can open qualifies except custom RDP, which needs the native client.
export function canHoopCli(connection) {
  if (!connection || connection.access_mode_connect !== 'enabled') return false
  return !(connection.type === 'custom' && connection.subtype === 'rdp')
}

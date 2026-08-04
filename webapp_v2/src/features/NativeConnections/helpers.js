import { SUBTYPE_LABELS } from './constants'

/**
 * Collapse SSH variants to "ssh". Local-mode SSH connections carry the subtype
 * "ssh-local" but connect exactly like a regular SSH connection (gateway SSH
 * server + password), so they show the same credentials and command.
 */
export function normalizeSubtype(subtype) {
  return subtype === 'ssh-local' ? 'ssh' : subtype
}

/**
 * The host the user should point their client at.
 *
 * The gateway's own serverHost can be 0.0.0.0 behind a proxy, so the browser's
 * hostname is authoritative — except on localhost, where 0.0.0.0 is what
 * actually reaches the proxy from a container.
 */
export function getHostname() {
  const { hostname } = window.location
  return hostname === 'localhost' ? '0.0.0.0' : hostname
}

export function getSslMode() {
  return window.location.protocol === 'https:' ? 'require' : 'disable'
}

export function buildPostgresConnectionString({ port, username, password, database_name }) {
  const dbName = database_name || 'postgres'
  return `postgres://${username}:${password}@${getHostname()}:${port}/${dbName}?sslmode=${getSslMode()}`
}

export function buildSshCommand({ port, username }) {
  return `ssh ${username}@${getHostname()} -p ${port}`
}

export function buildAwsSsmCommand({ aws_access_key_id, aws_secret_access_key, endpoint_url }) {
  const { origin, hostname } = window.location
  const endpoint = hostname === 'localhost' ? endpoint_url || `${origin}/ssm/` : `${origin}/ssm/`
  return (
    `AWS_ACCESS_KEY_ID="${aws_access_key_id}" ` +
    `AWS_SECRET_ACCESS_KEY="${aws_secret_access_key}" ` +
    'aws ssm start-session --target {TARGET_INSTANCE} ' +
    `--endpoint-url "${endpoint}"`
  )
}

// The MCP endpoint exposed by the local http proxy.
export function buildMcpUrl({ port }) {
  return `${window.location.protocol}//${getHostname()}:${port}/mcp`
}

/** Human-readable duration for the JIT access-window notice. */
export function formatDurationSec(sec) {
  if (sec < 3600) {
    const minutes = sec / 60
    return `${minutes} minute${minutes === 1 ? '' : 's'}`
  }
  if (sec === 3600) return '1 hour'
  if (sec % 3600 === 0) return `${sec / 3600} hours`
  return `${Math.floor(sec / 3600)}h ${Math.floor((sec % 3600) / 60)}m`
}

export function getSubtypeLabel(subtype) {
  const normalized = normalizeSubtype(subtype)
  if (SUBTYPE_LABELS[normalized]) return SUBTYPE_LABELS[normalized]
  if (!normalized) return ''
  return normalized
    .split(/[-_]/)
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(' ')
}

/**
 * A credential with no expire_at is persistent and never goes stale.
 * Mirrors native-client-access-valid? in the CLJS constants.
 */
export function isSessionValid(session) {
  if (!session) return false
  if (!session.expire_at) return true
  return new Date(session.expire_at).getTime() > Date.now()
}

/**
 * Flat lowercase haystack backing the drawer's search box, whose placeholder
 * promises "type, attributes, tag or names".
 *
 * Filtering is client-side because the server cannot honour that promise: the
 * List endpoint's `search` only covers name/type/subtype/resource_name/status,
 * `attribute` and `tags` are exact-match filters, and the tag sanitiser rejects
 * the ":" a key:value query needs. The full list is already in memory for the
 * policy filter anyway.
 */
export function buildSearchIndex(connection) {
  const tagPairs = Object.entries(connection.connection_tags || {}).flatMap(([k, v]) => [
    k,
    v,
    `${k}:${v}`,
  ])
  return [
    connection.name,
    connection.resource_name,
    connection.type,
    connection.subtype,
    ...(connection.attributes || []),
    ...(connection.managed_attributes || []),
    ...(connection.tags || []),
    ...tagPairs,
  ]
    .filter(Boolean)
    .join(' ')
    .toLowerCase()
}

export function matchesQuery(role, query) {
  const q = query.trim().toLowerCase()
  return !q || role.searchIndex.includes(q)
}

/** Extracts the most useful message out of an axios error. */
export function errorMessage(error, fallback) {
  return error?.response?.data?.message || error?.message || fallback
}

/**
 * Opens the browser RDP client. It is a real form POST to /rdpproxy/client with
 * the credential in a hidden field, not a fetch — the gateway responds with an
 * HTML document that has to land in a new tab.
 */
export function openRdpWebClient(username) {
  const form = document.createElement('form')
  form.method = 'POST'
  form.action = '/rdpproxy/client'
  form.target = '_blank'

  const input = document.createElement('input')
  input.type = 'hidden'
  input.name = 'credential'
  input.value = username

  form.appendChild(input)
  document.body.appendChild(form)
  form.submit()
  form.remove()
}

// MCP Gateway (mcpproxy) connection settings, reconstructed from what a
// saved connection actually carries.
//
// The create wizard (still CLJS: webapp/src/webapp/resources/setup/
// roles_step.cljs::mcpproxy-role-form) holds three pieces of form state that
// are deliberately NOT saved as env vars — which catalog server was picked,
// which auth mode the admin chose, and which header a pasted token rides in.
// Nothing on the agent reads them, so emitting them would put three keys
// nothing consumes on every connection.
//
// The edit screen has only the env vars, so it has to derive those three back.
// Each derivation below says what it reads and why that reading is sound.
//
// Wire keys are the connection's own env vars, so everything here speaks
// `envvar:NAME` rather than the lower-case credential names the CLJS create
// form uses internally.

export const TRANSPORTS = [
  { value: 'streamable-http', label: 'Streamable HTTP (remote)' },
  { value: 'sse', label: 'HTTP + SSE (legacy remote)' },
  { value: 'stdio', label: 'Stdio (local server run by the agent)' },
  { value: 'client-stdio', label: "Stdio (server runs on the user's machine)" },
]

// Both stdio transports spawn a command instead of reaching a URL. They differ
// only in WHICH machine runs it, never in how anything is carried, so every
// stdio carve-out keys off this one set.
export const STDIO_TRANSPORTS = new Set(['stdio', 'client-stdio'])

export const DEFAULT_TRANSPORT = 'streamable-http'

export const AUTH_MODES = [
  { value: 'oauth', label: 'OAuth login (Hoop brokers the flow)' },
  { value: 'static', label: 'API key or personal access token' },
  { value: 'passthrough', label: 'Each user sends their own credential' },
  { value: 'none', label: 'No authentication' },
]

// The header a pasted credential rides in when nothing names another one.
// Every catalog server that documents a static credential without naming a
// header, and every self-hosted server, uses the bearer convention.
export const DEFAULT_STATIC_HEADER = 'Authorization'

export const RUG_PULL_MODES = [
  { value: 'kill', label: 'Kill the session (recommended)' },
  { value: 'alert', label: 'Record an alert and continue' },
]

export const ENV = {
  transport: 'envvar:MCP_TRANSPORT',
  remoteUrl: 'envvar:REMOTE_URL',
  auth: 'envvar:MCP_AUTH',
  allowedTools: 'envvar:MCP_ALLOWED_TOOLS',
  deniedTools: 'envvar:MCP_DENIED_TOOLS',
  approvalTools: 'envvar:MCP_APPROVAL_TOOLS',
  maxCalls: 'envvar:MCP_MAX_CALLS',
  maxResultKb: 'envvar:MCP_MAX_RESULT_KB',
  onRugPull: 'envvar:MCP_ON_RUG_PULL',
  blockSampling: 'envvar:MCP_BLOCK_SAMPLING',
  blockElicitation: 'envvar:MCP_BLOCK_ELICITATION',
  insecure: 'envvar:INSECURE',
}

export const HEADER_PREFIX = 'envvar:HEADER_'
export const MCPENV_PREFIX = 'envvar:MCPENV_'
export const AUTHORIZATION_KEY = 'envvar:HEADER_Authorization'

// Every env var this form owns. Anything else on the connection belongs to
// the headers / child-environment editors and must keep rendering there.
const OWNED_KEYS = new Set(Object.values(ENV))

export function isOwnedKey(key) {
  return OWNED_KEYS.has(key)
}

export function isStdio(transport) {
  return STDIO_TRANSPORTS.has(transport)
}

export function headerKeyFor(headerName) {
  return `${HEADER_PREFIX}${headerName}`
}

/** Header name carried inside an `envvar:HEADER_<name>` key. */
export function headerNameOf(key) {
  return key.slice(HEADER_PREFIX.length)
}

/**
 * Every `envvar:HEADER_*` key on this connection.
 *
 * Case is preserved exactly. The header name travels inside the key and
 * reaches the provider byte for byte (agent/controller/mcpproxy.go's
 * mcpBackendHeaders strips only the prefix), so context7's CONTEXT7_API_KEY
 * and google-maps' X-Goog-Api-Key must survive untouched — normalising either
 * one is an unauthenticated request, not a warning.
 */
export function headerKeys(secret) {
  return Object.keys(secret || {}).filter((k) => k.startsWith(HEADER_PREFIX))
}

/**
 * The header the connection's own credential rides in, or null when it
 * carries none.
 *
 * This is the derivation that replaces the create form's `mcp_static_header`.
 * It is sound because the create form emits exactly one credential header per
 * connection: the auth widget owns every `HEADER_*` under :credentials, while
 * extra headers an admin adds live in a separate list and only gain the prefix
 * at save time. So a single HEADER_* key IS the credential, whatever it is
 * called.
 *
 * More than one means the admin added extra headers alongside the credential.
 * Authorization wins then, because that is the only name the auth widget
 * produces without being told otherwise (both the OAuth freeze and the default
 * static header use it); the rest stay editable as ordinary headers.
 */
export function credentialHeaderKey(secret) {
  const keys = headerKeys(secret)
  if (keys.length === 0) return null
  if (keys.length === 1) return keys[0]
  const authorization = keys.find(
    (k) => headerNameOf(k).toLowerCase() === 'authorization',
  )
  return authorization || keys[0]
}

/**
 * The auth mode to render for a saved connection.
 *
 * MCP_AUTH alone cannot answer this: the agent accepts none|static|passthrough
 * and has no oauth mode at all, because Hoop brokers that login itself and
 * resolves the result into a header before the session opens
 * (gateway/services/mcp_oauth_grant.go). So an OAuth connection is stored as
 * MCP_AUTH=static and is indistinguishable from a pasted token — by design,
 * agent-side.
 *
 * `granted` closes that gap. It is the gateway reporting that a durable OAuth
 * grant backs this connection (Connection.mcp_oauth_granted), which only an
 * adopted login produces. Without it the form guesses "static" and offers to
 * replace a token when it should be offering to re-authorize.
 *
 * stdio backends authenticate through their child environment; MCP_AUTH is
 * meaningless there and the form renders no auth block at all.
 */
export function deriveAuthMode({ secret, granted }) {
  const stored = decodePlain(secret?.[ENV.auth]).trim()
  if (stored === 'passthrough') return 'passthrough'
  if (granted) return 'oauth'
  if (stored === 'none') return 'none'
  // An older connection saved before MCP_AUTH was written at all still has a
  // credential header; treat the header as the answer rather than defaulting
  // a configured connection to "no authentication".
  if (!stored) return credentialHeaderKey(secret) ? 'static' : 'none'
  return 'static'
}

/**
 * MCP_AUTH for a chosen mode.
 *
 * OAuth collapses to "static" because that is what the agent sees: the gateway
 * has already resolved the grant into a header by then. Passthrough must
 * travel verbatim — there is no credential on the connection and the agent has
 * to know to take one off each inbound request instead; collapsing it would
 * produce a backend that authenticates as nobody.
 */
export function authEnvValue(mode) {
  if (mode === 'none') return 'none'
  if (mode === 'passthrough') return 'passthrough'
  return 'static'
}

/**
 * Modes to offer for a catalog entry.
 *
 * A server the catalog does not know (custom / self-hosted) gets every mode:
 * Hoop cannot know what it accepts and the admin does. A known server gets
 * what the gateway said it supports — anything else strands the admin on a
 * flow the provider cannot complete (an OAuth login against google-maps runs
 * RFC 9728 discovery on an endpoint that publishes no authorization server).
 *
 * Passthrough is the exception: it is a Hoop capability, not a provider one,
 * sending a bearer credential in the same header a static token uses but
 * sourced per caller. Any server documenting a static credential can serve it.
 * A server taking no credential at all cannot.
 */
export function authModesFor(entry) {
  if (!entry) return AUTH_MODES
  const declared = entry.auth_modes?.length
    ? entry.auth_modes
    : entry.auth
      ? [entry.auth]
      : null
  if (!declared) return AUTH_MODES
  const supported = new Set(declared)
  if (supported.has('static')) supported.add('passthrough')
  return AUTH_MODES.filter((m) => supported.has(m.value))
}

/**
 * Header name a catalog entry expects for a pasted credential, from its
 * "Name: ${TEMPLATE}" header field. Null when the entry names none.
 */
export function staticHeaderName(entry) {
  const header = entry?.header || ''
  const name = header.split(':', 1)[0].trim()
  return name || null
}

export function staticHeaderFor(entry) {
  return staticHeaderName(entry) || DEFAULT_STATIC_HEADER
}

/**
 * The catalog entry a saved connection points at, matched on endpoint.
 *
 * This replaces the create form's `mcp_server`. Matching on the URL is what
 * makes it sound: the picker's only effect is to write that URL (plus the
 * transport and auth defaults that follow from it), so the URL identifies the
 * entry as well as the name did. Null means custom / self-hosted, which is
 * also what the picker would show.
 */
export function matchCatalogEntry(entries, remoteUrl) {
  const url = (remoteUrl || '').trim()
  if (!url) return null
  const normalize = (u) => u.replace(/\/+$/, '')
  return (entries || []).find((e) => normalize(e.url) === normalize(url)) || null
}

// ---------------------------------------------------------------------------
// Value coercion
// ---------------------------------------------------------------------------

// Kept local so this module has no import cycle with secretsCodec's
// display/reference handling: every value read here is a plain setting the
// agent parses, never a secrets-manager reference.
function decodePlain(encoded) {
  if (!encoded) return ''
  try {
    return decodeURIComponent(escape(window.atob(encoded)))
  } catch {
    return ''
  }
}

export { decodePlain }

/**
 * A boolean setting stored as the STRING "true"/"false".
 *
 * Every value in the env map is a string, so a plain truthiness test reads
 * "false" as true — the bug that makes a switch saved off reopen on. Only the
 * exact string counts.
 */
export function boolSetting(secret, key, whenUnset) {
  const raw = decodePlain(secret?.[key]).trim()
  if (!raw) return whenUnset
  return raw === 'true'
}

export function textSetting(secret, key) {
  return decodePlain(secret?.[key])
}

// Ported from webapp/src/webapp/connections/native_client_access/constants.cljs
export const ACCESS_DURATION_OPTIONS = [
  { label: '15 minutes', value: '15' },
  { label: '30 minutes', value: '30' },
  { label: '1 hour', value: '60' },
  { label: '2 hours', value: '120' },
  { label: '4 hours', value: '240' },
  { label: '8 hours', value: '480' },
  { label: '16 hours', value: '960' },
  { label: '24 hours', value: '1440' },
  { label: '32 hours', value: '1920' },
  { label: '40 hours', value: '2400' },
  { label: '48 hours', value: '2880' },
]

export const DEFAULT_ACCESS_DURATION_MINUTES = '30'

// Both halves are identical today. The shape is kept so splitting the copy for
// admins later is a one-line change rather than a refactor.
export const ERROR_MESSAGES = {
  agentOffline:
    'The Agent configured for this connection is not available at this moment. Please reach out to your organization admin to enable it before proceeding.',
  generic:
    'This connection method is not available at this moment. Please reach out to your organization admin to enable this method.',
}

export const REQUEST_FAILED_FALLBACK = 'Failed to request native client access'

// Subtypes that the gateway collapses into "httpproxy" (see
// mapValidSubtypeToHttpProxy in gateway/api/connections/connection_credentials.go).
// grafana/kibana/kubernetes-token should never reach the client un-collapsed;
// they are listed defensively because the CLJS renderer handled them.
export const HTTP_PROXY_LIKE_SUBTYPES = new Set([
  'httpproxy',
  'kubernetes',
  'kubernetes-eks',
  'grafana',
  'kibana',
])

export const SUBTYPE_LABELS = {
  postgres: 'PostgreSQL',
  rdp: 'Remote Desktop',
  ssh: 'SSH',
  'aws-ssm': 'AWS SSM',
  kubernetes: 'Kubernetes',
  httpproxy: 'HTTP Proxy',
  mcp: 'MCP',
  'claude-code': 'Claude Code',
}

export const CLAUDE_CODE_DOCS_URL = 'https://code.claude.com/docs/en/overview'

// How often a row waiting on a review re-tries the resume endpoint. There is no
// push channel for an approval, and a review is not something a person approves
// in the same breath — 10s is responsive enough without hammering an endpoint
// that sweeps expired credential sessions on every call.
export const REVIEW_POLL_MS = 10_000

export const DRAWER_ID = 'native-connections-drawer'
export const DRAWER_TITLE_ID = 'native-connections-drawer-title'
export const DRAWER_WIDTH = 600
export const DRAWER_OFFSET = 8

// The legacy CLJS stylesheet parks Radix overlays at 201 and poppers/tooltips
// at 202 (webapp/src/css/tailwind.css), and the legacy snackbar at 203. Those
// are the values to clear on CLJS routes — the AppShell header itself is only
// at 100 (Mantine's getDefaultZIndex('app')), and the navbar at 101.
export const DRAWER_Z_INDEX = 300

// Dialogs opened from inside the drawer have to clear it. Mantine's Modal
// defaults to 200, which put the disconnect confirmation behind the drawer and
// made it impossible to click through to.
export const DRAWER_MODAL_Z_INDEX = 400

// Filtering runs over the whole array, but rendering thousands of accordion
// items is what actually hurts. Above the cap the count line tells the user to
// narrow the search instead of silently truncating.
export const MAX_RENDERED_ROWS = 150

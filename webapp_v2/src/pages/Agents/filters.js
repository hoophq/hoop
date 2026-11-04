// Client-side search, filter and sort for the agents list. The page loads
// every agent of the org at once (GET /agents), so no server query is needed.

export const STATUS_ONLINE = 'online'
export const STATUS_OFFLINE = 'offline'

export const SORT_NAME_ASC = 'name-asc'
export const SORT_NAME_DESC = 'name-desc'
export const SORT_STATUS = 'status'
export const SORT_VERSION = 'version'

export const SORT_OPTIONS = [
  { value: SORT_NAME_ASC, label: 'Name A–Z' },
  { value: SORT_NAME_DESC, label: 'Name Z–A' },
  { value: SORT_STATUS, label: 'Online first' },
  { value: SORT_VERSION, label: 'Newest version' },
]

export const DEFAULT_FILTERS = {
  search: '',
  status: null,
  version: null,
  sort: SORT_NAME_ASC,
}

export const isOnline = (agent) => agent?.status === 'CONNECTED'

export const hasFilters = (filters) =>
  Boolean(filters.search.trim() || filters.status || filters.version)

const collator = new Intl.Collator(undefined, { sensitivity: 'base', numeric: true })

export const compareNames = (a, b) => collator.compare(a?.name ?? '', b?.name ?? '')

// Numeric parts of "1.278.0" or "v1.278.0-rc1"; null when the value has no
// leading semantic version, so such agents sort after the known versions.
function versionParts(version) {
  const match = /^v?(\d+)(?:\.(\d+))?(?:\.(\d+))?/.exec((version ?? '').trim())
  if (!match) return null
  return match.slice(1).map((part) => Number(part ?? 0))
}

// Newest version first; unknown versions last.
export function compareVersionsDesc(a, b) {
  const pa = versionParts(a)
  const pb = versionParts(b)
  if (!pa && !pb) return collator.compare(a ?? '', b ?? '')
  if (!pa) return 1
  if (!pb) return -1
  for (let i = 0; i < 3; i++) {
    if (pa[i] !== pb[i]) return pb[i] - pa[i]
  }
  return collator.compare(b ?? '', a ?? '')
}

// Distinct non-empty versions, newest first, for the Version filter.
export function agentVersions(agents) {
  const versions = new Set()
  for (const agent of agents || []) {
    if (agent?.version) versions.add(agent.version)
  }
  return [...versions].sort(compareVersionsDesc)
}

const COMPARATORS = {
  [SORT_NAME_ASC]: compareNames,
  [SORT_NAME_DESC]: (a, b) => compareNames(b, a),
  [SORT_STATUS]: (a, b) => Number(isOnline(b)) - Number(isOnline(a)) || compareNames(a, b),
  [SORT_VERSION]: (a, b) => compareVersionsDesc(a?.version, b?.version) || compareNames(a, b),
}

export function sortAgents(agents, sort = SORT_NAME_ASC) {
  const compare = COMPARATORS[sort] ?? COMPARATORS[SORT_NAME_ASC]
  return [...(agents || [])].sort(compare)
}

export function filterAgents(agents, { search = '', status = null, version = null } = {}) {
  const query = search.trim().toLowerCase()
  return (agents || []).filter((agent) => {
    if (query && !(agent?.name ?? '').toLowerCase().includes(query)) return false
    if (status === STATUS_ONLINE && !isOnline(agent)) return false
    if (status === STATUS_OFFLINE && isOnline(agent)) return false
    if (version && agent?.version !== version) return false
    return true
  })
}

export const applyAgentFilters = (agents, filters) =>
  sortAgents(filterAgents(agents, filters), filters.sort)

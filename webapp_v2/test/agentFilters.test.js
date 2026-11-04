import { describe, expect, it } from 'vitest'
import {
  DEFAULT_FILTERS,
  SORT_NAME_ASC,
  SORT_NAME_DESC,
  SORT_STATUS,
  SORT_VERSION,
  STATUS_OFFLINE,
  STATUS_ONLINE,
  agentVersions,
  applyAgentFilters,
  compareVersionsDesc,
  filterAgents,
  hasFilters,
  sortAgents,
} from '@/pages/Agents/filters'

const AGENTS = [
  { id: '1', name: 'prod-us', version: '1.278.0', status: 'CONNECTED' },
  { id: '2', name: 'Dev-br', version: '1.9.0', status: 'DISCONNECTED' },
  { id: '3', name: 'agent10', version: '1.280.1', status: 'CONNECTED' },
  { id: '4', name: 'agent2', version: 'unknown', status: 'DISCONNECTED' },
  { id: '5', name: 'prod-eu', version: '1.278.0', status: 'DISCONNECTED' },
]

const names = (list) => list.map((a) => a.name)

describe('sortAgents', () => {
  it('sorts by name A–Z, case-insensitive and numeric-aware', () => {
    expect(names(sortAgents(AGENTS, SORT_NAME_ASC))).toEqual(['agent2', 'agent10', 'Dev-br', 'prod-eu', 'prod-us'])
  })

  it('sorts by name Z–A', () => {
    expect(names(sortAgents(AGENTS, SORT_NAME_DESC))).toEqual(['prod-us', 'prod-eu', 'Dev-br', 'agent10', 'agent2'])
  })

  it('puts online agents first, then by name', () => {
    expect(names(sortAgents(AGENTS, SORT_STATUS))).toEqual(['agent10', 'prod-us', 'agent2', 'Dev-br', 'prod-eu'])
  })

  it('puts the newest version first and unknown versions last', () => {
    expect(names(sortAgents(AGENTS, SORT_VERSION))).toEqual(['agent10', 'prod-eu', 'prod-us', 'Dev-br', 'agent2'])
  })

  it('falls back to name A–Z for an unknown sort and does not mutate the input', () => {
    const input = [...AGENTS]
    expect(names(sortAgents(input, 'bogus'))).toEqual(names(sortAgents(AGENTS, SORT_NAME_ASC)))
    expect(input).toEqual(AGENTS)
  })

  it('accepts an empty or missing list', () => {
    expect(sortAgents(undefined)).toEqual([])
  })
})

describe('compareVersionsDesc', () => {
  it('compares numerically, not as text', () => {
    expect(['1.9.0', '1.278.0', '1.28.3'].sort(compareVersionsDesc)).toEqual(['1.278.0', '1.28.3', '1.9.0'])
  })
})

describe('filterAgents', () => {
  it('matches the name search case-insensitively', () => {
    expect(names(filterAgents(AGENTS, { search: '  PROD ' }))).toEqual(['prod-us', 'prod-eu'])
  })

  it('filters by status', () => {
    expect(names(filterAgents(AGENTS, { status: STATUS_ONLINE }))).toEqual(['prod-us', 'agent10'])
    expect(names(filterAgents(AGENTS, { status: STATUS_OFFLINE }))).toEqual(['Dev-br', 'agent2', 'prod-eu'])
  })

  it('filters by exact version', () => {
    expect(names(filterAgents(AGENTS, { version: '1.278.0' }))).toEqual(['prod-us', 'prod-eu'])
  })

  it('combines filters', () => {
    expect(names(filterAgents(AGENTS, { search: 'prod', status: STATUS_ONLINE, version: '1.278.0' }))).toEqual(['prod-us'])
  })
})

describe('applyAgentFilters', () => {
  it('returns every agent sorted by name with the default filters', () => {
    expect(names(applyAgentFilters(AGENTS, DEFAULT_FILTERS))).toEqual(['agent2', 'agent10', 'Dev-br', 'prod-eu', 'prod-us'])
  })
})

describe('agentVersions', () => {
  it('lists distinct versions, newest first', () => {
    expect(agentVersions([...AGENTS, { id: '6', name: 'x', version: '' }])).toEqual(['1.280.1', '1.278.0', '1.9.0', 'unknown'])
  })
})

describe('hasFilters', () => {
  it('ignores the sort and a blank search', () => {
    expect(hasFilters({ ...DEFAULT_FILTERS, search: '   ', sort: SORT_VERSION })).toBe(false)
    expect(hasFilters({ ...DEFAULT_FILTERS, status: STATUS_ONLINE })).toBe(true)
  })
})

import { describe, expect, it } from 'vitest'
import {
  LINUX_VM,
  catalogEntries,
  categoryLabel,
  credentialTypeLabel,
  docsUrl,
  entryBadges,
  entryIconUrl,
  filterEntries,
  filterOptions,
  groupByCategory,
  hasFilters,
  markdownLinkParts,
  popularEntries,
  sectionTitle,
} from '@/pages/Resources/Catalog/entries'

const entry = (id, category, tags = [], extra = {}) => ({
  id,
  name: id.toUpperCase(),
  description: `${id} description`,
  category,
  tags,
  ...extra,
})

const CONNECTIONS = [
  entry('postgres', 'database', ['sql', 'relational']),
  entry('mysql', 'database', ['sql']),
  entry('mongodb', 'database', ['nosql', 'document']),
  entry('ssh', 'infrastructure-access', ['shell']),
  entry('grafana', 'web-applications', ['monitoring'], { description: 'Dashboards over HTTP' }),
  entry('mcp', 'ai'),
  entry('mcpproxy', 'ai'),
]

const ENTRIES = catalogEntries(CONNECTIONS, {})
const NO_FILTERS = { search: '', categories: new Set(), tags: new Set() }
const ids = (list) => list.map((e) => e.id)

describe('catalogEntries', () => {
  it('adds Linux VM to the metadata entries', () => {
    expect(ids(ENTRIES).at(-1)).toBe('linux-vm')
    expect(ENTRIES.at(-1)).toBe(LINUX_VM)
  })

  it('shows mcp without the MCP Gateway flag and mcpproxy with it', () => {
    expect(ids(catalogEntries(CONNECTIONS, {}))).toContain('mcp')
    expect(ids(catalogEntries(CONNECTIONS, {}))).not.toContain('mcpproxy')
    const flagged = ids(catalogEntries(CONNECTIONS, { 'experimental.mcp_gateway': true }))
    expect(flagged).toContain('mcpproxy')
    expect(flagged).not.toContain('mcp')
  })

  it('answers Linux VM alone before the metadata loads', () => {
    expect(ids(catalogEntries(undefined, undefined))).toEqual(['linux-vm'])
  })
})

describe('filterEntries', () => {
  const filter = (filters) => ids(filterEntries(ENTRIES, { ...NO_FILTERS, ...filters }))

  it('keeps every entry with no filter', () => {
    expect(filter({})).toEqual(ids(ENTRIES))
  })

  it('searches name, description and tags, ignoring case', () => {
    expect(filter({ search: 'POSTGRES' })).toEqual(['postgres'])
    expect(filter({ search: 'dashboards' })).toEqual(['grafana'])
    expect(filter({ search: 'nosql' })).toEqual(['mongodb'])
  })

  it('ORs the categories', () => {
    expect(filter({ categories: new Set(['ai', 'web-applications']) })).toEqual(['grafana', 'mcp'])
  })

  it('ORs the tags', () => {
    expect(filter({ tags: new Set(['document', 'shell']) })).toEqual(['mongodb', 'ssh'])
  })

  it('ANDs search, categories and tags', () => {
    expect(filter({ search: 'sql', categories: new Set(['database']), tags: new Set(['relational']) })).toEqual([
      'postgres',
    ])
  })

  it('ignores a blank search', () => {
    expect(hasFilters({ ...NO_FILTERS, search: '  ' })).toBe(false)
    expect(filter({ search: '  ' })).toEqual(ids(ENTRIES))
  })
})

describe('popularEntries', () => {
  it('shows the popular entries with no filter', () => {
    expect(ids(popularEntries(ENTRIES, NO_FILTERS))).toEqual(['postgres', 'mysql', 'mongodb', 'ssh', 'linux-vm'])
  })

  it.each([{ search: 'pg' }, { categories: new Set(['database']) }, { tags: new Set(['sql']) }])(
    'hides them with a filter: %o',
    (filters) => {
      expect(popularEntries(ENTRIES, { ...NO_FILTERS, ...filters })).toEqual([])
    },
  )
})

describe('groupByCategory', () => {
  it('groups by category, sorted', () => {
    const groups = groupByCategory(ENTRIES)
    expect(groups.map(([category]) => category)).toEqual([
      'ai',
      'database',
      'infrastructure-access',
      'web-applications',
    ])
    expect(ids(groups[2][1])).toEqual(['ssh', 'linux-vm'])
  })
})

describe('filterOptions', () => {
  it('lists distinct categories and tags, sorted', () => {
    expect(filterOptions(ENTRIES)).toEqual({
      categories: ['ai', 'database', 'infrastructure-access', 'web-applications'],
      tags: ['container', 'document', 'infrastructure', 'linux', 'monitoring', 'nosql', 'relational', 'shell', 'sql', 'ssh', 'vm'],
    })
  })

  it('keeps the first 20 distinct tags', () => {
    const many = [entry('x', 'database', Array.from({ length: 25 }, (_, i) => `t${String(i).padStart(2, '0')}`))]
    expect(filterOptions(many).tags).toHaveLength(20)
    expect(filterOptions(many).tags.at(-1)).toBe('t19')
  })
})

describe('labels', () => {
  it('titles a section with its first letter capitalized', () => {
    expect(sectionTitle('infrastructure-access')).toBe('Infrastructure access')
  })

  it('labels a category filter with each word capitalized', () => {
    expect(categoryLabel('infrastructure-access')).toBe('Infrastructure Access')
  })

  it('writes AI and LLMs as such in both places', () => {
    expect(sectionTitle('ai-and-llms')).toBe('AI and LLMs')
    expect(categoryLabel('ai-and-llms')).toBe('AI and LLMs')
  })

  it('maps badges', () => {
    expect(entryBadges({ badges: ['new', 'beta', 'preview'] })).toEqual([
      { text: 'NEW', color: 'green' },
      { text: 'BETA', color: 'indigo' },
      { text: 'Preview', color: 'gray' },
    ])
    expect(entryBadges({})).toEqual([])
  })

  it('maps credential types', () => {
    expect(credentialTypeLabel('env-var')).toBe('Environment Variable')
    expect(credentialTypeLabel('filesystem')).toBe('File Content')
    expect(credentialTypeLabel('textarea')).toBe('Text Content')
    expect(credentialTypeLabel('other')).toBe('other')
  })
})

describe('urls', () => {
  it('builds the docs url from the documentation path', () => {
    expect(docsUrl({ documentationConfig: { path: 'quickstart/databases/postgres' } })).toBe(
      'https://hoop.dev/docs/quickstart/databases/postgres',
    )
    expect(docsUrl({})).toBe('https://hoop.dev/docs/')
  })

  it('reads the icon by icon-name, else by id', () => {
    expect(entryIconUrl(LINUX_VM)).toBe('/icons/connections/ssh-default.svg')
    expect(entryIconUrl({ id: 'postgres' })).toBe('/icons/connections/postgres-default.svg')
  })
})

describe('markdownLinkParts', () => {
  it('splits text and links', () => {
    expect(markdownLinkParts('See [the docs](https://hoop.dev/docs) and [AWS](https://aws.amazon.com).')).toEqual([
      'See ',
      { text: 'the docs', href: 'https://hoop.dev/docs' },
      ' and ',
      { text: 'AWS', href: 'https://aws.amazon.com' },
      '.',
    ])
  })

  it('keeps text with no link, and no text', () => {
    expect(markdownLinkParts('plain [text]')).toEqual(['plain [text]'])
    expect(markdownLinkParts(undefined)).toEqual([])
  })
})

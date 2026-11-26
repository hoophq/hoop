import { hiddenByFlags } from '@/pages/Resources/Setup/catalog'

// A catalog-only entry: it is not in the metadata.
export const LINUX_VM = {
  id: 'linux-vm',
  name: 'Linux VM or Container',
  description: 'Connect to Linux virtual machines, Docker containers, or any remote server via SSH.',
  category: 'infrastructure-access',
  'icon-name': 'ssh',
  tags: ['linux', 'vm', 'container', 'ssh', 'infrastructure'],
  overview: {
    description:
      'Connect to any Linux-based system including virtual machines, Docker containers, bare metal servers, and cloud instances.',
  },
  setupGuide: { accessMethods: { webapp: true, cli: true, runbooks: true } },
  resourceConfiguration: { type: 'server', subtype: 'custom' },
}

const POPULAR = new Set(['postgres', 'mysql', 'mongodb', 'ssh', 'linux-vm'])

const BADGES = {
  new: { text: 'NEW', color: 'green' },
  beta: { text: 'BETA', color: 'indigo' },
}

const CREDENTIAL_TYPES = {
  'env-var': 'Environment Variable',
  filesystem: 'File Content',
  textarea: 'Text Content',
}

const byText = (a, b) => (a < b ? -1 : a > b ? 1 : 0)

function capitalize(text) {
  return text.charAt(0).toUpperCase() + text.slice(1).toLowerCase()
}

export function catalogEntries(connections = [], featureFlags = {}) {
  return [...connections.filter((entry) => !hiddenByFlags(entry.id, featureFlags)), LINUX_VM]
}

export function hasFilters({ search, categories, tags }) {
  return search.trim() !== '' || categories.size > 0 || tags.size > 0
}

export function filterEntries(entries, { search, categories, tags }) {
  const term = search.toLowerCase()
  const matches = (text) => text.toLowerCase().includes(term)
  return entries.filter(
    (entry) =>
      (search.trim() === '' ||
        matches(entry.name) ||
        matches(entry.description ?? '') ||
        (entry.tags ?? []).some(matches)) &&
      (categories.size === 0 || categories.has(entry.category)) &&
      (tags.size === 0 || (entry.tags ?? []).some((tag) => tags.has(tag))),
  )
}

export function popularEntries(entries, filters) {
  if (hasFilters(filters)) return []
  return entries.filter((entry) => POPULAR.has(entry.id)).slice(0, 5)
}

// [[category, entries]], sorted by category.
export function groupByCategory(entries) {
  const groups = new Map()
  entries.forEach((entry) => groups.set(entry.category, [...(groups.get(entry.category) ?? []), entry]))
  return [...groups.entries()].sort(([a], [b]) => byText(a, b))
}

// Categories sorted; tags: the first 20 distinct, sorted.
export function filterOptions(entries) {
  const categories = [...new Set(entries.map((entry) => entry.category).filter(Boolean))].sort(byText)
  const tags = [...new Set(entries.flatMap((entry) => entry.tags ?? []).filter(Boolean))].slice(0, 20).sort(byText)
  return { categories, tags }
}

export function sectionTitle(category) {
  return capitalize(category).replace(/-/g, ' ')
}

// The filter list capitalizes each word; a section title only the first.
export function categoryLabel(category) {
  return category
    .split('-')
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(' ')
}

export function entryBadges(entry) {
  return (entry.badges ?? []).map((badge) => BADGES[badge] ?? { text: capitalize(badge), color: 'gray' })
}

export function docsUrl(entry) {
  return `https://hoop.dev/docs/${entry.documentationConfig?.path ?? ''}`
}

export function credentialTypeLabel(type) {
  return CREDENTIAL_TYPES[type] ?? type
}

export function entryIconUrl(entry) {
  return `/icons/connections/${entry['icon-name'] ?? entry.id}-default.svg`
}

const MARKDOWN_LINK = /\[([^\]]+)\]\(([^)]+)\)/

// Splits "see [the docs](https://…)" into text and { text, href } parts.
export function markdownLinkParts(text = '') {
  return text
    .split(/(\[[^\]]+\]\([^)]+\))/)
    .filter((part) => part !== '')
    .map((part) => {
      const link = part.match(MARKDOWN_LINK)
      return link && link[0] === part ? { text: link[1], href: link[2] } : part
    })
}

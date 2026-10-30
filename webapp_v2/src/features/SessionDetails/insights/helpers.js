// Pure rules of the insight cards, kept apart so tests read them.

export function blockedMessage(type, direction, words = []) {
  const subject = direction === 'output' ? 'Response' : 'Query'
  if (type === 'deny_words_list') {
    return `${subject} blocked — ${words.length} forbidden ${words.length === 1 ? 'keyword' : 'keywords'} detected`
  }
  if (type === 'pattern_match') return `${subject} blocked — pattern violation detected`
  return `${subject} blocked`
}

const LOWER_WORDS = new Set(['of', 'the', 'and'])

// "EMAIL_ADDRESS" → "Email Address", "date-of-birth" → "Date of Birth".
export function infoTypeLabel(infoType = '') {
  return infoType
    .split(/_|-/)
    .map((word) => {
      const lower = word.toLowerCase()
      return LOWER_WORDS.has(lower) ? lower : lower.charAt(0).toUpperCase() + lower.slice(1)
    })
    .join(' ')
}

const fromCounts = (counts) => Object.entries(counts || {}).map(([infoType, count]) => ({ infoType, count }))

// What the masking redacted in the session: the report when it has data, else
// the counts the session carries. An agent session has them in
// metrics.data_analyzer, a sidecar session in metrics.data_masking.info_types.
export function maskingAnalytics(report, session) {
  const items = report?.items || []
  if (items.length || report?.total_redact_count > 0) {
    return {
      items: items.map((item) => ({ infoType: item.info_type, count: item.redact_total })),
      total: report.total_redact_count ?? items.reduce((sum, item) => sum + (item.redact_total || 0), 0),
    }
  }
  const metrics = session?.metrics || {}
  const counts = metrics.data_analyzer && Object.keys(metrics.data_analyzer).length
    ? metrics.data_analyzer
    : metrics.data_masking?.info_types
  const fallback = fromCounts(counts)
  return { items: fallback, total: fallback.reduce((sum, item) => sum + (Number(item.count) || 0), 0) }
}

export const itemsText = (total) => `${total} ${total === 1 ? 'item' : 'items'}`

// Collapsed: "3 (Email Address + 2 more)"; open: "3 (Email Address, Person, Phone Number)".
export function categoriesText(items, { full }) {
  if (!items.length) return full ? '-' : '0'
  const labels = items.map((item) => infoTypeLabel(item.infoType))
  if (full) return `${labels.length} (${labels.join(', ')})`
  const more = labels.length - 1
  return more >= 1 ? `${labels.length} (${labels[0]} + ${more} more)` : `${labels.length} (${labels[0]})`
}

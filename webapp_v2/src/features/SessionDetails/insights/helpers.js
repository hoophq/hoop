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
      items: splitJoined(items.map((item) => ({ infoType: item.info_type, count: item.redact_total }))),
      total: report.total_redact_count ?? items.reduce((sum, item) => sum + (item.redact_total || 0), 0),
    }
  }
  const metrics = session?.metrics || {}
  if (metrics.data_analyzer && Object.keys(metrics.data_analyzer).length) {
    const fallback = fromCounts(metrics.data_analyzer)
    return { items: fallback, total: sumCounts(fallback) }
  }
  const joined = fromCounts(metrics.data_masking?.info_types)
  return { items: splitJoined(joined), total: sumCounts(joined) }
}

const sumCounts = (items) => items.reduce((sum, item) => sum + (Number(item.count) || 0), 0)

// The gateway files the entities one sidecar event masked as one info type,
// "email+ssn", and counts the event's cells once (sidecarsessions.go), in the
// session metrics and in its report alike. Each entity is a category; the
// volume stays the event's. An info type with no "+" passes as it is.
function splitJoined(items) {
  const byType = new Map()
  for (const { infoType, count } of items) {
    for (const part of infoType.split('+').filter(Boolean)) {
      byType.set(part, (byType.get(part) || 0) + (Number(count) || 0))
    }
  }
  return [...byType].map(([infoType, count]) => ({ infoType, count }))
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

import dayjs from 'dayjs'
import advancedFormat from 'dayjs/plugin/advancedFormat'
import { CHART_SERIES_COLORS } from '@/theme'
import { OTHER_SUBTYPE } from './constants'

// `Do` ("23rd") reproduces the legacy `format-date-with-suffix` helper exactly
// for every day of the month, including the 11th/12th/13th "th" exceptions.
dayjs.extend(advancedFormat)

const MS_PER_DAY = 24 * 60 * 60 * 1000

// Statuses the Reviews chart counts.
const COUNTED_REVIEW_STATUSES = {
  APPROVED: 'approved',
  REJECTED: 'rejected',
}

/* -------------------------------------------------------------------------- */
/* Dates                                                                      */
/*                                                                            */
/* The legacy app mixes two bases and this port reproduces both: cljs-time     */
/* `today-at-midnight` is UTC midnight (the Sessions card and the Reviews      */
/* count) while `today` is the local calendar date (the Redacted card, every   */
/* range and every label).                                                     */
/* -------------------------------------------------------------------------- */

export function startOfLocalDay(date = new Date()) {
  const start = new Date(date)
  start.setHours(0, 0, 0, 0)
  return start
}

/** cljs-time `today-at-midnight` — midnight of the current *UTC* day. */
export function startOfUtcDay(date = new Date()) {
  return new Date(Date.UTC(date.getUTCFullYear(), date.getUTCMonth(), date.getUTCDate()))
}

export function addDays(date, days) {
  return new Date(date.getTime() + days * MS_PER_DAY)
}

/** Local calendar date as `YYYY-MM-DD` — the only format /reports/sessions accepts. */
export function localDateKey(date) {
  const pad = (n) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`
}

/** "Jul 23rd" — the legacy range-label format. */
export function formatOrdinalDate(date) {
  return dayjs(date).format('MMM Do')
}

/** "Jul 30, 2026" — the legacy chart-tooltip date format. */
export function formatTooltipDate(date) {
  return new Date(date).toLocaleDateString('en-US', {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
  })
}

/** "Jul 23rd - Jul 30th" — the range covered, as shown under a chart heading. */
export function rangeLabel(days) {
  const today = startOfLocalDay()
  return `${formatOrdinalDate(addDays(today, -days))} - ${formatOrdinalDate(today)}`
}

/**
 * Query window for the Redacted Data chart.
 *
 * A one-day range sends only `start_date` and lets the gateway default
 * `end_date` to tomorrow; every longer range pins `end_date` to today.
 */
export function redactedRangeParams(days) {
  const today = startOfLocalDay()

  if (days === 1) {
    return { startDate: localDateKey(today), rangeLabel: rangeLabel(days) }
  }

  return {
    startDate: localDateKey(addDays(today, -days)),
    endDate: localDateKey(today),
    rangeLabel: rangeLabel(days),
  }
}

/** Query window for the "Today's overview" redaction total. */
export function todayReportParams() {
  return { startDate: localDateKey(startOfLocalDay()) }
}

/**
 * Query window for the "Today's overview" session count.
 *
 * /sessions requires strict RFC3339 (a bare date returns 422) and filters on
 * `created_at`, inclusive at both ends. `limit: 1` is enough: `total` comes from
 * a separate un-limited COUNT query, so we pay for one row instead of twenty.
 */
export function todaySessionParams() {
  const start = startOfUtcDay()
  const end = new Date(start.getTime() + 23 * 3600_000 + 59 * 60_000 + 59_000)

  return {
    start_date: start.toISOString(),
    end_date: end.toISOString(),
    limit: 1,
  }
}

/* -------------------------------------------------------------------------- */
/* Aggregations                                                               */
/* -------------------------------------------------------------------------- */

/** Reviews created today, in any status. */
export function countReviewsToday(reviews = []) {
  const start = startOfUtcDay().getTime()
  const end = start + MS_PER_DAY

  return reviews.filter((review) => {
    const createdAt = new Date(review.created_at).getTime()
    return Number.isFinite(createdAt) && createdAt > start && createdAt < end
  }).length
}

/**
 * Approved/rejected counts per day over the last `days`, oldest first.
 *
 * Buckets are keyed by the UTC date embedded in `created_at` while the label is
 * rendered in local time, and the label is overwritten by whichever review in
 * the bucket is visited last.
 *
 * A review in any other status still opens a bucket, which then renders as a
 * zero-height bar.
 */
export function buildReviewBuckets(reviews = [], days) {
  const cutoff = Date.now() - days * MS_PER_DAY
  const buckets = new Map()

  for (const review of reviews) {
    const createdAt = new Date(review.created_at)
    const time = createdAt.getTime()
    if (!Number.isFinite(time) || time <= cutoff) continue

    const key = String(review.created_at).slice(0, 10)
    let bucket = buckets.get(key)
    if (!bucket) {
      bucket = { label: '', approved: 0, rejected: 0 }
      buckets.set(key, bucket)
    }
    bucket.label = formatTooltipDate(createdAt)

    const countedAs = COUNTED_REVIEW_STATUSES[review.status]
    if (countedAs) bucket[countedAs] += 1
  }

  return [...buckets.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([, bucket]) => bucket)
}

/** "US_SOCIAL_SECURITY_NUMBER" → "us-social-security-number". */
export function normalizeInfoType(infoType) {
  return String(infoType ?? '').replaceAll('_', '-').toLowerCase()
}

/**
 * Redaction totals per info type, largest first.
 *
 * The report query has no ORDER BY, so the server's row order is whatever the
 * hash aggregate produces and can differ between identical requests. Sorting
 * here is what makes the bar order stable.
 */
export function buildRedactedItems(items = []) {
  const totals = new Map()

  for (const item of items) {
    const key = normalizeInfoType(item.info_type)
    totals.set(key, (totals.get(key) ?? 0) + (item.redact_total ?? 0))
  }

  return [...totals.entries()]
    .map(([infoType, redactTotal]) => ({ infoType, redactTotal }))
    .sort((a, b) => b.redactTotal - a.redactTotal)
}

/** Connection counts per subtype, largest first, with a cycled palette color. */
export function buildConnectionSlices(connections = []) {
  const counts = new Map()

  for (const connection of connections) {
    const subtype = connection.subtype || OTHER_SUBTYPE
    counts.set(subtype, (counts.get(subtype) ?? 0) + 1)
  }

  return [...counts.entries()]
    .sort(([, a], [, b]) => b - a)
    .map(([name, value], index) => ({
      name,
      value,
      color: CHART_SERIES_COLORS[index % CHART_SERIES_COLORS.length],
    }))
}

import dayjs from 'dayjs'
import advancedFormat from 'dayjs/plugin/advancedFormat'
import { CHART_SERIES_COLORS } from '@/theme'
import { OTHER_SUBTYPE } from './constants'

// `Do` ("23rd") reproduces the legacy `format-date-with-suffix` helper exactly
// for every day of the month, including the 11th/12th/13th "th" exceptions.
dayjs.extend(advancedFormat)

// A review carries exactly one decision, and its status keeps moving after that
// decision is made:
//
//   PENDING ─┬─ approve → APPROVED ─┬─ run  → PROCESSING → EXECUTED
//            │                      └─ JIT revoke → REVOKED
//            └─ reject  → REJECTED  (terminal)
//
// So APPROVED is transient and REJECTED is terminal. Matching the two literals —
// which is what the legacy chart did — counts only approvals that have not been
// used yet, while every rejection ever made persists. On any real history the
// chart then reads "we reject everything".
//
// The rule is therefore expressed as a derivation, not an allow-list: PENDING is
// undecided, REJECTED is the only rejection, and everything else is downstream of
// an approval (PROCESSING and EXECUTED are the normal path, UNKNOWN is the legacy
// spelling of PROCESSING, and REVOKED means approved-then-withdrawn — the review
// decision was still an approval). Deriving rather than enumerating is deliberate:
// a status added to the gateway later shows up somewhere instead of silently
// vanishing, which is exactly how EXECUTED went missing.
const UNDECIDED_REVIEW_STATUS = 'PENDING'
const REJECTED_REVIEW_STATUS = 'REJECTED'

/** 'approved' | 'rejected' | null (still undecided). */
export function reviewDecision(status) {
  if (!status || status === UNDECIDED_REVIEW_STATUS) return null
  return status === REJECTED_REVIEW_STATUS ? 'rejected' : 'approved'
}

/* -------------------------------------------------------------------------- */
/* Dates                                                                      */
/*                                                                            */
/* Everything here works in the user's LOCAL calendar day. The legacy app      */
/* mixed two bases — cljs-time `today-at-midnight` is UTC midnight while       */
/* `today` is the local date — so its "today" cards disagreed with each other  */
/* for as many hours as the user's UTC offset.                                 */
/* -------------------------------------------------------------------------- */

export function startOfLocalDay(date = new Date()) {
  const start = new Date(date)
  start.setHours(0, 0, 0, 0)
  return start
}

/**
 * Calendar-day arithmetic, deliberately not 24-hour arithmetic.
 *
 * A local day is 23 or 25 hours long on a DST transition, so adding a fixed
 * 86,400,000 ms to a local midnight lands at 01:00 or 23:00 rather than
 * midnight twice a year — which would silently skew every "today" window and
 * range boundary on this page. setDate keeps the local wall-clock time.
 */
export function addDays(date, days) {
  const next = new Date(date)
  next.setDate(next.getDate() + days)
  return next
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

/**
 * First day covered by a range, inclusive.
 *
 * Every window ends at tomorrow's midnight (exclusive), so an N-day range has to
 * start N-1 days back to cover exactly N calendar days: 24h is today alone, 7d
 * is today plus the six days before it.
 */
function rangeStart(days) {
  return addDays(startOfLocalDay(), -(days - 1))
}

/** "Jul 23rd - Jul 30th", or a single date when the range is one day. */
export function rangeLabel(days) {
  const today = startOfLocalDay()
  const start = rangeStart(days)

  return start.getTime() === today.getTime()
    ? formatOrdinalDate(today)
    : `${formatOrdinalDate(start)} - ${formatOrdinalDate(today)}`
}

/**
 * Query window for the Redacted Data chart.
 *
 * `end_date` is always tomorrow. The gateway compares against
 * `TO_TIMESTAMP(end_date, 'YYYY-MM-DD')` — midnight *starting* that day — so
 * sending today would silently drop everything redacted today.
 */
export function redactedRangeParams(days) {
  return {
    startDate: localDateKey(rangeStart(days)),
    endDate: localDateKey(addDays(startOfLocalDay(), 1)),
    rangeLabel: rangeLabel(days),
  }
}

/** Query window for the "Today's overview" redaction total. */
export function todayReportParams() {
  const today = startOfLocalDay()

  return {
    startDate: localDateKey(today),
    endDate: localDateKey(addDays(today, 1)),
  }
}

/**
 * Query window for the "Today's overview" session count.
 *
 * /sessions requires strict RFC3339 (a bare date returns 422) and filters on
 * `created_at`, inclusive at both ends. `limit: 1` is enough: `total` comes from
 * a separate un-limited COUNT query, so we pay for one row instead of twenty.
 *
 * `count: 'exact'` is required, not decorative: the gateway now defaults to
 * `capped`. This card renders the total as a headline figure, so it is the one
 * caller that genuinely cannot accept the cheaper mode — it would show a flat
 * 10,000 on any tenant busy enough to exceed the cap in a day. The cost is
 * bounded by the one-day window, which keeps the COUNT indexable.
 */
export function todaySessionParams() {
  const start = startOfLocalDay()

  return {
    start_date: start.toISOString(),
    end_date: new Date(addDays(start, 1).getTime() - 1).toISOString(),
    limit: 1,
    count: 'exact',
  }
}

/* -------------------------------------------------------------------------- */
/* Aggregations                                                               */
/* -------------------------------------------------------------------------- */

/** Reviews created today, in any status. */
export function countReviewsToday(reviews = []) {
  const start = startOfLocalDay()
  const end = addDays(start, 1)

  return reviews.filter((review) => {
    const createdAt = new Date(review.created_at).getTime()
    return (
      Number.isFinite(createdAt) && createdAt >= start.getTime() && createdAt < end.getTime()
    )
  }).length
}

/**
 * Approved/rejected counts per day over the last `days`, oldest first.
 *
 * Buckets are keyed by the local calendar date so the grouping always agrees
 * with the label shown in the tooltip. The legacy version keyed on the UTC date
 * (`created_at.slice(0, 10)`) but rendered the label in local time, so a review
 * created just after UTC midnight was filed under one day and labelled another.
 *
 * Undecided reviews are skipped rather than creating a bucket. The legacy `cond`
 * had no `:else` branch and wrote a literal nil key, leaving a
 * `{approved: 0, rejected: 0}` entry that rendered as a zero-height bar for
 * dates whose only activity was pending.
 */
export function buildReviewBuckets(reviews = [], days) {
  // The same calendar window the Redacted Data chart queries and the subtitle
  // announces. The legacy code used a rolling `now - N days` cutoff, which cuts
  // the oldest day off partway through and lets "24h" reach into yesterday, so
  // the bars never quite matched the label above them.
  const start = rangeStart(days).getTime()
  const end = addDays(startOfLocalDay(), 1).getTime()
  const buckets = new Map()

  for (const review of reviews) {
    const decision = reviewDecision(review.status)
    if (!decision) continue

    const createdAt = new Date(review.created_at)
    const time = createdAt.getTime()
    if (!Number.isFinite(time) || time < start || time >= end) continue

    const key = localDateKey(createdAt)
    let bucket = buckets.get(key)
    if (!bucket) {
      bucket = { label: formatTooltipDate(createdAt), approved: 0, rejected: 0 }
      buckets.set(key, bucket)
    }
    bucket[decision] += 1
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

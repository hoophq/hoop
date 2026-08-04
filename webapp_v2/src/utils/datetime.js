const pad2 = (n) => String(n).padStart(2, '0')

/**
 * Accepts either a `Date` or the `YYYY-MM-DD` string Mantine v8 date inputs emit.
 *
 * A bare `YYYY-MM-DD` is parsed by `new Date()` as **UTC** midnight, so in any
 * timezone behind UTC it lands on the previous local day — build it from parts
 * instead. The picker means a local calendar date; honour that.
 */
function parseDateInput(value) {
  if (!value) return null
  if (value instanceof Date) {
    return Number.isNaN(value.getTime()) ? null : new Date(value.getTime())
  }
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(String(value))
  if (match) {
    return new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]))
  }
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime()) ? null : parsed
}

/**
 * Port of `webapp.formatters/time-parsed->full-date` (formatters.cljs:94).
 * Takes an API timestamp (`2022-10-28T16:09:17.772Z`) and renders it as
 * `DD/MM/YYYY HH:mm:ss` in the **browser's local timezone**, exactly as v1 did.
 *
 * Returns an em dash for null/unparseable input — v1 threw on those.
 */
export function formatFullDate(value) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return (
    `${pad2(date.getDate())}/${pad2(date.getMonth() + 1)}/${date.getFullYear()} ` +
    `${pad2(date.getHours())}:${pad2(date.getMinutes())}:${pad2(date.getSeconds())}`
  )
}

/**
 * RFC3339 timestamp for the first instant of `date`, in the local timezone.
 *
 * v1 built these by string concatenation — `new Date("2026-08-03 00:00:00.000Z")`
 * (audit_filters.cljs:59-63). That is not a valid ISO string: V8 tolerates the
 * space separator, but Safari returns an Invalid Date and the subsequent
 * `.toISOString()` throws a RangeError, so the date filter is broken there.
 *
 * These also anchor the window to the *local* day rather than the UTC day, which
 * makes the range consistent with `formatFullDate` — v1 filtered by UTC days
 * while rendering local times, so a session could fall outside the range it
 * appeared to be in.
 */
export function toStartOfDayISO(value) {
  const date = parseDateInput(value)
  if (!date) return undefined
  date.setHours(0, 0, 0, 0)
  return date.toISOString()
}

/** RFC3339 timestamp for the last instant of `date`, in the local timezone. */
export function toEndOfDayISO(value) {
  const date = parseDateInput(value)
  if (!date) return undefined
  date.setHours(23, 59, 59, 999)
  return date.toISOString()
}

/**
 * Inverse of the two above: an ISO timestamp back to the local `YYYY-MM-DD` that
 * Mantine v8 date inputs expect as their value.
 */
export function toDateInputValue(value) {
  if (!value) return null
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return null
  return `${date.getFullYear()}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())}`
}

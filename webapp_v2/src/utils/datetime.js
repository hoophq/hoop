const RELATIVE = new Intl.RelativeTimeFormat('en', { numeric: 'auto' })
const UNITS = [
  ['day', 24 * 60 * 60 * 1000],
  ['hour', 60 * 60 * 1000],
  ['minute', 60 * 1000],
]

// "2 hours ago", "yesterday", "just now".
export function formatRelativeTime(iso, now = Date.now()) {
  const diff = new Date(iso).getTime() - now
  for (const [unit, ms] of UNITS) {
    if (Math.abs(diff) >= ms) return RELATIVE.format(Math.round(diff / ms), unit)
  }
  return 'just now'
}

const pad = (n) => String(n).padStart(2, '0')

// "30/07/2026 13:32:47", matching the CLJS session details.
export function formatFullDate(iso) {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  return (
    `${pad(d.getDate())}/${pad(d.getMonth() + 1)}/${d.getFullYear()} ` +
    `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
  )
}

// ── Review time windows ──────────────────────────────────────────────────────
// A window is two "HH:mm" UTC strings. The user picks it in local time.

const toMinutes = (hhmm) => {
  const [h, m] = hhmm.split(':').map((n) => parseInt(n, 10))
  return h * 60 + m
}

// "09:00" local → "12:00" UTC in UTC-3, for today's date.
export function localTimeToUtc(hhmm) {
  if (!hhmm) return null
  const [h, m] = hhmm.split(':').map((n) => parseInt(n, 10))
  const now = new Date()
  const local = new Date(now.getFullYear(), now.getMonth(), now.getDate(), h, m)
  return `${pad(local.getUTCHours())}:${pad(local.getUTCMinutes())}`
}

// "22:00" UTC → "7:00 PM" in UTC-3, for today's date.
export function utcTimeToDisplay(hhmm) {
  if (!hhmm) return ''
  const [h, m] = hhmm.split(':').map((n) => parseInt(n, 10))
  const now = new Date()
  const d = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate(), h, m))
  return d.toLocaleTimeString('en-US', { hour: 'numeric', minute: '2-digit' })
}

// A window that ends before it starts crosses midnight (22:00 to 02:00).
export function isWithinTimeWindow(startUtc, endUtc, now = new Date()) {
  if (!startUtc || !endUtc) return false
  const current = now.getUTCHours() * 60 + now.getUTCMinutes()
  const start = toMinutes(startUtc)
  const end = toMinutes(endUtc)
  return start <= end ? current >= start && current <= end : current >= start || current <= end
}

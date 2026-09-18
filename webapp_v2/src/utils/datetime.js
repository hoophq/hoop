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

// What the control plane can honestly say about a sidecar.
//
// `last_seen_at` is the instant of the sidecar's last handshake with this
// gateway process (gateway/api/sidecar/state.go): kept in memory, never
// refreshed by a heartbeat, gone after a gateway restart. So there are two
// states, not three. "Offline" would need a heartbeat the sidecar does not send
// yet; OFFLINE_AFTER_MS is the slot for it.
//
// export const OFFLINE_AFTER_MS = 5 * 60 * 1000

export const SIDECAR_STATUS = {
  WAITING: 'waiting',
  CONNECTED: 'connected',
}

const STATUS = {
  [SIDECAR_STATUS.WAITING]: {
    key: SIDECAR_STATUS.WAITING,
    label: 'Waiting',
    badge: 'inactive',
    hint: 'Has not connected to this control plane yet. A control plane restart also resets this.',
  },
  [SIDECAR_STATUS.CONNECTED]: {
    key: SIDECAR_STATUS.CONNECTED,
    label: 'Connected',
    badge: 'active',
    hint: 'Last handshake with this control plane.',
  },
}

export function sidecarStatus(sidecar) {
  return sidecar?.last_seen_at ? STATUS[SIDECAR_STATUS.CONNECTED] : STATUS[SIDECAR_STATUS.WAITING]
}

const RELATIVE = new Intl.RelativeTimeFormat('en', { numeric: 'auto' })
const UNITS = [
  ['day', 24 * 60 * 60 * 1000],
  ['hour', 60 * 60 * 1000],
  ['minute', 60 * 1000],
]

// "2 hours ago", "yesterday", "just now". Staleness stays visible next to
// "Connected", because nothing turns it off.
export function formatRelativeTime(iso, now = Date.now()) {
  const diff = new Date(iso).getTime() - now
  for (const [unit, ms] of UNITS) {
    if (Math.abs(diff) >= ms) return RELATIVE.format(Math.round(diff / ms), unit)
  }
  return 'just now'
}

// What the control plane can honestly say about a sidecar.
//
// `last_seen_at` is the instant of the sidecar's last handshake with this
// gateway process (gateway/api/sidecar/state.go): kept in memory, never
// stored, gone after a gateway restart. A connected sidecar repeats the
// handshake every minute (heartbeatEvery, sidecar/daemon/controlplane.go), so
// a timestamp that stopped moving is a sidecar that stopped calling.
//
// Three states follow, and only three:
//   no timestamp    → Waiting   (never called this gateway process)
//   fresh timestamp → Connected
//   stale timestamp → Offline   (missed OFFLINE_AFTER_MS worth of heartbeats)

export const SIDECAR_STATUS = {
  WAITING: 'waiting',
  CONNECTED: 'connected',
  OFFLINE: 'offline',
}

// Five missed beats. One or two missed calls are a hiccup the daemon rides out
// ("handshake failed; serving the last good config") without dropping traffic,
// so a shorter window would report an outage the data path never had.
export const OFFLINE_AFTER_MS = 5 * 60 * 1000

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
    hint: 'Called this control plane within the last few minutes.',
  },
  [SIDECAR_STATUS.OFFLINE]: {
    key: SIDECAR_STATUS.OFFLINE,
    label: 'Offline',
    badge: 'warning',
    hint: 'Stopped calling this control plane. It may be stopped, or unable to reach it; lanes it already serves keep running on their last configuration.',
  },
}

export function sidecarStatus(sidecar, now = Date.now()) {
  if (!sidecar?.last_seen_at) return STATUS[SIDECAR_STATUS.WAITING]
  const age = now - new Date(sidecar.last_seen_at).getTime()
  return age > OFFLINE_AFTER_MS ? STATUS[SIDECAR_STATUS.OFFLINE] : STATUS[SIDECAR_STATUS.CONNECTED]
}

const RELATIVE = new Intl.RelativeTimeFormat('en', { numeric: 'auto' })
const UNITS = [
  ['day', 24 * 60 * 60 * 1000],
  ['hour', 60 * 60 * 1000],
  ['minute', 60 * 1000],
]

// "2 hours ago", "yesterday", "just now". Staleness stays visible next to the
// badge, so an Offline row says how long it has been quiet.
export function formatRelativeTime(iso, now = Date.now()) {
  const diff = new Date(iso).getTime() - now
  for (const [unit, ms] of UNITS) {
    if (Math.abs(diff) >= ms) return RELATIVE.format(Math.round(diff / ms), unit)
  }
  return 'just now'
}

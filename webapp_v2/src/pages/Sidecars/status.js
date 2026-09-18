// What the control plane can honestly say about a sidecar.
//
// `last_seen_at` is the instant of the sidecar's last check-in with this
// gateway process (gateway/api/sidecar/state.go): kept in memory, never
// stored, gone after a gateway restart. So there are two states, not three.
// "Offline" would need this to survive a restart; today a cleared timestamp
// and a sidecar that never called look the same from here, and the badge
// must not claim otherwise.

export const SIDECAR_STATUS = {
  WAITING: 'waiting',
  CONNECTED: 'connected',
}

const STATUS = {
  [SIDECAR_STATUS.WAITING]: {
    key: SIDECAR_STATUS.WAITING,
    label: 'Waiting',
    badge: 'inactive',
    hint: 'Has not checked in with this control plane yet. A control plane restart also resets this.',
  },
  [SIDECAR_STATUS.CONNECTED]: {
    key: SIDECAR_STATUS.CONNECTED,
    label: 'Connected',
    badge: 'active',
    hint: 'Last check-in with this control plane. The sidecar asks for its configuration; nothing is pushed to it.',
  },
}

export function sidecarStatus(sidecar) {
  return sidecar?.last_seen_at ? STATUS[SIDECAR_STATUS.CONNECTED] : STATUS[SIDECAR_STATUS.WAITING]
}


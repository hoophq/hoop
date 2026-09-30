// What the control plane can honestly say about a sidecar.
//
// `last_seen_at` and `config_state` are written by the sidecar's handshake and
// stored on its row (gateway/api/sidecar), so they survive a gateway restart.
// The gateway computes `config_state` from the revision it served and the one
// the sidecar reports; this file only names each state for an admin. There is
// no Offline: the relative last-seen time beside the badge is what says a
// sidecar stopped calling.

export const SIDECAR_STATUS = {
  WAITING: 'waiting',
  CONNECTED: 'connected',
  APPLIED: 'applied',
  APPLYING: 'applying',
  NOT_APPLIED: 'not_applied',
  REFUSED: 'refused',
  RESTART: 'restart',
  UNKNOWN: 'unknown',
}

const STATUS = {
  [SIDECAR_STATUS.WAITING]: {
    key: SIDECAR_STATUS.WAITING,
    label: 'Waiting',
    badge: 'inactive',
    hint: 'Has not checked in with this control plane yet.',
  },
  // Checked in, but nothing has been served: the plane holds no configuration
  // for it, or the sidecar runs its own config file.
  [SIDECAR_STATUS.CONNECTED]: {
    key: SIDECAR_STATUS.CONNECTED,
    label: 'Active',
    badge: 'active',
    hint: 'Last check-in with this control plane. The sidecar asks for its configuration; nothing is pushed to it.',
  },
}

// One entry per `config_state` the gateway computes. `label` is the badge and
// `config` the word in the details card's Configuration row.
const CONFIG_STATE = {
  [SIDECAR_STATUS.APPLIED]: {
    key: SIDECAR_STATUS.APPLIED,
    label: 'Active',
    badge: 'active',
    config: 'Applied',
    hint: 'Runs the configuration this control plane last served. It checks in every minute; nothing is pushed to it.',
  },
  [SIDECAR_STATUS.APPLYING]: {
    key: SIDECAR_STATUS.APPLYING,
    label: 'Applying',
    badge: 'inactive',
    config: 'Applying',
    hint: 'A newer configuration was served less than two minutes ago and the sidecar has not reported on it yet. Normal right after a save.',
  },
  [SIDECAR_STATUS.NOT_APPLIED]: {
    key: SIDECAR_STATUS.NOT_APPLIED,
    label: 'Not applied',
    badge: 'warning',
    config: 'Not applied',
    hint: 'A newer configuration was served more than two minutes ago and the sidecar never reported applying it. A sidecar that exits on this configuration restarts in a loop; its own log names the reason.',
  },
  [SIDECAR_STATUS.REFUSED]: {
    key: SIDECAR_STATUS.REFUSED,
    label: 'Config refused',
    badge: 'danger',
    config: 'Refused',
    hint: 'The sidecar refused the served configuration and keeps running its previous rules.',
    showsError: true,
  },
  [SIDECAR_STATUS.RESTART]: {
    key: SIDECAR_STATUS.RESTART,
    label: 'Restart needed',
    badge: 'warning',
    config: 'Restart needed',
    hint: 'The sidecar cannot take the served configuration without a restart. It keeps running the previous one until then.',
    showsError: true,
  },
  [SIDECAR_STATUS.UNKNOWN]: {
    key: SIDECAR_STATUS.UNKNOWN,
    label: 'Active',
    badge: 'active',
    config: 'Unknown',
    hint: 'Checked in, but this sidecar build is too old to report whether it applied the served configuration. Upgrade it to see the configuration state.',
  },
}

// `last_error` is the sidecar's own words and is only defined for the states
// that carry one; a stale one next to any other state would mislead.
function withError(state, sidecar) {
  const detail = state.showsError ? sidecar.last_error : ''
  return detail ? { ...state, detail } : state
}

// A state this build does not know reads as Active: the sidecar did check
// in, and that is the only claim the badge can make about it.
export function sidecarStatus(sidecar) {
  if (!sidecar?.last_seen_at) return STATUS[SIDECAR_STATUS.WAITING]
  const state = CONFIG_STATE[sidecar.config_state]
  return state ? withError(state, sidecar) : STATUS[SIDECAR_STATUS.CONNECTED]
}

// The details card's Configuration row, or null while nothing has been served.
export function configState(sidecar) {
  if (!sidecar?.config_state) return null
  const state = CONFIG_STATE[sidecar.config_state]
  if (!state) return { label: sidecar.config_state }
  const { config, detail } = withError(state, sidecar)
  return { label: config, detail }
}

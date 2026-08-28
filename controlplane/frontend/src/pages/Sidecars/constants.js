// The four states are `inventory.State` in the backend (EVL-232), fixed in the
// stub rather than deferred precisely so the frontend and the wire layer do not
// each invent their own strings. Do not add a fifth here.
export const SIDECAR_STATES = {
  connected: {
    label: 'Connected',
    variant: 'active',
    hint: 'Socket open, heartbeat current, and the generation it acked is the one we issued.',
  },
  stale: {
    label: 'Stale',
    variant: 'warning',
    hint: 'Socket open, but the acked generation is behind the issued one past the heartbeat window.',
  },
  rejected: {
    label: 'Rejected',
    variant: 'danger',
    hint: 'The sidecar refused the config and kept running the previous one. The reason is on the row.',
  },
  disconnected: {
    label: 'Disconnected',
    variant: 'inactive',
    hint: 'Socket closed. The record is kept so a restart does not read as a deletion.',
  },
}

export const STATE_FILTER_VALUES = Object.values(SIDECAR_STATES).map((s) => s.label)

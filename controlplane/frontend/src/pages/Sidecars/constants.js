// Every row in the fleet list is exactly this tall, and it is set rather than
// left to the content on purpose.
//
// This list grows with the fleet, and per-user pods put one sidecar per
// engineer, so a few thousand rows is an ordinary shape. Windowing that costs
// nothing while the height is a constant — `index * ROW_HEIGHT` needs no
// measurement cache and no invalidation. Let the content decide instead and the
// day someone adds a third line, the property is gone with no test failing.
//
// Fixing it here makes that a visible break instead of a silent one, and gives
// the windowed container its constant when it arrives.
export const ROW_HEIGHT = 80

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

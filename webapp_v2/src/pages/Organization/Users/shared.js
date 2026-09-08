// What GatewayUsers.jsx and ControlPlaneUsers.jsx have in common.

export const STATUS_OPTIONS = [
  { value: 'active', label: 'Active' },
  { value: 'inactive', label: 'Inactive' },
  { value: 'reviewing', label: 'Reviewing' },
]

// The backend activates a local user with this password immediately and never
// forces a change, so it is the account's real credential. The previous
// generator drew from three eight-word lists with Math.random — 512
// possibilities — and because the value lived in component state of a modal that
// never unmounts, every user invited before a page reload got the SAME one.
//
// Readability still matters, since an admin copies this into a message by hand:
// four words from a larger list plus digits, drawn from crypto.getRandomValues.
const WORDS = [
  'amber', 'anchor', 'atlas', 'beacon', 'bridge', 'canyon', 'cedar', 'cobalt',
  'compass', 'copper', 'coral', 'delta', 'ember', 'falcon', 'forest', 'granite',
  'harbor', 'indigo', 'ivory', 'jasper', 'juniper', 'lantern', 'marble', 'meadow',
  'mercury', 'nimbus', 'onyx', 'orbit', 'pepper', 'quartz', 'quiver', 'ridge',
  'river', 'saffron', 'sierra', 'silver', 'summit', 'thunder', 'timber', 'tundra',
  'velvet', 'walnut', 'willow', 'zephyr',
]

export function generatePassword() {
  const bytes = crypto.getRandomValues(new Uint32Array(5))
  const words = Array.from(bytes.slice(0, 4), (n) => WORDS[n % WORDS.length])
  return `${words.join('-')}-${String(bytes[4] % 10000).padStart(4, '0')}`
}

export function statusVariant(status) {
  if (status === 'active') return 'active'
  if (status === 'inactive') return 'inactive'
  return 'warning'
}

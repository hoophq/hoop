const MINUTE = 60
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR

// Relative check-in age. Coarse on purpose: the question is "is this current",
// not "how many seconds". Anything past a week reads as a date instead, because
// "63 days ago" is not something anyone counts.
export function timeAgo(iso) {
  if (!iso) return '—'
  const seconds = Math.floor((Date.now() - new Date(iso).getTime()) / 1000)
  if (seconds < 0) return 'just now'
  if (seconds < 45) return 'just now'
  if (seconds < HOUR) return `${Math.floor(seconds / MINUTE)}m ago`
  if (seconds < DAY) return `${Math.floor(seconds / HOUR)}h ago`
  if (seconds < 7 * DAY) return `${Math.floor(seconds / DAY)}d ago`
  // The user's own timezone, never UTC — every admin reads this in local time.
  return new Date(iso).toLocaleDateString(undefined, {
    year: 'numeric',
    month: 'short',
    day: '2-digit',
  })
}

// What the collapsed row says about its resources.
//
// A bare count is the wrong summary: to find the one lane left observe-only you
// would open every row. The exception goes in the collapsed line so expanding
// confirms which, instead of discovering that one exists.
export function resourceSummary(lanes = []) {
  const total = lanes.length
  const noun = total === 1 ? 'resource' : 'resources'
  const observing = lanes.filter((lane) => !lane.enforcing).length
  if (observing === 0) return `${total} ${noun}`
  return `${total} ${noun} · ${observing} observe-only`
}

export function hasObserveOnly(lanes = []) {
  return lanes.some((lane) => !lane.enforcing)
}

// `applied` comes from config.ack and hello, nowhere else. Never infer it from
// "we sent N so it runs N" — that is exactly what config.nack exists to break.
export function generationLabel(generation) {
  if (!generation) return '—'
  const { issued, applied } = generation
  if (applied === issued) return String(issued)
  return `${applied} of ${issued}`
}

// AI routing of a human level's approval (ENG-663): `approvers.<level>.routing`
// of an analyzer spec, the alternative to `groups`. The form keeps the
// fields it does not edit (`rest`, `otherNotify`), so a save writes them back
// unchanged.

// "How approval is done" of a human level. The AI picks the groups (the
// recommended default of a new hold); static routing sends the request to
// every selected group. "By rules" waits for a per-level key on the backend.
export const ROUTING_STATIC = 'static'
export const ROUTING_AI = 'ai'
export const ROUTING_MODES = [
  {
    value: ROUTING_AI,
    label: 'AI Decides',
    description: 'AI selects the best groups from past approvals.',
    recommended: true,
  },
  { value: ROUTING_STATIC, label: 'By groups', description: 'All selected groups get the request.' },
]
const ROUTING_MODE_VALUES = new Set(ROUTING_MODES.map((m) => m.value))
export const isRoutingMode = (mode) => ROUTING_MODE_VALUES.has(mode)

// NotifyRoute.Delivery (sidecar/daemon/levelapprovers.go). Empty means dm.
export const DELIVERY_DM = 'dm'
export const DELIVERIES = [
  { value: DELIVERY_DM, label: 'Direct message' },
  { value: 'channel', label: 'Channel' },
  { value: 'both', label: 'Both' },
]
const DELIVERY_VALUES = new Set(DELIVERIES.map((d) => d.value))
export const isKnownDelivery = (delivery) => DELIVERY_VALUES.has(delivery)
export const deliveryUsesChannels = (delivery) => delivery === 'channel' || delivery === 'both'

// The `notify` keys the plane reads. Each one notifies only when its
// integration is set up; Teams is recorded until the integration ships (ENG-705).
export const NOTIFY_INTEGRATIONS = [
  { key: 'slack', label: 'Slack', description: 'Send requests in Slack.' },
  { key: 'teams', label: 'Teams', description: 'Send requests in Microsoft Teams.' },
]

const isObject = (v) => v != null && typeof v === 'object' && !Array.isArray(v)
const stringList = (v) => (Array.isArray(v) ? v.filter((s) => typeof s === 'string' && s !== '') : [])

const emptyNotifyRoute = () => ({ on: false, delivery: DELIVERY_DM, channels: [], rest: {} })

// `candidates` null lets the AI pick from every group, the ones created later
// included: the backend reads an absent or empty list as "all". A list is
// the user's own selection; an empty one saves as "all" too, so the card
// says so while it is empty.
export const emptyRouting = (mode = ROUTING_AI) => ({
  mode,
  candidates: null,
  notify: Object.fromEntries(NOTIFY_INTEGRATIONS.map(({ key }) => [key, emptyNotifyRoute()])),
  // `notify` keys the plane does not read, as stored.
  otherNotify: {},
  // `routing` fields the form does not edit, as stored.
  rest: {},
})

// The candidates to save: null and an empty list both mean every group.
export const candidatesToSpec = (candidates) => (candidates?.length > 0 ? [...candidates] : null)

// The groups the card shows checked for a stored or edited `candidates`.
export const candidateSelection = (candidates, options) => (candidates === null ? options : candidates)

// The next `candidates` after the user checks `selection`: every known group
// checked is "all" (null), so a group created later is a candidate too.
export function selectCandidates(selection, options) {
  const all = options.length > 0 && options.every((group) => selection.includes(group))
  return all ? null : options.filter((group) => selection.includes(group))
}

// readRouting reads a stored `routing` value. Absent: static routing (the
// level names its groups instead).
export function readRouting(raw) {
  if (!isObject(raw)) return emptyRouting(ROUTING_STATIC)
  const routing = emptyRouting(ROUTING_AI)
  const { candidates, notify, ...rest } = raw
  routing.candidates = candidatesToSpec(stringList(candidates))
  routing.rest = rest
  if (!isObject(notify)) return routing
  for (const [key, value] of Object.entries(notify)) {
    if (!(key in routing.notify)) {
      routing.otherNotify[key] = value
      continue
    }
    // A key that is present uses its integration; an empty value means dm.
    const { delivery, channels_candidates: channels, ...entryRest } = isObject(value) ? value : {}
    routing.notify[key] = {
      on: true,
      delivery: typeof delivery === 'string' && delivery !== '' ? delivery : DELIVERY_DM,
      channels: stringList(channels),
      rest: entryRest,
    }
  }
  return routing
}

// notifyRouteToSpec writes one `notify` entry. A dm delivery is left out, and
// channels are written only for a delivery that posts to channels.
export function notifyRouteToSpec(route) {
  const entry = { ...route.rest }
  if (route.delivery !== DELIVERY_DM) {
    entry.delivery = route.delivery
    if (route.channels.length > 0) entry.channels_candidates = [...route.channels]
  }
  return entry
}

// routingToSpec writes the `routing` value of an AI-routed level. Empty
// candidates and switched-off integrations are left out; with no integration
// on, `notify` is left out and notifications work as before.
export function routingToSpec(routing) {
  const out = { ...routing.rest }
  const candidates = candidatesToSpec(routing.candidates)
  if (candidates) out.candidates = candidates
  const notify = { ...routing.otherNotify }
  for (const { key } of NOTIFY_INTEGRATIONS) {
    if (routing.notify[key].on) notify[key] = notifyRouteToSpec(routing.notify[key])
  }
  if (Object.keys(notify).length > 0) out.notify = notify
  return out
}

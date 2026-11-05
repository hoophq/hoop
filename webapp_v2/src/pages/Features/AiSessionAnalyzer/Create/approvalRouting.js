// AI routing of a human level's approval (ENG-663): `approvers.<level>.routing`
// of an analyzer spec, the alternative to `groups`. The form keeps the
// fields it does not edit (`rest`, `otherNotify`), so a save writes them back
// unchanged.

export const ROUTING_STATIC = 'static'
export const ROUTING_AI = 'ai'
export const ROUTING_MODES = [
  { value: ROUTING_STATIC, label: 'Static' },
  { value: ROUTING_AI, label: 'AI routing' },
]

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
// integration is set up.
export const NOTIFY_INTEGRATIONS = [
  { key: 'slack', label: 'Slack' },
  { key: 'teams', label: 'MS Teams' },
]

const isObject = (v) => v != null && typeof v === 'object' && !Array.isArray(v)
const stringList = (v) => (Array.isArray(v) ? v.filter((s) => typeof s === 'string' && s !== '') : [])

const emptyNotifyRoute = () => ({ on: false, delivery: DELIVERY_DM, channels: [], rest: {} })

export const emptyRouting = () => ({
  mode: ROUTING_STATIC,
  candidates: [],
  notify: Object.fromEntries(NOTIFY_INTEGRATIONS.map(({ key }) => [key, emptyNotifyRoute()])),
  // `notify` keys the plane does not read, as stored.
  otherNotify: {},
  // `routing` fields the form does not edit, as stored.
  rest: {},
})

// readRouting reads a stored `routing` value. Absent: static routing.
export function readRouting(raw) {
  const routing = emptyRouting()
  if (!isObject(raw)) return routing
  const { candidates, notify, ...rest } = raw
  routing.mode = ROUTING_AI
  routing.candidates = stringList(candidates)
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
  if (routing.candidates.length > 0) out.candidates = [...routing.candidates]
  const notify = { ...routing.otherNotify }
  for (const { key } of NOTIFY_INTEGRATIONS) {
    if (routing.notify[key].on) notify[key] = notifyRouteToSpec(routing.notify[key])
  }
  if (Object.keys(notify).length > 0) out.notify = notify
  return out
}

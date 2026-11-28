// AI routing of a human level's approval (ENG-663): `approvers.<level>.routing`
// of an analyzer spec, the alternative to `groups`. A level with groups can
// name its own `approvers.<level>.notify`, in the shape of `routing.notify`;
// the form holds both in `routing.notify`, so a change of mode keeps it. The
// form keeps the fields it does not edit (`rest`, `otherNotify`), so a save
// writes them back unchanged.

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
// says so while it is empty. A new human hold has no mode: the editor picks
// one, and a save refuses a level without it.
export const emptyRouting = (mode = null) => ({
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
// checked is "all" (null), so a group created later is a candidate too. Only
// once the org's groups have `loaded`: before, `options` is the level's own
// names, and checking them all must not widen the selection to every group.
export function selectCandidates(selection, options, loaded) {
  const all = loaded && options.length > 0 && options.every((group) => selection.includes(group))
  return all ? null : options.filter((group) => selection.includes(group))
}

// The approval patch for a change of "How approval is done". The selection
// carries over in its meaning. By groups with no selection made yet starts
// from the candidates, where none (null or empty) means every group; a By
// groups selection already made stays, an empty one included, since it means
// the administrators. AI Decides starts from the groups when it has no
// candidates. Before the groups load, `options` may be empty: the editor
// seeds the level once they arrive.
export function routingModePatch(approval, mode, options, loaded) {
  const { routing, groups } = approval
  const patch = { routing: { ...routing, mode } }
  if (mode === ROUTING_STATIC && groups.length === 0 && !approval.groupsChosen) {
    patch.groups = routing.candidates?.length > 0 ? [...routing.candidates] : [...options]
    patch.groupsChosen = patch.groups.length > 0
  } else if (mode === ROUTING_AI && !routing.candidates?.length && groups.length > 0) {
    patch.routing.candidates = selectCandidates(groups, options, loaded)
  }
  return patch
}

// readNotify reads a stored `notify` value into `routing`.
function readNotify(routing, notify) {
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

// readRouting reads a stored `routing` value. Absent: static routing (the
// level names its groups instead), with the level's own `notify`.
export function readRouting(raw, levelNotify) {
  if (!isObject(raw)) return readNotify(emptyRouting(ROUTING_STATIC), levelNotify)
  const routing = emptyRouting(ROUTING_AI)
  const { candidates, notify, ...rest } = raw
  routing.candidates = candidatesToSpec(stringList(candidates))
  routing.rest = rest
  return readNotify(routing, notify)
}

// notifyRouteToSpec writes one `notify` entry. Channels are written only for
// a delivery that posts to channels. A dm delivery is left out, unless
// `explicit`: a level with groups names it, since it stops the channel post.
export function notifyRouteToSpec(route, explicit = false) {
  const entry = { ...route.rest }
  if (route.delivery !== DELIVERY_DM) {
    entry.delivery = route.delivery
    if (route.channels.length > 0) entry.channels_candidates = [...route.channels]
  } else if (explicit) {
    entry.delivery = DELIVERY_DM
  }
  return entry
}

// notifyToSpec writes the `notify` value: of `routing` for an AI-routed
// level, of the level itself (`explicit`) for one with groups. Switched-off
// integrations are left out. Null with no integration on: `notify` is then
// left out and notifications work as before.
export function notifyToSpec(routing, explicit = false) {
  const notify = { ...routing.otherNotify }
  for (const { key } of NOTIFY_INTEGRATIONS) {
    if (routing.notify[key].on) notify[key] = notifyRouteToSpec(routing.notify[key], explicit)
  }
  return Object.keys(notify).length > 0 ? notify : null
}

// routingToSpec writes the `routing` value of an AI-routed level. Empty
// candidates are left out.
export function routingToSpec(routing) {
  const out = { ...routing.rest }
  const candidates = candidatesToSpec(routing.candidates)
  if (candidates) out.candidates = candidates
  const notify = notifyToSpec(routing)
  if (notify) out.notify = notify
  return out
}

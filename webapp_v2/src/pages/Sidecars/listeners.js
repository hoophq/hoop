import { guardrailMatchers, laneAnalyzer, maskConfigured } from './resolve'
import { LISTENER_FIELDS, PROTOCOLS, appliesTo, listenerAccepts } from './schema'

/**
 * Authoring one listener of a sidecar's configuration.
 *
 * The form state is the listener document itself. Fields come from the
 * schema (./schema); keys it does not list (guardrails, mask, analyzer, opa,
 * the deprecated spellings) are never touched, so they survive a save. The
 * document is decoded with DisallowUnknownFields three times, and absent,
 * empty and non-empty mean different things for those sections.
 */

const str = (v) => (typeof v === 'string' ? v : '')

export function protocolOptions(current) {
  const options = PROTOCOLS.map((p) => ({ value: p.value, label: p.label }))
  // A Select whose value matches no option renders blank, one keystroke from
  // rewriting a working lane.
  if (current && !PROTOCOLS.some((p) => p.value === current)) {
    options.push({ value: current, label: `${current} (unknown)` })
  }
  return options
}

export function getPath(obj, path) {
  return path.split('.').reduce((v, key) => v?.[key], obj)
}

export function setPath(obj, path, value) {
  const [key, ...rest] = path.split('.')
  const next = { ...(obj ?? {}) }
  if (rest.length) next[key] = setPath(next[key], rest.join('.'), value)
  else if (value === undefined) delete next[key]
  else next[key] = value
  return next
}

// A string where the schema has a list: grpc.descriptors takes one path or
// a list (DescriptorPaths.UnmarshalJSON).
function normalize(obj, fields) {
  for (const f of fields) {
    const v = obj[f.key]
    if (f.type === 'list' && typeof v === 'string') obj[f.key] = [v]
    if (f.type === 'object' && v && typeof v === 'object') normalize(v, f.fields ?? [])
  }
  return obj
}

export function listenerToForm(listener) {
  return normalize(structuredClone(listener ?? {}), LISTENER_FIELDS)
}

export function emptyListener() {
  return listenerToForm({ protocol: 'postgres' })
}

function pruneValue(f, v, protocol) {
  if (v === undefined || v === null) return undefined
  switch (f.type) {
    case 'string': {
      const s = String(v).trim()
      return s === '' || s === f.default ? undefined : s
    }
    case 'integer':
      return Number(v) ? Number(v) : undefined
    case 'boolean':
      return v === true ? true : undefined
    case 'list': {
      const items = (Array.isArray(v) ? v : [v]).map((x) => String(x).trim()).filter(Boolean)
      return items.length || f.presence ? items : undefined
    }
    case 'map': {
      const entries = Object.entries(v).filter(([k]) => k.trim())
      return entries.length ? Object.fromEntries(entries) : undefined
    }
    case 'object': {
      const inner = prune(v, f.fields ?? [], protocol)
      // `upstream_tls: {}` dials TLS; only an absent block is plaintext.
      return Object.keys(inner).length || f.presence || f.required ? inner : undefined
    }
    default:
      return v
  }
}

function prune(obj, fields, protocol) {
  const out = { ...obj }
  for (const f of fields) {
    // The daemon refuses the whole config over a block its protocol cannot carry.
    const v = appliesTo(f, protocol) ? pruneValue(f, out[f.key], protocol) : undefined
    if (v === undefined) delete out[f.key]
    else out[f.key] = v
  }
  return out
}

/**
 * The document to save. Zero values are dropped rather than written, because
 * the daemon reads an absent key as its default and an operator also reads
 * this document as YAML.
 */
export function formToListener(form) {
  return prune(form, LISTENER_FIELDS, form.protocol)
}

function schemaErrors(obj, fields, protocol, prefix, errors) {
  for (const f of fields) {
    if (!appliesTo(f, protocol)) continue
    const path = prefix ? `${prefix}.${f.key}` : f.key
    const value = obj?.[f.key]
    if (f.required && f.type !== 'object' && value === undefined) errors[path] = 'Required.'
    if (f.type === 'integer' && Number(value) < 0) errors[path] = 'Cannot be negative.'
    if (f.type === 'object' && (value !== undefined || f.required)) {
      schemaErrors(value ?? {}, f.fields ?? [], protocol, path, errors)
    }
  }
}

// sidecar/daemon/analyzer.go forbiddenHeaders. The gateway does not run the
// daemon's lane validation, so a saved header would stop the sidecar booting.
const FORBIDDEN_HEADERS = ['authorization', 'cookie', 'proxy-authorization', 'set-cookie']
const forbiddenHeader = (values) =>
  (values ?? []).map((v) => String(v).trim()).find((v) => FORBIDDEN_HEADERS.includes(v.toLowerCase()))

/**
 * Field errors, keyed by the field's path in the listener.
 *
 * The generic ones come from the schema. The rest are cross-field checks from
 * the daemon's validateLane, which the gateway does not run: without them a
 * save succeeds and the sidecar refuses to start. They read the sections this
 * form does not render off `original` and `config`.
 */
export function validateListener(form, others = [], original = null, config = null) {
  const l = formToListener(form)
  const errors = {}
  schemaErrors(l, LISTENER_FIELDS, l.protocol, '', errors)

  if (l.name && others.some((o) => o.name === l.name)) {
    errors.name = 'Another listener already uses this name.'
  } else if (l.name && GENERATED_LABEL.test(l.name)) {
    errors.name = 'Reserved: a listener with no name is shown as listener[0], listener[1] and so on.'
  }

  if (l.protocol && !PROTOCOLS.some((p) => p.value === l.protocol)) {
    errors.protocol = `The sidecar does not support "${l.protocol}".`
  }

  const network = l.network === 'unix' ? 'unix' : 'tcp'
  if (l.listen && others.some((o) => (o.network === 'unix' ? 'unix' : 'tcp') === network && o.listen === l.listen)) {
    errors.listen = 'Another listener already binds this address.'
  }

  const dtls = l.downstream_tls
  if (dtls?.cert_file && !dtls.key_file) errors['downstream_tls.key_file'] = 'Required with a certificate.'
  if (dtls?.key_file && !dtls.cert_file) errors['downstream_tls.cert_file'] = 'Required with a key.'

  if (l.mysql_auth_key_file && !l.upstream_tls) errors.mysql_auth_key_file = 'Needs upstream TLS.'

  if (l.spanner?.dialect === 'per_database' && !Object.keys(l.spanner.databases ?? {}).length) {
    errors['spanner.databases'] = 'Needed when the dialect is per_database.'
  }

  const header = forbiddenHeader(l.http?.headers)
  if (header) errors['http.headers'] = `"${header}" may not be exposed to policy.`

  const analyzing = laneAnalyzer(original, config).on
  // maskConfigured, not the resolved rule count: the daemon decides from the
  // raw bytes, so `rules: null` counts as masking.
  const masking = maskConfigured(original, config)
  const ruleTypes = new Set(guardrailMatchers(original, config).map((e) => e.rule?.type))

  if (listenerAccepts(l.protocol, 'http') && analyzing && !l.http?.capture_body) {
    errors['http.capture_body'] = 'This listener runs the AI analyzer, which reads the request body.'
  }

  if (listenerAccepts(l.protocol, 'grpc')) {
    const hasDescriptors = (l.grpc?.descriptors ?? []).length > 0
    const capture = l.grpc?.capture_payload === true
    const metadata = forbiddenHeader(l.grpc?.metadata)
    if (metadata) errors['grpc.metadata'] = `"${metadata}" may not be exposed to policy.`
    if (!hasDescriptors && capture) errors['grpc.descriptors'] = 'Needed to capture payloads.'
    if (!hasDescriptors && l.grpc?.strict) errors['grpc.descriptors'] = 'Needed for strict decoding.'
    if (masking && !hasDescriptors) {
      errors['grpc.descriptors'] = 'Needed to mask: the listener cannot decode a message to rewrite it.'
    }
    if (analyzing && !capture) {
      errors['grpc.capture_payload'] = 'This listener runs the AI analyzer, which reads the payload.'
    }
    if (ruleTypes.has('pii') && !capture) {
      errors['grpc.capture_payload'] = 'This listener has a PII guardrail, which scans the payload.'
    }
    if (l.protocol === 'spanner' && !capture && (ruleTypes.has('operation') || ruleTypes.has('table'))) {
      errors['grpc.capture_payload'] =
        'This listener has operation or table guardrails, which read the SQL inside the payload.'
    }
  }

  return errors
}

export const hasErrors = (errors) => Object.keys(errors).length > 0

/**
 * The problems of a refused save (the 422's `problems`), split between the
 * listener being saved and the others. Every save checks the whole document,
 * so a listener saved invalid earlier refuses this one too. The daemon starts a
 * listener's problem with its name, or with `listener "<name>"`.
 */
export function groupSaveProblems(problems, ownName, otherNames) {
  const names = [ownName, ...otherNames].filter(Boolean).sort((a, b) => b.length - a.length)
  const own = []
  const others = new Map()
  for (const problem of problems) {
    const name = names.find((n) => problem.startsWith(`${n}: `) || problem.startsWith(`listener "${n}": `))
    const text = name ? problem.slice(problem.indexOf(': ') + 2) : problem
    if (!name || name === ownName) own.push(text)
    else others.set(name, [...(others.get(name) ?? []), text])
  }
  return { own, others: [...others].map(([name, list]) => ({ name, problems: list })) }
}

/**
 * What to call a listener that may not have said.
 *
 * `name` is optional in the document, and a config seeded from a file often
 * omits it. The daemon does not store one either: displayName (daemon.go) falls
 * back to `listener[i]` at read time, and that fallback is what its logs, its
 * audit rows and its startup errors print. Using the same string here means the
 * UI names a lane the way the operator already sees it named elsewhere.
 */
export function listenerLabel(listener, index) {
  return listener?.name || `listener[${index}]`
}

// The shape listenerLabel generates for a listener with no name. A real name of
// that shape gives two rows one URL, and the route can only resolve to one of
// them, so the form refuses to write one. Reserving it is what makes the label
// a key rather than a guess: names are already unique, generated labels are
// unique by position, and this keeps the two sets from overlapping.
const GENERATED_LABEL = /^listener\[\d+\]$/

// Where a listener is edited. The route keys on the LABEL, because that is what
// an operator can read in a URL and share, while the editor works on the
// position — so a rename stays one edit rather than a delete and an insert.
export function listenerPath(sidecarId, listener, index) {
  return `/sidecars/${encodeURIComponent(sidecarId)}/listeners/${encodeURIComponent(listenerLabel(listener, index))}`
}

// The position the route's label points at, or -1. Resolving through the same
// label the path was built from is what keeps an unnamed listener reachable.
/**
 * The position a route label refers to, or -1.
 *
 * A NAMED listener wins over a generated one. `listenerLabel` falls back to
 * `listener[i]` for an unnamed lane, and nothing stops the operator from naming
 * a different listener exactly that — at which point a plain findIndex returns
 * whichever comes first in the array, and Edit opens the wrong row. Names are
 * unique (validateListener refuses a duplicate), so preferring them makes the
 * answer deterministic; the generated label only resolves when no listener
 * carries it as a real name.
 */
export function listenerIndexByLabel(listeners, label) {
  const all = listeners ?? []
  const named = all.findIndex((l) => str(l?.name).trim() === label && label !== '')
  if (named !== -1) return named
  return all.findIndex((l, i) => listenerLabel(l, i) === label)
}

// The listeners of a configuration with one replaced or appended, ready to PUT
// as a whole document. The rest of the document is the caller's to carry.
export function replaceListener(configuration, index, listener) {
  const listeners = [...(configuration?.listeners ?? [])]
  if (index === null || index < 0 || index >= listeners.length) listeners.push(listener)
  else listeners[index] = listener
  return { ...(configuration ?? {}), listeners }
}

export function removeListener(configuration, index) {
  const listeners = (configuration?.listeners ?? []).filter((_, i) => i !== index)
  return { ...(configuration ?? {}), listeners }
}

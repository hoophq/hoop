import { PROTOCOLS_ORDER, protocolInfo } from './config'

/**
 * Authoring one listener of a sidecar's configuration.
 *
 * The schema is `daemon.ListenerConfig` in sidecar/daemon/config.go. There are
 * no yaml tags anywhere in the sidecar: sidecar/config/yaml transcodes YAML to
 * JSON and hands the bytes to the same decoder, so the YAML in the docs and
 * this form describe one struct.
 *
 * # The form owns some keys and must not touch the rest
 *
 * A listener also carries `guardrails`, `mask` and `opa`, and the deprecated
 * `connection` and `policy` spellings. This form renders none of them, and
 * every one of them has to survive a round trip untouched:
 *
 *   - The document is decoded with DisallowUnknownFields three times (the API,
 *     the JSONB column, and the sidecar's own loader), so a dropped key is
 *     silent data loss rather than an error anyone sees.
 *   - Their absent / empty / non-empty states mean three different things.
 *     `mask.rules` absent inherits the top-level rules, `[]` switches masking
 *     off, and a non-empty list replaces them. `opa: {}` is how a lane opts out
 *     of an inherited endpoint. Rebuilding the object would flatten all of it.
 *
 * So formToListener spreads the original and writes only OWNED_KEYS. Nothing
 * else is read, written or deleted.
 */

const OWNED_KEYS = [
  'name',
  'protocol',
  'listen',
  'network',
  'upstream',
  'identity_header',
  'idle_timeout_sec',
  'max_conns',
  'upstream_tls',
  'downstream_tls',
  'http',
  'grpc',
]

export const PROTOCOL_OPTIONS = PROTOCOLS_ORDER.map((value) => ({ value, label: protocolInfo(value).label }))

// Only these three terminate the client's TLS. pgwire negotiates it in-band so
// nothing in front can, and a grpc-transport lane is its own HTTP/2 endpoint.
// On anything else the daemon refuses the whole config at startup.
const DOWNSTREAM_TLS_PROTOCOLS = new Set(['postgres', 'grpc', 'spanner'])

const GRPC_PROTOCOLS = new Set(['grpc', 'spanner'])

export const supportsDownstreamTLS = (protocol) => DOWNSTREAM_TLS_PROTOCOLS.has(protocol)
export const supportsHTTPBlock = (protocol) => protocol === 'http'
export const supportsGRPCBlock = (protocol) => GRPC_PROTOCOLS.has(protocol)

const str = (v) => (typeof v === 'string' ? v : '')
const num = (v) => (typeof v === 'number' && Number.isFinite(v) ? v : 0)
const list = (v) => (Array.isArray(v) ? v : [])

// A blank TLS block is not the same as no TLS block. `upstream_tls: {}` still
// dials the backend over TLS, verified against the host trust store — only an
// ABSENT block is plaintext. So the form carries an explicit toggle and cannot
// infer presence from the fields being empty, the way downstream_tls can
// (that one is refused without a keypair, so a keypair is its presence).
function tlsToForm(tls) {
  return {
    ca_file: str(tls?.ca_file),
    cert_file: str(tls?.cert_file),
    key_file: str(tls?.key_file),
    server_name: str(tls?.server_name),
    insecure_skip_verify: tls?.insecure_skip_verify === true,
  }
}

function upstreamTLSFromForm(form) {
  if (!form.upstream_tls_enabled) return undefined
  const tls = form.upstream_tls
  const out = {}
  if (tls.ca_file.trim()) out.ca_file = tls.ca_file.trim()
  if (tls.cert_file.trim()) out.cert_file = tls.cert_file.trim()
  if (tls.key_file.trim()) out.key_file = tls.key_file.trim()
  if (tls.server_name.trim()) out.server_name = tls.server_name.trim()
  if (tls.insecure_skip_verify) out.insecure_skip_verify = true
  return out
}

function downstreamTLSFromForm(form) {
  if (!supportsDownstreamTLS(form.protocol)) return undefined
  const tls = form.downstream_tls
  const cert = tls.cert_file.trim()
  const key = tls.key_file.trim()
  if (!cert && !key) return undefined
  return { cert_file: cert, key_file: key }
}

function httpFromForm(form) {
  if (!supportsHTTPBlock(form.protocol)) return undefined
  const http = form.http
  const headers = http.headers.map((h) => h.trim()).filter(Boolean)
  if (!http.capture_body && !num(http.max_body_bytes) && headers.length === 0) return undefined
  const out = { capture_body: http.capture_body }
  if (num(http.max_body_bytes)) out.max_body_bytes = num(http.max_body_bytes)
  if (headers.length) out.headers = headers
  return out
}

function grpcFromForm(form) {
  if (!supportsGRPCBlock(form.protocol)) return undefined
  const grpc = form.grpc
  const descriptors = grpc.descriptors.map((d) => d.trim()).filter(Boolean)
  const metadata = grpc.metadata.map((m) => m.trim()).filter(Boolean)
  if (
    descriptors.length === 0 &&
    metadata.length === 0 &&
    !grpc.capture_payload &&
    !grpc.strict &&
    !num(grpc.max_payload_bytes)
  ) {
    return undefined
  }
  const out = {}
  if (descriptors.length) out.descriptors = descriptors
  if (grpc.capture_payload) out.capture_payload = true
  if (grpc.strict) out.strict = true
  if (num(grpc.max_payload_bytes)) out.max_payload_bytes = num(grpc.max_payload_bytes)
  if (metadata.length) out.metadata = metadata
  return out
}

export function emptyListener() {
  return listenerToForm({ protocol: 'postgres', network: 'tcp' })
}

export function listenerToForm(listener) {
  const l = listener ?? {}
  return {
    name: str(l.name),
    protocol: str(l.protocol) || 'postgres',
    // "" means tcp in the document; the control is never blank.
    network: l.network === 'unix' ? 'unix' : 'tcp',
    listen: str(l.listen),
    upstream: str(l.upstream),
    identity_header: str(l.identity_header),
    idle_timeout_sec: num(l.idle_timeout_sec),
    max_conns: num(l.max_conns),
    upstream_tls_enabled: !!l.upstream_tls,
    upstream_tls: tlsToForm(l.upstream_tls),
    downstream_tls: tlsToForm(l.downstream_tls),
    http: {
      capture_body: l.http?.capture_body === true,
      max_body_bytes: num(l.http?.max_body_bytes),
      headers: list(l.http?.headers).map(String),
    },
    grpc: {
      // descriptors is one path or a list of them (DescriptorPaths.UnmarshalJSON).
      descriptors: typeof l.grpc?.descriptors === 'string' ? [l.grpc.descriptors] : list(l.grpc?.descriptors),
      capture_payload: l.grpc?.capture_payload === true,
      max_payload_bytes: num(l.grpc?.max_payload_bytes),
      strict: l.grpc?.strict === true,
      metadata: list(l.grpc?.metadata).map(String),
    },
  }
}

/**
 * The form's values merged onto the listener it was opened with.
 *
 * `original` is spread first and only OWNED_KEYS are written, so every section
 * this form does not render survives byte for byte. An owned key whose value
 * builder returns undefined is DELETED rather than written as null: the daemon
 * reads an absent block and a present empty one differently.
 *
 * A block that the new protocol cannot carry is dropped, because the daemon
 * refuses the whole config over it — `http` on a postgres lane, `grpc` off a
 * grpc transport, `downstream_tls` on a lane that terminates none. Changing
 * the protocol is the operator's own action, so this is their edit, not a
 * silent one.
 */
export function formToListener(original, form) {
  const next = { ...(original ?? {}) }
  const values = {
    name: form.name.trim(),
    protocol: form.protocol,
    // tcp is the default the daemon assumes for an absent key, so writing it
    // would only add noise to a document an operator also reads as YAML.
    network: form.network === 'unix' ? 'unix' : undefined,
    listen: form.listen.trim(),
    upstream: form.upstream.trim(),
    identity_header: supportsHTTPBlock(form.protocol) ? form.identity_header.trim() || undefined : undefined,
    idle_timeout_sec: num(form.idle_timeout_sec) || undefined,
    max_conns: num(form.max_conns) || undefined,
    upstream_tls: upstreamTLSFromForm(form),
    downstream_tls: downstreamTLSFromForm(form),
    http: httpFromForm(form),
    grpc: grpcFromForm(form),
  }
  for (const key of OWNED_KEYS) {
    if (values[key] === undefined) delete next[key]
    else next[key] = values[key]
  }
  return next
}

/**
 * Field errors, keyed the way the form reads them.
 *
 * These mirror daemon.ValidateStructure, which the gateway now runs on every
 * write (gateway/services/sidecar.go). The server stays the authority — this
 * only moves the message next to the field instead of into a toast.
 *
 * `others` is every OTHER listener in the same sidecar, so a name or a bind
 * address is checked against the set it is joining. `listen` is keyed by
 * network the same way the daemon keys it.
 */
export function validateListener(form, others = []) {
  const errors = {}
  const name = form.name.trim()
  const listen = form.listen.trim()
  const upstream = form.upstream.trim()

  if (!name) {
    errors.name = 'Required.'
  } else if (others.some((l) => l.name === name)) {
    errors.name = 'Another listener already uses this name.'
  }

  if (!form.protocol) errors.protocol = 'Required.'

  if (!listen) {
    errors.listen = 'Required.'
  } else if (others.some((l) => (l.network === 'unix' ? 'unix' : 'tcp') === form.network && l.listen === listen)) {
    errors.listen = 'Another listener already binds this address.'
  }

  if (!upstream) errors.upstream = 'Required.'

  if (supportsDownstreamTLS(form.protocol)) {
    const cert = form.downstream_tls.cert_file.trim()
    const key = form.downstream_tls.key_file.trim()
    if (cert && !key) errors.downstream_key_file = 'Required with a certificate.'
    if (key && !cert) errors.downstream_cert_file = 'Required with a key.'
  }

  if (supportsGRPCBlock(form.protocol)) {
    const hasDescriptors = form.grpc.descriptors.some((d) => d.trim())
    // grpc.capture_payload and grpc.strict both need a descriptor set to read
    // the protobuf with; the daemon refuses the config without one.
    if (!hasDescriptors && form.grpc.capture_payload) {
      errors.grpc_descriptors = 'Needed to capture payloads.'
    }
    if (!hasDescriptors && form.grpc.strict) {
      errors.grpc_descriptors = 'Needed for strict decoding.'
    }
  }

  return errors
}

export const hasErrors = (errors) => Object.keys(errors).length > 0

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

// Where a listener is edited. The route keys on the LABEL, because that is what
// an operator can read in a URL and share, while the editor works on the
// position — so a rename stays one edit rather than a delete and an insert.
export function listenerPath(sidecarId, listener, index) {
  return `/sidecars/${encodeURIComponent(sidecarId)}/listeners/${encodeURIComponent(listenerLabel(listener, index))}`
}

// The position the route's label points at, or -1. Resolving through the same
// label the path was built from is what keeps an unnamed listener reachable.
export function listenerIndexByLabel(listeners, label) {
  return (listeners ?? []).findIndex((l, i) => listenerLabel(l, i) === label)
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

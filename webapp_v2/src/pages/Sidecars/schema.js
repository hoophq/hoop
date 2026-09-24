import schema from '@sidecar-schema'

// sidecar/daemon/schema.json, generated from the tags on daemon.ListenerConfig.
// The listener form renders from it, so a field the daemon gains shows up here
// without a change to this app.
export const LISTENER_FIELDS = schema.fields
export const PROTOCOLS = schema.protocols

export const protocolLabel = (value) => PROTOCOLS.find((p) => p.value === value)?.label ?? value

export function appliesTo(field, protocol) {
  if (field.protocols) return field.protocols.includes(protocol)
  if (field.except_protocols) return !field.except_protocols.includes(protocol)
  return true
}

// Whether a listener of this protocol carries the top-level block `key`.
export function listenerAccepts(protocol, key) {
  const field = LISTENER_FIELDS.find((f) => f.key === key)
  return !!field && appliesTo(field, protocol)
}

// What a sidecar reported it accepts. A sidecar that reported nothing has not
// connected yet, or predates the report, and nothing is refused for it.
export function sidecarSupport(sidecar) {
  const keys = sidecar?.supported_config_keys?.length ? new Set(sidecar.supported_config_keys) : null
  const protocols = sidecar?.supported_protocols?.length ? new Set(sidecar.supported_protocols) : null
  return {
    key: (path) => !keys || keys.has(`listeners.${path}`),
    protocol: (value) => !protocols || protocols.has(value),
  }
}

export const ALL_SUPPORTED = sidecarSupport(null)

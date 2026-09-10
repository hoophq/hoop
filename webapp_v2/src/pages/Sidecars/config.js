import { ShieldCheck, Sparkles, VenetianMask } from 'lucide-react'

// Reading a sidecar's stored configuration (daemon.Config,
// sidecar/daemon/config.go), the document the control plane holds and serves to
// the sidecar on its handshake (SidecarResponse.configuration).
// A feature is "on" when the config asks for it: a non-empty rule list turns
// guardrails or masking on, an `analyzer` block turns the AI analyzer on. A
// lane's own guardrails/mask block overrides the top-level default, so the
// per-listener view resolves that inheritance the way the daemon does.

export const FEATURES = {
  'ai-analyzer': { key: 'ai-analyzer', label: 'AI Analyzer', icon: Sparkles, color: 'pink' },
  'data-masking': { key: 'data-masking', label: 'Data Masking', icon: VenetianMask, color: 'violet' },
  guardrails: { key: 'guardrails', label: 'Guardrails', icon: ShieldCheck, color: 'indigo' },
}

const FEATURE_ORDER = ['ai-analyzer', 'data-masking', 'guardrails']

const PROTOCOLS = {
  postgres: { label: 'PostgreSQL', subtype: 'postgres' },
  mysql: { label: 'MySQL', subtype: 'mysql' },
  mssql: { label: 'SQL Server', subtype: 'mssql' },
  http: { label: 'HTTP', subtype: 'httpproxy' },
}

export function protocolInfo(protocol) {
  return PROTOCOLS[protocol] ?? { label: protocol, subtype: protocol }
}

const hasRules = (section) => Array.isArray(section?.rules) && section.rules.length > 0

// Config.resolve in sidecar/daemon/config.go merges a lane's overrides onto the
// top-level defaults, and the two sections do NOT merge the same way. Reading
// the presence of the whole `guardrails`/`mask` block instead reported a lane
// that overrides only `mode` as running no guardrails, while the daemon was
// still applying the inherited rules.

// `rules` absent inherits the default; `rules: []` is how a lane runs none
// against a top-level set; a non-empty list concatenates with the default.
function guardrailsOn(listener, config) {
  const own = listener?.guardrails
  if (own?.rules === undefined) return hasRules(config?.guardrails)
  return own.rules.length > 0
}

// Only a non-empty lane list replaces the default (`o != nil && len(o.Rules) > 0`),
// so nothing a lane writes can remove inherited masking.
const maskOn = (listener, config) => hasRules(listener?.mask) || hasRules(config?.mask)

export function listenerFeatures(listener, config) {
  const on = []
  if (config?.analyzer) on.push('ai-analyzer')
  if (maskOn(listener, config)) on.push('data-masking')
  if (guardrailsOn(listener, config)) on.push('guardrails')
  return on
}

// Every feature at least one lane runs with.
export function configFeatures(config) {
  if (!config) return []
  const listeners = config.listeners ?? []
  const all = new Set(listeners.flatMap((l) => listenerFeatures(l, config)))
  if (listeners.length === 0) listenerFeatures(null, config).forEach((f) => all.add(f))
  return FEATURE_ORDER.filter((f) => all.has(f))
}

// The daemon records to stdout when `audit.file` is empty or "-", and a
// deployment that wants no trail points the file at /dev/null
// (buildAudit, sidecar/daemon/daemon.go). So audit is on unless the file is
// a null device.
const NULL_DEVICES = ['/dev/null', 'nul', 'NUL']

export function auditEnabled(config) {
  if (!config) return false
  return !NULL_DEVICES.includes(config.audit?.file ?? '')
}

// A sidecar the control plane has no configuration for cannot start: it has no
// listeners, and the daemon refuses a config without them.
export function hasConfiguration(config) {
  return !!config && (config.listeners ?? []).length > 0
}

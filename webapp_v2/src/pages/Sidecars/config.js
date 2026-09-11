import { ShieldCheck, Sparkles, VenetianMask } from 'lucide-react'
import { guardrailRules, maskRules } from './resolve'

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

// Every protocol a listener can declare, which is the codec registry
// (sidecar/codec/all) plus grpc and spanner — those two have no codec on
// purpose (ADR-0013) and are carved out of the daemon's own validation.
//
// `subtype` is a connections-metadata key, only for the protocols that are
// also a hoop connection type. grpc and spanner are not, so they carry none
// and render without an icon rather than falling back to an unrelated one.
const PROTOCOLS = {
  postgres: { label: 'PostgreSQL', subtype: 'postgres' },
  mysql: { label: 'MySQL', subtype: 'mysql' },
  mssql: { label: 'SQL Server', subtype: 'mssql' },
  mongodb: { label: 'MongoDB', subtype: 'mongodb' },
  http: { label: 'HTTP', subtype: 'httpproxy' },
  grpc: { label: 'gRPC', subtype: null },
  spanner: { label: 'Cloud Spanner', subtype: null },
}

export const PROTOCOLS_ORDER = ['postgres', 'mysql', 'mssql', 'mongodb', 'http', 'grpc', 'spanner']

export function protocolInfo(protocol) {
  return PROTOCOLS[protocol] ?? { label: protocol, subtype: protocol }
}

// Whether a lane runs a feature is a question about its RESOLVED rules, so both
// answers come from resolve.js rather than re-reading the document here. The
// two sections inherit by different rules and each has its own opt-out
// spelling; keeping that logic in one place is what stops the chips from
// disagreeing with the lane detail that renders beside them.

export function listenerFeatures(listener, config) {
  const on = []
  if (config?.analyzer) on.push('ai-analyzer')
  if (maskRules(listener, config).length > 0) on.push('data-masking')
  if (guardrailRules(listener, config).length > 0) on.push('guardrails')
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

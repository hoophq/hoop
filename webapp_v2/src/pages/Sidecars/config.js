import { ShieldCheck, Sparkles, VenetianMask } from 'lucide-react'

// Reading a sidecar's reported config (daemon.Config, sidecar/daemon/config.go).
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

// The guardrails/mask block a lane runs with: its own when present, else the
// config's default.
const resolved = (listener, config, key) => (listener?.[key] !== undefined ? listener[key] : config?.[key])

export function listenerFeatures(listener, config) {
  const on = []
  if (config?.analyzer) on.push('ai-analyzer')
  if (hasRules(resolved(listener, config, 'mask'))) on.push('data-masking')
  if (hasRules(resolved(listener, config, 'guardrails'))) on.push('guardrails')
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

// Audit is on unless the config turns the sink off entirely.
export function auditEnabled(config) {
  return !!config && config.audit?.file !== ''
}

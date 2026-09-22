import { ShieldCheck, Sparkles, VenetianMask } from 'lucide-react'
import { guardrailMatchers, laneAnalyzer, maskRules } from './resolve'

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

// What the picker offers, and it deliberately trails the schema.
//
// The published reference (setup/configuration/hoop-sidecar/config-file, the
// Listeners table) names six protocols. `spanner` is the seventh and the daemon
// accepts it, but the docs still carry it as unreleased, so offering it here
// would put a choice in front of an operator that the documentation denies.
// The frontend stays behind on purpose: when the docs gain it, this list does
// too, in the same change.
//
// This is ONLY about what a new lane may choose. PROTOCOLS above still knows
// every protocol so an existing lane renders with its real name, and nothing in
// listeners.js narrows what it will WRITE — a lane the docs do not mention must
// still round-trip byte for byte.
export const PROTOCOLS_ORDER = ['postgres', 'mysql', 'mssql', 'mongodb', 'http', 'grpc']

// Every protocol the daemon accepts, offered or not. The write path reads this
// one, so a lane already on an unlisted protocol keeps its blocks.
export const PROTOCOLS_SUPPORTED = ['postgres', 'mysql', 'mssql', 'mongodb', 'http', 'grpc', 'spanner']

export function protocolInfo(protocol) {
  return PROTOCOLS[protocol] ?? { label: protocol, subtype: protocol }
}

// Whether a lane runs a feature is a question about its RESOLVED rules, so both
// answers come from resolve.js rather than re-reading the document here. The
// two sections inherit by different rules and each has its own opt-out
// spelling; keeping that logic in one place is what stops the chips from
// disagreeing with the lane detail that renders beside them.

// The feature a bound rule turns on. The API's kinds, not ours.
const BOUND_KIND_FEATURE = {
  guardrail: 'guardrails',
  datamasking: 'data-masking',
  analyzer: 'ai-analyzer',
}

// The rules the control plane distributes to one lane.
//
// They are not in the stored configuration and never will be: composition
// folds them into the SERVED document on each handshake and stores nothing,
// which is what makes editing a rule bound to three hundred sidecars one row
// update. So the chips read the stored document AND this list, or a listener
// enforcing a distributed rule would render as enforcing nothing.
export function boundFeatures(boundRules, listenerName) {
  const on = new Set()
  for (const b of boundRules ?? []) {
    if (listenerName != null && b.listener_name !== listenerName) continue
    const feature = BOUND_KIND_FEATURE[b.kind]
    if (feature) on.add(feature)
  }
  return [...on]
}

// The rule NAMES a lane gets from the control plane, for the listener detail.
export function boundRulesFor(boundRules, listenerName) {
  return (boundRules ?? []).filter((b) => b.listener_name === listenerName)
}

export function listenerFeatures(listener, config, boundRules) {
  const on = new Set(boundFeatures(boundRules, listener?.name))
  if (laneAnalyzer(listener, config).on) on.add('ai-analyzer')
  if (maskRules(listener, config).length > 0) on.add('data-masking')
  if (guardrailMatchers(listener, config).length > 0) on.add('guardrails')
  return FEATURE_ORDER.filter((f) => on.has(f))
}

// Every feature at least one lane runs with.
export function configFeatures(config, boundRules) {
  if (!config) return []
  const listeners = config.listeners ?? []
  const all = new Set(listeners.flatMap((l) => listenerFeatures(l, config, boundRules)))
  if (listeners.length === 0) listenerFeatures(null, config).forEach((f) => all.add(f))
  // A rule bound to a listener the document no longer has still reaches the
  // sidecar's answer as a refusal, so the chip is not dropped quietly here.
  boundFeatures(boundRules).forEach((f) => all.add(f))
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

// The control plane released this sidecar's configuration: it answers the
// handshake with the license alone and the sidecar runs its own config file
// (resolveConfigSource, sidecar/daemon/controlplane.go). The stored document
// below stays readable and is not applied.
export const loadsFromDisk = (sidecar) => sidecar?.configuration?.load_from_disk === true

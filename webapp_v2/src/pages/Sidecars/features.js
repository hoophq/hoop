import { FEATURES, FEATURE_ORDER } from './config'
import {
  SOURCE_DISTRIBUTED,
  SOURCE_INHERITED,
  SOURCE_LISTENER,
  guardrailMatchers,
  guardrailMode,
  laneAnalyzer,
  maskRules,
  resolveOPA,
} from './resolve'

// `listener` null reads the sidecar's defaults plus every distributed rule;
// a listener reads what that lane resolves to plus the rules bound to its name.

// The rule types of sidecar/policy a guardrail list can show. NOT the two in
// pages/Guardrails/helpers.js — those belong to the gateway's own guardrails,
// a different product with a different schema.
const RULE_TYPE_LABELS = {
  deny_words_list: 'Deny words',
  pattern_match: 'Pattern match',
  operation: 'Operation',
  table: 'Table',
  pii: 'PII',
  http_resource: 'HTTP resource',
  http_status: 'HTTP status',
  grpc_status: 'gRPC status',
}

// LaneAnalyzerConfig.HighRisk/MediumRisk/LowRisk (sidecar/daemon/analyzer.go).
// An unset level allows, so a level nobody named is left out.
const RISK_ACTIONS = {
  allow: 'allows',
  warn: 'warns',
  block: 'blocks',
  defer: 'reports',
}

const MASK_STRATEGY_LABELS = {
  redact: 'Redact',
  mask: 'Mask',
  partial: 'Partial',
  hash: 'Hash',
}

const FEATURE_KIND = {
  'ai-analyzer': 'analyzer',
  'data-masking': 'datamasking',
  guardrails: 'guardrail',
}

const join = (v) => (Array.isArray(v) && v.length > 0 ? v.join(', ') : null)

// The part of a rule that says what it matches. Each type reads its own
// fields and ignores the rest (policy.Rule is one struct for all nine).
function ruleMatcher(rule) {
  switch (rule.type) {
    case 'operation':
      return join(rule.operations)
    case 'table': {
      const tables = join(rule.tables)
      if (!tables) return null
      return rule.access ? `${tables} (${rule.access})` : tables
    }
    case 'deny_words_list':
      return join(rule.words)
    case 'pattern_match':
      return rule.pattern_regex || null
    case 'pii':
      return join(rule.entities)
    case 'http_resource':
      return [join(rule.methods), join(rule.resources)].filter(Boolean).join(' ') || null
    case 'http_status':
    case 'grpc_status':
      return [join(rule.methods), join(rule.statuses)].filter(Boolean).join(' ') || null
    default:
      return null
  }
}

function guardrailDetail(rule) {
  return [RULE_TYPE_LABELS[rule.type] ?? rule.type, ruleMatcher(rule)].filter(Boolean).join(' · ')
}

function maskDetail(rule) {
  const target = join(rule.columns) || join(rule.entities) || rule.entity || null
  const strategy = MASK_STRATEGY_LABELS[rule.strategy] ?? MASK_STRATEGY_LABELS.redact
  // keep_last only means anything to the partial strategy; its default is 4.
  const keepLast = rule.strategy === 'partial' ? `keep last ${rule.keep_last ?? 4}` : null
  return [target, strategy, keepLast].filter(Boolean).join(' · ')
}

// A lane's `analyzer` block and a deprecated `ai_analysis` rule carry the same
// six keys (specFromRule, sidecar/daemon/analyzer.go), so one reader serves both.
function analyzerDetail(spec) {
  const trigger = [spec.trigger?.operations, spec.trigger?.tables, spec.trigger?.resources]
    .map(join)
    .filter(Boolean)
    .join(', ')
  const risks = ['high', 'medium', 'low']
    .filter((level) => spec[level])
    .map((level) => `${level} ${RISK_ACTIONS[spec[level]] ?? spec[level]}`)
  const prompt = spec.prompt ? 'custom prompt' : null
  // An omitted trigger is not "nothing": declaring the analyzer is the opt-in.
  return [trigger || 'Every statement', ...risks, prompt].filter(Boolean).join(' · ')
}

// Where distributed rules are read from: the whole sidecar when there is no
// listener, that lane's name otherwise. A lane with no name gets none — the
// name is the only handle a binding has, so nothing can be bound to it — and
// must not fall through to the sidecar-wide list.
const scopeOf = (listener) => (listener == null ? null : listener.name || '')

// Seen from the sidecar itself, its top-level rules are its own, not inherited.
export const SOURCE_SIDECAR = 'sidecar'
const sourceOf = (listener, source) => (listener == null && source === SOURCE_INHERITED ? SOURCE_SIDECAR : source)

// The binding carries no rule body, so a row is the name and where it goes.
function distributedRules(boundRules, listener, kind) {
  const scope = scopeOf(listener)
  if (scope === '') return []
  const rows = (boundRules ?? []).filter((b) => b.kind === kind && (scope === null || b.listener_name === scope))
  if (scope !== null) {
    return rows.map((b) => ({ id: `cp-${b.rule_name}`, name: b.rule_name, source: SOURCE_DISTRIBUTED }))
  }
  const lanes = new Map()
  for (const b of rows) {
    if (!lanes.has(b.rule_name)) lanes.set(b.rule_name, [])
    lanes.get(b.rule_name).push(b.listener_name)
  }
  return [...lanes].map(([name, names]) => ({
    id: `cp-${name}`,
    name,
    source: SOURCE_DISTRIBUTED,
    detail: `On ${names.join(', ')}`,
  }))
}

function guardrailRows(listener, config, boundRules) {
  const own = guardrailMatchers(listener, config).map((entry, i) => ({
    id: `${entry.rule.name}-${i}`,
    name: entry.rule.name,
    detail: guardrailDetail(entry.rule),
    source: sourceOf(listener, entry.source),
    // action: "defer" reports a finding instead of denying.
    flag: entry.rule.action === 'defer' ? 'Report only' : null,
  }))
  return [...distributedRules(boundRules, listener, FEATURE_KIND.guardrails), ...own]
}

function maskRows(listener, config, boundRules) {
  const own = maskRules(listener, config).map((entry, i) => ({
    id: `${entry.rule.name}-${i}`,
    name: entry.rule.name,
    detail: maskDetail(entry.rule),
    source: sourceOf(listener, entry.source),
  }))
  return [...distributedRules(boundRules, listener, FEATURE_KIND['data-masking']), ...own]
}

function analyzerRows(listener, config, boundRules) {
  const { block, deprecated } = laneAnalyzer(listener, config)
  const rows = distributedRules(boundRules, listener, FEATURE_KIND['ai-analyzer'])
  if (block) {
    rows.push({ id: 'analyzer-block', name: 'Analyzer', detail: analyzerDetail(block), source: SOURCE_LISTENER })
  }
  for (const [i, entry] of deprecated.entries()) {
    rows.push({
      id: `${entry.rule.name}-${i}`,
      name: entry.rule.name,
      detail: analyzerDetail(entry.rule),
      source: sourceOf(listener, entry.source),
      // Still runs, through the same builder the block does. Marked because
      // the rule form carries no per-lane overrides and the block does.
      flag: 'Deprecated',
    })
  }
  return rows
}

const ROWS = {
  'ai-analyzer': analyzerRows,
  'data-masking': maskRows,
  guardrails: guardrailRows,
}

function summary(key, rules) {
  if (rules.length === 0) return 'No rule configured'
  if (key === 'ai-analyzer' && rules.length === 1 && rules[0].id === 'analyzer-block') return 'Configured'
  return `${rules.length} ${rules.length === 1 ? 'rule' : 'rules'}`
}

export function featureList(listener, config, boundRules) {
  return FEATURE_ORDER.map((key) => {
    const rules = ROWS[key](listener, config, boundRules)
    return {
      ...FEATURES[key],
      rules,
      summary: summary(key, rules),
      mode: key === 'guardrails' ? guardrailMode(listener, config) : null,
    }
  })
}

export { resolveOPA }

export function opaSummary(opa) {
  return [opa.url, opa.fail_open ? 'allows on failure' : 'denies on failure', opa.gate && 'gates the analyzer']
    .filter(Boolean)
    .join(' · ')
}

// Whether a lane writes its own `opa` key. Null inherits like an absent key
// (resolve.js); `{}` is the opt-out, so it counts as the lane's own.
export const overridesOPA = (listener) => listener?.opa !== undefined && listener?.opa !== null

// Chips only when the lane has a rule of its own or a distributed one; a lane
// running nothing but the sidecar's defaults reads "Inherited policy".
export function listenerPolicies(listener, config, boundRules) {
  const on = featureList(listener, config, boundRules).filter((f) => f.rules.length > 0)
  const own = on.some((f) => f.rules.some((r) => r.source !== SOURCE_INHERITED))
  return { features: own ? on.map((f) => f.key) : [], inheritedOnly: on.length > 0 && !own }
}

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

/**
 * The rules behind each feature chip, as rows a panel can render.
 *
 * `listener` null reads the sidecar's own defaults: the top-level blocks every
 * lane inherits, plus every rule the control plane distributes to any lane.
 * With a listener it is what that lane resolves to (resolve.js) plus the rules
 * distributed to it by name.
 */

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

// The API's binding kinds, by feature.
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

// The rules the control plane sends, as rows beside the document's own. The
// binding carries no rule body, so a row is the name and where it goes.
function distributedRules(boundRules, listener, kind) {
  const scope = scopeOf(listener)
  if (scope === '') return []
  const rows = (boundRules ?? []).filter((b) => b.kind === kind && (scope === null || b.listener_name === scope))
  if (scope !== null) {
    return rows.map((b) => ({ id: `cp-${b.rule_name}`, name: b.rule_name, source: SOURCE_DISTRIBUTED }))
  }
  // Sidecar-wide: one row per rule, naming the lanes it reaches.
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
    source: entry.source,
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
    source: entry.source,
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
      source: entry.source,
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

const EMPTY = {
  'ai-analyzer': 'Not used. Nothing is sent to a model.',
  'data-masking': 'No rules. Responses are returned unchanged.',
  guardrails: 'No rules. Everything passes.',
}

function summary(key, rules) {
  if (rules.length === 0) return 'No rule configured'
  if (key === 'ai-analyzer' && rules.length === 1 && rules[0].id === 'analyzer-block') return 'Configured'
  return `${rules.length} ${rules.length === 1 ? 'rule' : 'rules'}`
}

// One entry per feature, in display order, with its rows and the header summary.
export function featureList(listener, config, boundRules) {
  return FEATURE_ORDER.map((key) => {
    const rules = ROWS[key](listener, config, boundRules)
    return {
      ...FEATURES[key],
      rules,
      summary: summary(key, rules),
      empty: EMPTY[key],
      // Only guardrails carry an enforcement mode.
      mode: key === 'guardrails' ? guardrailMode(listener, config) : null,
    }
  })
}

export { resolveOPA }

/**
 * What the Policies cell says about a lane.
 *
 * Chips when the lane carries a rule of its own or one the control plane
 * distributes to it. "Inherited policy" when everything it runs comes from the
 * sidecar's top-level defaults, and nothing when it runs none at all. The
 * distinction is the row's whole point: two lanes with the same chips can be
 * one that was configured and one that merely inherits.
 */
export function listenerPolicies(listener, config, boundRules) {
  const on = featureList(listener, config, boundRules).filter((f) => f.rules.length > 0)
  const own = on.some((f) => f.rules.some((r) => r.source !== SOURCE_INHERITED))
  return { features: own ? on.map((f) => f.key) : [], inheritedOnly: on.length > 0 && !own }
}

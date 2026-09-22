/**
 * One row per RULE, for the control plane's guardrail and masking lists.
 *
 * A guardrail or a masking record is a named container: the API stores it with
 * `sidecar_spec.rules`, a list, and one record can hold a dozen. The list
 * pages used to draw one row per container, so the thing an operator names,
 * reasons about and goes looking for — the rule — had no row of its own and no
 * way to be scanned. This flattens the container out of the way. It stays in
 * the data: a row carries its record, because that is what Edit opens and what
 * the API still writes.
 *
 * The summaries live here rather than in each page because the listener detail
 * panel (pages/Sidecars/sections/ListenerDetails) describes the same rules, and
 * two descriptions of one rule type are two chances to disagree about it.
 */

const join = (v) => (Array.isArray(v) && v.length > 0 ? v.join(', ') : null)

// The eight guardrail rule types a sidecar runs. NOT the gateway's two — see
// pages/sidecarRuleVocabulary for why they share only names.
//
// `ai_analysis` is the ninth and is absent on purpose: the daemon sorts it out
// of the guardrails and reports it beside the analyzer block instead
// (splitAnalyzerRules).
export const GUARDRAIL_TYPE_LABELS = {
  deny_words_list: 'Deny words',
  pattern_match: 'Pattern match',
  operation: 'Operation',
  table: 'Table',
  pii: 'PII',
  http_resource: 'HTTP resource',
  http_status: 'HTTP status',
  grpc_status: 'gRPC status',
}

export const MASK_STRATEGY_LABELS = {
  redact: 'Redact',
  mask: 'Mask',
  partial: 'Partial',
  hash: 'Hash',
}

// The part of a guardrail rule that says what it matches. Each type reads its
// own fields and ignores the rest: policy.Rule is one struct for all of them.
export function guardrailMatcher(rule) {
  switch (rule?.type) {
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

export function guardrailSummary(rule) {
  return [GUARDRAIL_TYPE_LABELS[rule?.type] ?? rule?.type, guardrailMatcher(rule)].filter(Boolean).join(' · ')
}

export function maskSummary(rule) {
  const target = join(rule?.columns) || join(rule?.entities) || rule?.entity || null
  const strategy = MASK_STRATEGY_LABELS[rule?.strategy] ?? MASK_STRATEGY_LABELS.redact
  // keep_last only means anything to the partial strategy; its default is 4.
  const keepLast = rule?.strategy === 'partial' ? `keep last ${rule.keep_last ?? 4}` : null
  return [target, strategy, keepLast].filter(Boolean).join(' · ')
}

/**
 * Every rule of every record, as rows.
 *
 * A rule has no id — it is an element of a JSONB list — so the key is the
 * record plus the position, which is also how the form addresses it. An
 * unnamed rule keeps its row: the API does not require a name, and dropping
 * the row would hide a rule that is being enforced.
 */
export function ruleRows(records, summarise) {
  return (records ?? []).flatMap((record) =>
    (record.sidecar_spec?.rules ?? []).map((rule, index) => ({
      key: `${record.id ?? record.name}#${index}`,
      name: rule?.name || 'Unnamed rule',
      unnamed: !rule?.name,
      summary: summarise(rule),
      record,
      targets: record.sidecar_targets ?? [],
    }))
  )
}

/**
 * Where a rule is distributed, as short labels.
 *
 * A sidecar whose listeners are ALL named collapses to one label: a rule on a
 * twelve-lane sidecar listed twelve chips that said nothing a reader could
 * use. `sidecarsById` is the fleet.
 *
 * What does NOT collapse is a target the fleet cannot account for — a listener
 * that was renamed or deleted, or a sidecar that is gone. Composition refuses
 * the whole handshake over one of those (listenerIndex, sidecarconfig.go), so
 * it is the single most useful thing this cell can show, and folding it into
 * a tidy "all listeners" would hide the reason a sidecar stopped updating.
 */
export function targetSummary(targets, sidecarsById) {
  const byId = new Map()
  for (const t of targets ?? []) {
    if (!byId.has(t.sidecar_id)) byId.set(t.sidecar_id, [])
    byId.get(t.sidecar_id).push(t.listener_name)
  }
  const labels = []
  for (const [sidecarID, listeners] of byId) {
    const sidecar = sidecarsById?.get(sidecarID)
    const name = sidecar?.name ?? 'Unknown sidecar'
    // Set membership, not a count: two targets naming listeners the sidecar no
    // longer has reach the same count as two that exist, and the row would
    // then claim a coverage the rule has not got.
    const bindable = (sidecar?.configuration?.listeners ?? []).filter((l) => l?.name)
    const known = new Set(bindable.map((l) => l.name))
    const named = new Set(listeners)
    // "(missing)" is only said where the answer is known. A sidecar the fleet
    // has not got, or one the control plane stores no listeners for because it
    // reads its own config file, cannot tell us a target is stale — and
    // marking every one of those would cry wolf on a working rule.
    const checkable = bindable.length > 0
    if (checkable && bindable.every((l) => named.has(l.name))) {
      labels.push(`${name} · all listeners`)
      for (const listener of listeners) {
        if (!known.has(listener)) labels.push(`${name} · ${listener} (missing)`)
      }
      continue
    }
    for (const listener of listeners) {
      labels.push(checkable && !known.has(listener) ? `${name} · ${listener} (missing)` : `${name} · ${listener}`)
    }
  }
  return labels
}

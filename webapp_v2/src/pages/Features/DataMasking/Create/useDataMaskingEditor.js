import { useMemo, useState } from 'react'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { showSnackbar } from '@/utils/snackbar'
import { maskingAgentBlockers } from '@/utils/ruleSupport'
import { DEFAULT_KEEP_LAST, MASK_STRATEGIES, SSH_ONLY_STRATEGY } from '@/pages/sidecarRuleVocabulary'
import { useDataMaskingStore } from '../store'
import { normalizeEntityName, scoreToPercent } from '../helpers'

const newKey = () => Math.random().toString(36).slice(2)

export const emptyRule = () => ({
  key: newKey(),
  name: '',
  match: 'entities',
  entities: [],
  columns: [],
  strategy: 'redact',
  keep_last: DEFAULT_KEEP_LAST,
  mask_char: '',
})

export const emptyCustomEntity = () => ({ key: newKey(), name: '', regex: '' })

// The spec is the UI's truth; without one, each agent entity group becomes a
// rule masked in place, the closest sidecar strategy to what a DLP provider does.
function loadRules(stored) {
  const spec = stored?.sidecar_spec?.rules
  const rules = spec
    ? spec.map((r) => ({
        ...emptyRule(),
        ...r,
        match: (r.columns ?? []).length > 0 ? 'columns' : 'entities',
        strategy: r.strategy || 'redact',
        keep_last: r.keep_last ?? DEFAULT_KEEP_LAST,
        mask_char: r.mask_char ? String.fromCodePoint(r.mask_char) : '',
        key: newKey(),
      }))
    : (stored?.supported_entity_types ?? []).map((entry) => ({
        ...emptyRule(),
        name: entry.name,
        entities: entry.entity_types ?? entry.values ?? [],
        strategy: 'mask',
      }))
  return rules.length > 0 ? rules : [emptyRule()]
}

function rulesToSpec(rules, sshBound) {
  const out = rules
    .filter((r) => r.name.trim() !== '')
    .map((r) => {
      // Derived at save time: which lanes are bound changes with the picker.
      const strategy = sshBound ? SSH_ONLY_STRATEGY : r.strategy
      const rule = { name: r.name.trim(), strategy }
      if (r.match === 'columns') rule.columns = r.columns
      else rule.entities = r.entities
      // Only for the strategy that reads it.
      if (strategy === 'partial') rule.keep_last = Number(r.keep_last)
      if ((strategy === 'mask' || strategy === 'partial') && r.mask_char !== '') {
        // The daemon takes a Go rune, which is the code point as a number.
        rule.mask_char = r.mask_char.codePointAt(0)
      }
      return rule
    })
  return { rules: out }
}

// Shared by the rule page and the sidecar dialog. `targets` seeds a new rule's listeners.
export function useDataMaskingEditor({ rule: stored, id, isEdit, targets: seed = [], onSaved, onDeleted }) {
  const submitting = useDataMaskingStore((s) => s.submitting)
  const createRule = useDataMaskingStore((s) => s.createRule)
  const updateRule = useDataMaskingStore((s) => s.updateRule)
  const deleteRule = useDataMaskingStore((s) => s.deleteRule)
  const sidecars = useSidecarStore((s) => s.sidecars)

  const [name, setName] = useState(stored?.name ?? '')
  const [description, setDescription] = useState(stored?.description ?? '')
  const [connectionIds, setConnectionIds] = useState(stored?.connection_ids ?? [])
  const [attributes, setAttributes] = useState(stored?.attributes ?? [])
  const [targets, setTargets] = useState(stored?.sidecar_targets ?? seed)
  const [rules, setRules] = useState(() => loadRules(stored))
  const [customEntities, setCustomEntities] = useState(
    () => stored?.custom_entity_types?.map((c) => ({ key: newKey(), name: c.name, regex: c.regex })) ?? [],
  )
  // Percent, '' = unset. A new blank rule starts at 85; a stored or template
  // rule keeps its own value so saving adds no threshold it did not have.
  const [scoreThreshold, setScoreThreshold] = useState(() =>
    stored ? scoreToPercent(stored.score_threshold) : 85,
  )

  // An ssh lane masks bytes in place, so only the length-preserving strategy is offered.
  const sshBound = useMemo(() => {
    const byId = new Map(sidecars.map((sc) => [sc.id, sc]))
    return targets.some((t) => {
      const lane = byId.get(t.sidecar_id)?.configuration?.listeners?.find((l) => l.name === t.listener_name)
      return (lane?.protocol ?? '').toLowerCase() === 'ssh'
    })
  }, [targets, sidecars])

  const strategies = sshBound ? MASK_STRATEGIES.filter((s) => s.value === SSH_ONLY_STRATEGY) : MASK_STRATEGIES

  // Rules the agent cannot run. One of them empties the agent's entity list.
  const agentBlocked = useMemo(() => rules.filter((r) => maskingAgentBlockers(r).length > 0), [rules])
  const agentBound = connectionIds.length > 0 || attributes.length > 0

  const canSubmit = name.trim() !== '' && !submitting

  const save = async () => {
    if (!canSubmit) return false
    const spec = rulesToSpec(rules, sshBound)
    if (spec.rules.length === 0) {
      showSnackbar({ level: 'error', text: 'Give every rule a name, or remove it.' })
      return false
    }
    const half = customEntities.findIndex((c) => (c.name.trim() === '') !== (c.regex.trim() === ''))
    if (half !== -1) {
      showSnackbar({ level: 'error', text: `Custom entity ${half + 1} needs a name and a regex, or remove it.` })
      return false
    }
    const custom = customEntities.filter((c) => c.name.trim() !== '' && c.regex.trim() !== '')
    if (custom.length > 0 && agentBlocked.length > 0) {
      const n = rules.indexOf(agentBlocked[0]) + 1
      showSnackbar({
        level: 'error',
        text: `Custom entity types run on agents only, and rule ${n} matches by column, so agents would run nothing from this rule. Remove one or the other.`,
      })
      return false
    }
    const named = rules.filter((r) => r.name.trim() !== '')
    const payload = {
      name: name.trim(),
      description,
      connection_ids: connectionIds,
      attributes,
      supported_entity_types:
        agentBlocked.length > 0
          ? []
          : named.map((r) => ({ name: r.name.trim().toUpperCase(), entity_types: r.entities })),
      custom_entity_types: custom.map((c) => ({
        name: normalizeEntityName(c.name),
        regex: c.regex.trim(),
        score: 0.8,
      })),
      sidecar_spec: spec,
      sidecar_targets: targets,
    }
    if (scoreThreshold !== '') payload.score_threshold = Number(scoreThreshold) / 100
    const { ok, error } = isEdit ? await updateRule(id, payload) : await createRule(payload)
    if (ok) {
      showSnackbar({ level: 'success', text: isEdit ? 'Rule updated.' : 'Rule created.' })
      onSaved?.()
      return true
    }
    showSnackbar({ level: 'error', text: error?.response?.data?.message || 'Failed to save the rule.' })
    return false
  }

  const remove = async () => {
    const { ok, error } = await deleteRule(id)
    if (ok) {
      showSnackbar({ level: 'success', text: 'Rule deleted.' })
      onDeleted?.()
      return true
    }
    showSnackbar({ level: 'error', text: error?.response?.data?.message || 'Failed to delete the rule.' })
    return false
  }

  return {
    isEdit,
    name,
    setName,
    description,
    setDescription,
    connectionIds,
    setConnectionIds,
    attributes,
    setAttributes,
    targets,
    setTargets,
    rules,
    setRules,
    customEntities,
    setCustomEntities,
    scoreThreshold,
    setScoreThreshold,
    strategies,
    sshBound,
    agentBlocked,
    agentBound,
    canSubmit,
    submitting,
    save,
    remove,
  }
}

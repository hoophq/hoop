import { useMemo, useState } from 'react'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { showSnackbar } from '@/utils/snackbar'
import { DEFAULT_KEEP_LAST, MASK_STRATEGIES, SSH_ONLY_STRATEGY } from '@/pages/sidecarRuleVocabulary'
import { useDataMaskingStore } from '../store'

export const emptyRule = () => ({
  key: Math.random().toString(36).slice(2),
  name: '',
  match: 'entities',
  entities: [],
  columns: [],
  strategy: 'redact',
  keep_last: DEFAULT_KEEP_LAST,
  mask_char: '',
})

function specToRules(spec) {
  const rules = spec?.rules ?? []
  if (rules.length === 0) return [emptyRule()]
  return rules.map((r) => ({
    ...emptyRule(),
    ...r,
    match: (r.columns ?? []).length > 0 ? 'columns' : 'entities',
    strategy: r.strategy || 'redact',
    keep_last: r.keep_last ?? DEFAULT_KEEP_LAST,
    mask_char: r.mask_char ? String.fromCodePoint(r.mask_char) : '',
    key: Math.random().toString(36).slice(2),
  }))
}

function rulesToSpec(rules, sshBound) {
  const out = rules
    .filter((r) => r.name.trim() !== '')
    .map((r) => {
      // Clamped here rather than held in state. An ssh lane accepts one
      // strategy, and which lanes are bound changes as the picker changes, so
      // deriving it at save time keeps the two from drifting apart.
      const strategy = sshBound ? SSH_ONLY_STRATEGY : r.strategy
      const rule = { name: r.name.trim(), strategy }
      if (r.match === 'columns') rule.columns = r.columns
      else rule.entities = r.entities
      // Only where the strategy reads it. A keep_last on a hash rule is a
      // field the daemon declares but never looks at, and a form that writes
      // it teaches the reader the wrong thing.
      if (strategy === 'partial') rule.keep_last = Number(r.keep_last)
      if ((strategy === 'mask' || strategy === 'partial') && r.mask_char !== '') {
        // The daemon takes a Go rune, which is the code point as a number.
        rule.mask_char = r.mask_char.codePointAt(0)
      }
      return rule
    })
  return { rules: out }
}

/**
 * The state and the save of one masking rule in the sidecar's vocabulary,
 * shared by the page and by the dialog a sidecar's feature accordion opens.
 *
 * `targets` seeds the listeners of a NEW rule; a stored rule keeps its own.
 * `onSaved` and `onDeleted` run once the write went through.
 */
export function useSidecarDataMaskingEditor({ rule: stored, id, isEdit, targets: seed = [], onSaved, onDeleted }) {
  const submitting = useDataMaskingStore((s) => s.submitting)
  const createRule = useDataMaskingStore((s) => s.createRule)
  const updateRule = useDataMaskingStore((s) => s.updateRule)
  const deleteRule = useDataMaskingStore((s) => s.deleteRule)
  const sidecars = useSidecarStore((s) => s.sidecars)

  const [name, setName] = useState(stored?.name ?? '')
  const [description, setDescription] = useState(stored?.description ?? '')
  const [targets, setTargets] = useState(stored?.sidecar_targets ?? seed)
  const [rules, setRules] = useState(() => specToRules(stored?.sidecar_spec))

  // An ssh lane masks a byte stream IN PLACE: a replacement of a different
  // size shifts every byte after it, which desynchronizes a terminal and
  // corrupts a download. Length preservation is the safety property, so the
  // strategy list collapses to the one that has it.
  const sshBound = useMemo(() => {
    const byId = new Map(sidecars.map((sc) => [sc.id, sc]))
    return targets.some((t) => {
      const lane = byId.get(t.sidecar_id)?.configuration?.listeners?.find((l) => l.name === t.listener_name)
      return (lane?.protocol ?? '').toLowerCase() === 'ssh'
    })
  }, [targets, sidecars])

  const strategies = sshBound ? MASK_STRATEGIES.filter((s) => s.value === SSH_ONLY_STRATEGY) : MASK_STRATEGIES

  const canSubmit = name.trim() !== '' && !submitting

  const save = async () => {
    if (!canSubmit) return false
    const spec = rulesToSpec(rules, sshBound)
    if (spec.rules.length === 0) {
      showSnackbar({ level: 'error', text: 'Give every rule a name, or remove it.' })
      return false
    }
    const payload = {
      name: name.trim(),
      description,
      // The gateway's own fields stay empty: this rule reaches a sidecar by
      // naming its listeners, not a connection, and a sidecar has no custom
      // recognizer to register.
      connection_ids: [],
      attributes: [],
      supported_entity_types: [],
      custom_entity_types: [],
      sidecar_spec: spec,
      sidecar_targets: targets,
    }
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
    targets,
    setTargets,
    rules,
    setRules,
    strategies,
    sshBound,
    canSubmit,
    submitting,
    save,
    remove,
  }
}

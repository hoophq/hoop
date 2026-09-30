import { useMemo, useState } from 'react'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { showSnackbar } from '@/utils/snackbar'
import { operationsFor, ruleTypesFor } from '@/pages/sidecarRuleVocabulary'
import { useGuardrailsStore } from '../store'

export const emptyRule = () => ({
  key: Math.random().toString(36).slice(2),
  name: '',
  type: 'operation',
  message: '',
  action: '',
  operations: [],
  tables: [],
  access: '',
  require_table_match: false,
  words: [],
  pattern_regex: '',
  entities: [],
  resources: [],
  methods: [],
  statuses: [],
})

function specToRules(spec) {
  const rules = spec?.rules ?? []
  if (rules.length === 0) return [emptyRule()]
  return rules.map((r) => ({ ...emptyRule(), ...r, key: Math.random().toString(36).slice(2) }))
}

// Only the fields the chosen type reads reach the payload. A sidecar decodes
// its configuration strictly, and the gateway refuses a rule carrying a field
// its type does not declare — so dropping them here is what lets one form row
// hold every type.
function rulesToSpec(rules, typeFields) {
  const out = rules
    .filter((r) => r.name.trim() !== '')
    .map((r) => {
      const rule = { name: r.name.trim(), type: r.type }
      for (const field of typeFields(r.type)) {
        const v = r[field]
        if (Array.isArray(v) ? v.length > 0 : v !== '' && v !== false) rule[field] = v
      }
      // operations scopes any rule type, not just an operation rule.
      if (r.type !== 'operation' && r.operations.length > 0) rule.operations = r.operations
      if (r.message.trim() !== '') rule.message = r.message.trim()
      if (r.action !== '') rule.action = r.action
      return rule
    })
  return { rules: out }
}

/**
 * The state and the save of one guardrail in the sidecar's vocabulary, shared
 * by the page and by the dialog a sidecar's feature accordion opens.
 *
 * `targets` seeds the listeners of a NEW rule; a stored rule keeps its own.
 * `onSaved` and `onDeleted` run once the write went through.
 */
export function useSidecarGuardrailEditor({ guardrail, id, isEdit, targets: seed = [], onSaved, onDeleted }) {
  const submitting = useGuardrailsStore((s) => s.submitting)
  const createGuardrail = useGuardrailsStore((s) => s.createGuardrail)
  const updateGuardrail = useGuardrailsStore((s) => s.updateGuardrail)
  const deleteGuardrail = useGuardrailsStore((s) => s.deleteGuardrail)
  const sidecars = useSidecarStore((s) => s.sidecars)

  const [name, setName] = useState(guardrail?.name ?? '')
  const [description, setDescription] = useState(guardrail?.description ?? '')
  const [targets, setTargets] = useState(guardrail?.sidecar_targets ?? seed)
  const [rules, setRules] = useState(() => specToRules(guardrail?.sidecar_spec))

  // Which protocols the bound listeners speak. A sidecar REFUSES a rule its
  // lane cannot read — at startup, taking that sidecar's whole configuration
  // with it — so the type list narrows to what every bound lane accepts
  // instead of letting the save fail later.
  const protocols = useMemo(() => {
    const byId = new Map(sidecars.map((sc) => [sc.id, sc]))
    return targets.map((t) => {
      const lane = byId.get(t.sidecar_id)?.configuration?.listeners?.find((l) => l.name === t.listener_name)
      return lane?.protocol ?? ''
    })
  }, [targets, sidecars])

  const types = useMemo(() => ruleTypesFor(protocols), [protocols])
  const operations = useMemo(() => operationsFor(protocols[0]), [protocols])
  const typeFields = (t) => types.find((x) => x.value === t)?.fields ?? []

  const canSubmit = name.trim() !== '' && !submitting

  const save = async () => {
    if (!canSubmit) return false
    const spec = rulesToSpec(rules, typeFields)
    if (spec.rules.length === 0) {
      showSnackbar({ level: 'error', text: 'Give every rule a name, or remove it.' })
      return false
    }
    const payload = {
      id: isEdit ? id : '',
      name: name.trim(),
      description,
      // The gateway's own fields stay empty: a control plane has no
      // connections and no attributes to bind a rule to.
      connection_ids: [],
      attributes: [],
      input: { rules: [] },
      output: { rules: [] },
      sidecar_spec: spec,
      sidecar_targets: targets,
    }
    const { ok, error } = isEdit ? await updateGuardrail(id, payload) : await createGuardrail(payload)
    if (ok) {
      showSnackbar({ level: 'success', text: isEdit ? 'Guardrail updated.' : 'Guardrail created.' })
      onSaved?.()
      return true
    }
    showSnackbar({ level: 'error', text: error?.response?.data?.message || 'Failed to save the guardrail.' })
    return false
  }

  const remove = async () => {
    const { ok, error } = await deleteGuardrail(id)
    if (ok) {
      showSnackbar({ level: 'success', text: 'Guardrail deleted.' })
      onDeleted?.()
      return true
    }
    showSnackbar({ level: 'error', text: error?.response?.data?.message || 'Failed to delete the guardrail.' })
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
    types,
    operations,
    canSubmit,
    submitting,
    save,
    remove,
  }
}

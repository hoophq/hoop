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

// The gateway refuses a field the rule type does not declare.
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

// Shared by the rule page and the sidecar dialog. `targets` seeds a new rule's listeners.
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

  // A sidecar refuses at startup a rule type its lane cannot read, so the
  // type list narrows to what every bound lane accepts.
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
    // A type the list no longer offers stayed on a row from before the targets changed.
    const unsupported = rules.findIndex((r) => r.name.trim() !== '' && !types.some((t) => t.value === r.type))
    if (unsupported !== -1) {
      showSnackbar({ level: 'error', text: `Rule ${unsupported + 1} has a type the selected listeners cannot run.` })
      return false
    }
    const spec = rulesToSpec(rules, typeFields)
    if (spec.rules.length === 0) {
      showSnackbar({ level: 'error', text: 'Give every rule a name, or remove it.' })
      return false
    }
    const payload = {
      id: isEdit ? id : '',
      name: name.trim(),
      description,
      // Gateway-only fields.
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

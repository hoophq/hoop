import { useMemo, useState } from 'react'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { showSnackbar } from '@/utils/snackbar'
import { AGENT_GUARDRAIL_TYPES, guardrailAgentBlockers } from '@/utils/ruleSupport'
import { operationsFor, ruleTypesFor } from '@/pages/sidecarRuleVocabulary'
import { useGuardrailsStore } from '../store'

export const emptyRule = () => ({
  key: Math.random().toString(36).slice(2),
  name: '',
  type: 'operation',
  direction: 'input',
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

const withKey = (rule) => ({ ...emptyRule(), ...rule, key: Math.random().toString(36).slice(2) })

// The spec is the UI's truth for statement rules; the agent `input` half is
// read only when there is no spec, since save rewrites it from the spec. An
// agent rule has no name, so it takes the guardrail's (numbered when several).
function loadRules(guardrail) {
  const spec = guardrail?.sidecar_spec?.rules
  const input = guardrail?.input?.rules ?? []
  const output = guardrail?.output?.rules ?? []
  const base = guardrail?.name ?? ''
  const nameOf = (list, i) => (list.length > 1 ? `${base}/${i + 1}` : base)
  const rules = [
    ...(spec
      ? spec.map((r) => withKey({ ...r, direction: 'input' }))
      : input.map((r, i) => withKey({ ...r, name: nameOf(input, i), direction: 'input' }))),
    ...output.map((r, i) => withKey({ ...r, name: nameOf(output, i), direction: 'output' })),
  ]
  return rules.length > 0 ? rules : [emptyRule()]
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

const agentRule = (r) => ({
  type: r.type,
  words: r.type === 'deny_words_list' ? r.words : [],
  pattern_regex: r.type === 'pattern_match' ? r.pattern_regex : '',
  message: r.message.trim(),
})

// Shared by the rule page and the sidecar dialog. `targets` seeds a new rule's listeners.
export function useGuardrailEditor({ guardrail, id, isEdit, targets: seed = [], onSaved, onDeleted }) {
  const submitting = useGuardrailsStore((s) => s.submitting)
  const createGuardrail = useGuardrailsStore((s) => s.createGuardrail)
  const updateGuardrail = useGuardrailsStore((s) => s.updateGuardrail)
  const deleteGuardrail = useGuardrailsStore((s) => s.deleteGuardrail)
  const sidecars = useSidecarStore((s) => s.sidecars)

  const [name, setName] = useState(guardrail?.name ?? '')
  const [description, setDescription] = useState(guardrail?.description ?? '')
  const [connectionIds, setConnectionIds] = useState(guardrail?.connection_ids ?? [])
  const [attributes, setAttributes] = useState(guardrail?.attributes ?? [])
  const [targets, setTargets] = useState(guardrail?.sidecar_targets ?? seed)
  const [rules, setRules] = useState(() => loadRules(guardrail))

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

  // Statement rules the agent cannot run. One of them empties the agent's input half.
  const agentBlocked = useMemo(
    () => rules.filter((r) => r.direction === 'input' && guardrailAgentBlockers(r).length > 0),
    [rules],
  )
  const agentBound = connectionIds.length > 0 || attributes.length > 0

  const canSubmit = name.trim() !== '' && !submitting

  const save = async () => {
    if (!canSubmit) return false
    // A type the list no longer offers stayed on a row from before the targets changed.
    const unsupported = rules.findIndex((r) => r.name.trim() !== '' && !types.some((t) => t.value === r.type))
    if (unsupported !== -1) {
      showSnackbar({ level: 'error', text: `Rule ${unsupported + 1} has a type the selected listeners cannot run.` })
      return false
    }
    const named = rules.filter((r) => r.name.trim() !== '')
    if (named.length === 0) {
      showSnackbar({ level: 'error', text: 'Give every rule a name, or remove it.' })
      return false
    }
    const badOutput = rules.findIndex((r) => r.direction === 'output' && !AGENT_GUARDRAIL_TYPES.includes(r.type))
    if (badOutput !== -1) {
      showSnackbar({
        level: 'error',
        text: `Rule ${badOutput + 1} checks results, which only deny words and pattern rules can do.`,
      })
      return false
    }
    const outputRules = named.filter((r) => r.direction === 'output')
    if (outputRules.length > 0 && agentBlocked.length > 0) {
      const n = rules.indexOf(agentBlocked[0]) + 1
      showSnackbar({
        level: 'error',
        text: `Result rules run on agents only, and rule ${n} has a sidecar-only option, so agents would run nothing from this rule. Remove one or the other.`,
      })
      return false
    }
    const inputRules = named.filter((r) => r.direction === 'input')
    const spec = rulesToSpec(inputRules, typeFields)
    const payload = {
      id: isEdit ? id : '',
      name: name.trim(),
      description,
      connection_ids: connectionIds,
      attributes,
      input: { rules: agentBlocked.length > 0 ? [] : inputRules.map(agentRule) },
      output: { rules: outputRules.map(agentRule) },
      sidecar_spec: spec.rules.length > 0 ? spec : null,
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
    connectionIds,
    setConnectionIds,
    attributes,
    setAttributes,
    targets,
    setTargets,
    rules,
    setRules,
    types,
    operations,
    agentBlocked,
    agentBound,
    canSubmit,
    submitting,
    save,
    remove,
  }
}

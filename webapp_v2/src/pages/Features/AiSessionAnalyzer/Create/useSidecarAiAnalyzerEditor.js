import { useEffect, useMemo, useState } from 'react'
import { usersService } from '@/services/users'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { showSnackbar } from '@/utils/snackbar'
import { analyzerActionsFor, operationsFor, REVIEW_ACTION } from '@/pages/sidecarRuleVocabulary'
import { useAiSessionAnalyzerStore } from '../store'

const EMPTY = {
  trigger_operations: [],
  trigger_tables: [],
  trigger_resources: [],
  high: 'block',
  medium: '',
  low: '',
  prompt: '',
  message: '',
  max_calls: '',
  approval_rule: '',
  review_mode: '',
}

function specToForm(spec) {
  if (!spec) return { ...EMPTY }
  return {
    trigger_operations: spec.trigger?.operations ?? [],
    trigger_tables: spec.trigger?.tables ?? [],
    trigger_resources: spec.trigger?.resources ?? [],
    high: spec.high ?? '',
    medium: spec.medium ?? '',
    low: spec.low ?? '',
    prompt: spec.prompt ?? '',
    message: spec.message ?? '',
    max_calls: spec.max_calls ?? '',
    approval_rule: spec.approval_rule ?? '',
    review_mode: spec.review_mode ?? '',
  }
}

function formToSpec(f, ruleName) {
  const spec = {}
  const trigger = {}
  if (f.trigger_operations.length > 0) trigger.operations = f.trigger_operations
  if (f.trigger_tables.length > 0) trigger.tables = f.trigger_tables
  if (f.trigger_resources.length > 0) trigger.resources = f.trigger_resources
  if (Object.keys(trigger).length > 0) spec.trigger = trigger
  for (const level of ['high', 'medium', 'low']) {
    if (f[level] !== '') spec[level] = f[level]
  }
  if (f.prompt.trim() !== '') spec.prompt = f.prompt.trim()
  if (f.message.trim() !== '') spec.message = f.message.trim()
  if (f.max_calls !== '' && f.max_calls !== null) spec.max_calls = Number(f.max_calls)
  // The rule that says who may release a held statement, named after this one:
  // the control plane owns both halves and keeps them in step, so there is no
  // second name for an operator to get wrong. The sidecar refuses a hold that
  // names nothing, and the plane refuses a review whose rule it cannot find.
  // A stored approval rule that names another rule is an admin's choice of
  // reviewers (an imported file keeps its own), so it stays.
  if ([spec.high, spec.medium, spec.low].includes(REVIEW_ACTION)) {
    spec.approval_rule = f.approval_rule && f.approval_rule !== ruleName ? f.approval_rule : ruleName
    // No field edits it, so keep what an import or the API stored. Only on a
    // lane that holds: the sidecar refuses a mode nothing reads.
    if (f.review_mode !== '') spec.review_mode = f.review_mode
  }
  return spec
}

/**
 * The state and the save of one analyzer rule in the sidecar's vocabulary,
 * shared by the page and by the dialog a sidecar's feature accordion opens.
 *
 * `targets` seeds the listeners of a NEW rule; a stored rule keeps its own.
 * `onSaved` and `onDeleted` run once the write went through.
 */
export function useSidecarAiAnalyzerEditor({ rule: stored, ruleName, isEdit, targets: seed = [], onSaved, onDeleted }) {
  const submitting = useAiSessionAnalyzerStore((s) => s.submitting)
  const createRule = useAiSessionAnalyzerStore((s) => s.createRule)
  const updateRule = useAiSessionAnalyzerStore((s) => s.updateRule)
  const deleteRule = useAiSessionAnalyzerStore((s) => s.deleteRule)
  const sidecars = useSidecarStore((s) => s.sidecars)

  const [name, setName] = useState(stored?.name ?? '')
  const [description, setDescription] = useState(stored?.description ?? '')
  const [targets, setTargets] = useState(stored?.sidecar_targets ?? seed)
  const [form, setForm] = useState(() => specToForm(stored?.sidecar_spec))
  const set = (patch) => setForm((f) => ({ ...f, ...patch }))
  // Who may release what this rule holds: hoop groups, which each login syncs
  // from the identity provider. Empty leaves it to the administrators.
  const [reviewers, setReviewers] = useState(stored?.reviewers_groups ?? [])
  const [groupOptions, setGroupOptions] = useState([])

  useEffect(() => {
    let cancelled = false
    usersService
      .listGroups()
      .then(({ data }) => {
        if (!cancelled) setGroupOptions(Array.isArray(data) ? data : [])
      })
      // Without the list the field still shows the groups already stored.
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [])

  const protocol = useMemo(() => {
    const byId = new Map(sidecars.map((sc) => [sc.id, sc]))
    const first = targets[0]
    if (!first) return ''
    const lane = byId.get(first.sidecar_id)?.configuration?.listeners?.find((l) => l.name === first.listener_name)
    return lane?.protocol ?? ''
  }, [targets, sidecars])

  const isHTTP = protocol.toLowerCase() === 'http'
  const operations = useMemo(() => operationsFor(protocol), [protocol])
  const actions = useMemo(() => analyzerActionsFor(), [])
  const noTrigger =
    form.trigger_operations.length === 0 && form.trigger_tables.length === 0 && form.trigger_resources.length === 0

  const canSubmit = name.trim() !== '' && !submitting
  // Every group of the org, plus the stored ones, so a group nobody holds
  // any more still shows and can be removed.
  const reviewerOptions = useMemo(
    () => [...new Set([...groupOptions, ...reviewers])].sort(),
    [groupOptions, reviewers],
  )
  // Reviewers belong to the approval rule this form keeps beside the analyzer
  // rule. A hold that names another rule (an imported file's) keeps that
  // rule's reviewers, so the field is not shown for it.
  const ownHold =
    [form.high, form.medium, form.low].includes(REVIEW_ACTION) &&
    (!form.approval_rule || form.approval_rule === name.trim())

  const save = async () => {
    if (!canSubmit) return false
    const spec = formToSpec(form, name.trim())
    if (!spec.high && !spec.medium && !spec.low) {
      showSnackbar({ level: 'error', text: 'Set an action for at least one risk level.' })
      return false
    }
    const payload = {
      name: name.trim(),
      description: description || null,
      // The gateway's own fields stay empty: a control plane has no
      // connections, and the sidecar's risk vocabulary lives in sidecar_spec.
      connection_names: [],
      risk_evaluation: {
        low_risk_action: 'allow_execution',
        medium_risk_action: 'allow_execution',
        high_risk_action: 'allow_execution',
      },
      agentic: false,
      sidecar_spec: spec,
      sidecar_targets: targets,
      reviewers_groups: ownHold ? reviewers : undefined,
    }
    const { ok, error } = isEdit ? await updateRule(ruleName, payload) : await createRule(payload)
    if (ok) {
      showSnackbar({ level: 'success', text: isEdit ? 'Rule updated.' : 'Rule created.' })
      onSaved?.()
      return true
    }
    showSnackbar({ level: 'error', text: error?.response?.data?.message || 'Failed to save the rule.' })
    return false
  }

  // The gateway deletes the approval rule this one keeps beside it, so the
  // reviewers of a hold go with the rule.
  const remove = async () => {
    const { ok, error } = await deleteRule(ruleName)
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
    form,
    set,
    reviewers,
    setReviewers,
    reviewerOptions,
    ownHold,
    isHTTP,
    operations,
    actions,
    noTrigger,
    canSubmit,
    submitting,
    save,
    remove,
  }
}

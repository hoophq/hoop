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
  // Items: every field one names must match (ADR-0030).
  trigger_any: [],
  trigger_exclude: [],
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
    trigger_any: spec.trigger?.any ?? [],
    trigger_exclude: spec.trigger?.exclude ?? [],
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

// An item keeps only the fields it names, or is null when it names none.
function compactItem(item) {
  const out = {}
  for (const key of ['operations', 'tables', 'resources']) {
    if (item[key]?.length > 0) out[key] = item[key]
  }
  return Object.keys(out).length > 0 ? out : null
}

function formToSpec(f, ruleName) {
  const spec = {}
  const trigger = {}
  if (f.trigger_operations.length > 0) trigger.operations = f.trigger_operations
  if (f.trigger_tables.length > 0) trigger.tables = f.trigger_tables
  if (f.trigger_resources.length > 0) trigger.resources = f.trigger_resources
  // The sidecar refuses an item that names no field, so an empty row is dropped.
  const any = f.trigger_any.map(compactItem).filter(Boolean)
  const exclude = f.trigger_exclude.map(compactItem).filter(Boolean)
  if (any.length > 0) trigger.any = any
  if (exclude.length > 0) trigger.exclude = exclude
  if (Object.keys(trigger).length > 0) spec.trigger = trigger
  for (const level of ['high', 'medium', 'low']) {
    if (f[level] !== '') spec[level] = f[level]
  }
  if (f.prompt.trim() !== '') spec.prompt = f.prompt.trim()
  if (f.message.trim() !== '') spec.message = f.message.trim()
  if (f.max_calls !== '' && f.max_calls !== null) spec.max_calls = Number(f.max_calls)
  // A hold needs an approval rule; the plane keeps one named after this rule.
  // A stored one naming another rule (an imported file's) is kept.
  if ([spec.high, spec.medium, spec.low].includes(REVIEW_ACTION)) {
    spec.approval_rule = f.approval_rule && f.approval_rule !== ruleName ? f.approval_rule : ruleName
    // Only on a lane that holds: the sidecar refuses a mode nothing reads.
    if (f.review_mode !== '') spec.review_mode = f.review_mode
  }
  return spec
}

// The API stores seconds; the form shows minutes. An emptied field sends 0,
// which clears the stored limit.
const secToMinutes = (s) => (s ? s / 60 : '')
const minutesToSec = (m) => (m === '' || m == null ? 0 : Math.round(Number(m) * 60))

// Shared by the rule page and the sidecar dialog. `targets` seeds a new rule's listeners.
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
  // Hoop groups; empty leaves the hold to the administrators.
  const [reviewers, setReviewers] = useState(stored?.reviewers_groups ?? [])
  // A limit is sent only after an edit, so a save that does not touch it keeps
  // the stored value.
  const [pendingMinutes, setPendingMinutes] = useState(secToMinutes(stored?.pending_ttl_sec))
  const [approvalMinutes, setApprovalMinutes] = useState(secToMinutes(stored?.approval_ttl_sec))
  const [pendingTouched, setPendingTouched] = useState(false)
  const [approvalTouched, setApprovalTouched] = useState(false)
  const editPendingMinutes = (v) => {
    setPendingMinutes(v)
    setPendingTouched(true)
  }
  const editApprovalMinutes = (v) => {
    setApprovalMinutes(v)
    setApprovalTouched(true)
  }
  const [groupOptions, setGroupOptions] = useState([])

  useEffect(() => {
    let cancelled = false
    usersService
      .listGroups()
      .then(({ data }) => {
        if (!cancelled) setGroupOptions(Array.isArray(data) ? data : [])
      })
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
  // Exclude alone selects nothing, so it does not count as a trigger.
  const noTrigger =
    form.trigger_operations.length === 0 &&
    form.trigger_tables.length === 0 &&
    form.trigger_resources.length === 0 &&
    form.trigger_any.length === 0

  const canSubmit = name.trim() !== '' && !submitting
  // Plus the stored ones, so a removed group can still be unselected.
  const reviewerOptions = useMemo(
    () => [...new Set([...groupOptions, ...reviewers])].sort(),
    [groupOptions, reviewers],
  )
  const holds = [form.high, form.medium, form.low].includes(REVIEW_ACTION)
  // Reviewers belong to this rule's own approval rule, not an imported one.
  const ownHold = holds && (!form.approval_rule || form.approval_rule === name.trim())

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
      // Gateway-only fields.
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
      pending_ttl_sec: ownHold && pendingTouched ? minutesToSec(pendingMinutes) : undefined,
      approval_ttl_sec: ownHold && approvalTouched ? minutesToSec(approvalMinutes) : undefined,
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
    pendingMinutes,
    editPendingMinutes,
    approvalMinutes,
    editApprovalMinutes,
    holds,
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

import { useEffect, useMemo, useState } from 'react'
import { usersService } from '@/services/users'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { showSnackbar } from '@/utils/snackbar'
import {
  APPROVER_AI,
  APPROVER_HUMAN,
  REVIEW_ACTION,
  analyzerActionsFor,
  isHoldAction,
  operationsFor,
  readRiskLevel,
} from '@/pages/sidecarRuleVocabulary'
import { useAiSessionAnalyzerStore } from '../store'

const LEVEL_KEYS = ['high', 'medium', 'low']

// A bare hold loaded from a spec whose approval_rule names another rule (an
// imported file's): that rule decides, and this form cannot edit it.
export const APPROVER_RULE = 'approval_rule'

// Who approves one level's hold. A new hold uses the AI agent.
const emptyApproval = () => ({ approver: APPROVER_AI, groups: [], prompt: '' })
const emptyApprovals = () => Object.fromEntries(LEVEL_KEYS.map((level) => [level, emptyApproval()]))

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
  // No field edits it; kept so a save does not erase what the API set.
  match_ignore: [],
}

// `reviewers` are the groups of the rule's own approval rule. A bare hold
// from an older spec gets them, so it keeps its meaning as a human hold.
function specToForm(spec, ruleName, reviewers) {
  if (!spec) return { ...EMPTY, approval: emptyApprovals() }
  const imported = Boolean(spec.approval_rule) && spec.approval_rule !== ruleName
  const form = {
    trigger_operations: spec.trigger?.operations ?? [],
    trigger_tables: spec.trigger?.tables ?? [],
    trigger_resources: spec.trigger?.resources ?? [],
    trigger_any: spec.trigger?.any ?? [],
    trigger_exclude: spec.trigger?.exclude ?? [],
    prompt: spec.prompt ?? '',
    message: spec.message ?? '',
    max_calls: spec.max_calls ?? '',
    approval_rule: spec.approval_rule ?? '',
    review_mode: spec.review_mode ?? '',
    match_ignore: spec.match_ignore ?? [],
    approval: emptyApprovals(),
  }
  for (const level of LEVEL_KEYS) {
    const read = readRiskLevel(spec[level])
    const hold = isHoldAction(read.action)
    form[level] = hold ? REVIEW_ACTION : read.action
    if (!hold) continue
    if (read.approver) {
      form.approval[level] = { approver: read.approver, groups: read.groups, prompt: read.prompt }
    } else if (imported) {
      form.approval[level] = { ...emptyApproval(), approver: APPROVER_RULE }
    } else {
      form.approval[level] = { ...emptyApproval(), approver: APPROVER_HUMAN, groups: reviewers }
    }
  }
  return form
}

// The approvers of the levels that hold.
function holdApprovers(f) {
  return new Set(LEVEL_KEYS.filter((level) => isHoldAction(f[level])).map((level) => f.approval[level].approver))
}

// A hold level is an object naming its approver (ENG-680); a bare hold stays
// only for a level an imported approval rule decides.
function levelToSpec(action, approval) {
  if (!isHoldAction(action)) return action
  if (approval.approver === APPROVER_RULE) return REVIEW_ACTION
  if (approval.approver === APPROVER_HUMAN) {
    const level = { action: REVIEW_ACTION, approver: APPROVER_HUMAN }
    if (approval.groups.length > 0) level.approval_groups = approval.groups
    return level
  }
  const level = { action: REVIEW_ACTION, approver: APPROVER_AI }
  if (approval.prompt.trim() !== '') level.prompt = approval.prompt.trim()
  return level
}

// An item keeps only the fields it names, or is null when it names none.
function compactItem(item) {
  const out = {}
  for (const key of ['operations', 'tables', 'resources']) {
    if (item[key]?.length > 0) out[key] = item[key]
  }
  return Object.keys(out).length > 0 ? out : null
}

function formToSpec(f) {
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
  for (const level of LEVEL_KEYS) {
    if (f[level] !== '') spec[level] = levelToSpec(f[level], f.approval[level])
  }
  if (f.prompt.trim() !== '') spec.prompt = f.prompt.trim()
  if (f.message.trim() !== '') spec.message = f.message.trim()
  if (f.max_calls !== '' && f.max_calls !== null) spec.max_calls = Number(f.max_calls)
  const approvers = holdApprovers(f)
  // Only a bare hold reads the approval rule; a level object names its own approvers.
  if (approvers.has(APPROVER_RULE)) spec.approval_rule = f.approval_rule
  // Only a level that waits for a person: the sidecar refuses a mode nothing reads.
  if (approvers.has(APPROVER_RULE) || approvers.has(APPROVER_HUMAN)) {
    if (f.review_mode !== '') spec.review_mode = f.review_mode
    if (f.match_ignore.length > 0) spec.match_ignore = f.match_ignore
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
  // Groups of the rule's own approval rule: an older spec's bare hold reads them.
  const [form, setForm] = useState(() =>
    specToForm(stored?.sidecar_spec, stored?.name, stored?.reviewers_groups ?? []),
  )
  const set = (patch) => setForm((f) => ({ ...f, ...patch }))
  // A level that starts to hold gets the default approver, not a stale imported one.
  const setLevelAction = (level, action) =>
    setForm((f) => {
      const fresh = isHoldAction(action) && !isHoldAction(f[level])
      const keep = f.approval[level].approver !== APPROVER_RULE
      const approval = fresh && !keep ? { ...f.approval, [level]: emptyApproval() } : f.approval
      return { ...f, [level]: action, approval }
    })
  const setApproval = (level, patch) =>
    setForm((f) => ({ ...f, approval: { ...f.approval, [level]: { ...f.approval[level], ...patch } } }))
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
  // Plus the level's stored ones, so a removed group can still be unselected.
  const groupOptionsFor = (level) => [...new Set([...groupOptions, ...form.approval[level].groups])].sort()
  const approvers = holdApprovers(form)
  // A person decides: the hold waits, so its mode applies.
  const waits = approvers.has(APPROVER_HUMAN) || approvers.has(APPROVER_RULE)
  // Time limits belong to this rule's own reviews, not an imported rule's.
  const humanHold = approvers.has(APPROVER_HUMAN)

  const save = async () => {
    if (!canSubmit) return false
    const spec = formToSpec(form)
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
      // Level objects carry the approval groups now; the gateway keeps its rule from them.
      pending_ttl_sec: humanHold && pendingTouched ? minutesToSec(pendingMinutes) : undefined,
      approval_ttl_sec: humanHold && approvalTouched ? minutesToSec(approvalMinutes) : undefined,
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
    setLevelAction,
    setApproval,
    groupOptionsFor,
    pendingMinutes,
    editPendingMinutes,
    approvalMinutes,
    editApprovalMinutes,
    waits,
    humanHold,
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

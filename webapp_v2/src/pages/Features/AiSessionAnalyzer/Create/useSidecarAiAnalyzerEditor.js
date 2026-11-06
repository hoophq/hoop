import { useEffect, useMemo, useRef, useState } from 'react'
import { usersService } from '@/services/users'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { showSnackbar } from '@/utils/snackbar'
import {
  APPROVER_AI,
  APPROVER_HUMAN,
  HANDOFF_CHOICES,
  HANDOFF_HOLD,
  REVIEW_ACTION,
  analyzerActionsFor,
  isHandoffAction,
  isHoldAction,
  operationsFor,
  readLevelApprover,
  readLevelHandoff,
} from '@/pages/sidecarRuleVocabulary'
import { useAiSessionAnalyzerStore } from '../store'

const LEVEL_KEYS = ['high', 'medium', 'low']

// A level that can hold (a hold, or a hand-off that allows one) with no
// approvers entry, loaded from a spec whose approval_rule names another rule
// (an imported file's): that rule decides, and this form cannot edit it.
export const APPROVER_RULE = 'approval_rule'

// Who approves one level's hold. A new hold uses the AI agent. A hand-off
// level keeps here the approver of its "Hold for approval" choice.
const emptyApproval = () => ({ approver: APPROVER_AI, groups: [], prompt: '' })
const emptyApprovals = () => Object.fromEntries(LEVEL_KEYS.map((level) => [level, emptyApproval()]))

// A hand-off level's prompt and allowed actions (ENG-661). A new one allows all.
const emptyHandoff = () => ({ prompt: '', allowed: HANDOFF_CHOICES.map((c) => c.value) })
const emptyHandoffs = () => Object.fromEntries(LEVEL_KEYS.map((level) => [level, emptyHandoff()]))

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

// `reviewers` are the groups of the rule's own approval rule. A level that
// can hold with no approvers entry gets them, so it keeps its meaning as a
// human hold.
function specToForm(spec, ruleName, reviewers) {
  if (!spec) return { ...EMPTY, approval: emptyApprovals(), handoff: emptyHandoffs() }
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
    handoff: emptyHandoffs(),
  }
  for (const level of LEVEL_KEYS) {
    const action = spec[level] ?? ''
    form[level] = isHoldAction(action) ? REVIEW_ACTION : action
    if (isHandoffAction(action)) form.handoff[level] = readLevelHandoff(spec, level) ?? emptyHandoff()
    if (!canHold(form, level)) continue
    const read = readLevelApprover(spec, level)
    if (read) {
      form.approval[level] = read
    } else if (imported) {
      form.approval[level] = { ...emptyApproval(), approver: APPROVER_RULE }
    } else {
      form.approval[level] = { ...emptyApproval(), approver: APPROVER_HUMAN, groups: reviewers }
    }
  }
  return form
}

// A level can hold: its action is a hold, or a hand-off that allows one.
function canHold(f, level) {
  if (isHoldAction(f[level])) return true
  return isHandoffAction(f[level]) && f.handoff[level].allowed.includes(HANDOFF_HOLD)
}

// The approvers of the levels that can hold.
function holdApprovers(f) {
  return new Set(LEVEL_KEYS.filter((level) => canHold(f, level)).map((level) => f.approval[level].approver))
}

// approvers.<level> (ENG-680). A level an imported approval rule decides has
// no entry: it files under that rule.
function approverToSpec(approval) {
  if (approval.approver === APPROVER_HUMAN) {
    return approval.groups.length > 0 ? { type: APPROVER_HUMAN, groups: approval.groups } : { type: APPROVER_HUMAN }
  }
  if (approval.approver === APPROVER_AI) {
    const prompt = approval.prompt.trim()
    return prompt !== '' ? { type: APPROVER_AI, prompt } : { type: APPROVER_AI }
  }
  return null
}

// handoffs.<level> (ENG-661): the actions the agent may select, and its prompt.
function handoffToSpec(handoff) {
  const prompt = handoff.prompt.trim()
  return prompt !== '' ? { allowed_actions: [...handoff.allowed], prompt } : { allowed_actions: [...handoff.allowed] }
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
  const handoffs = {}
  const approvers = {}
  for (const level of LEVEL_KEYS) {
    if (f[level] === '') continue
    spec[level] = f[level]
    if (isHandoffAction(f[level])) handoffs[level] = handoffToSpec(f.handoff[level])
    const approver = canHold(f, level) ? approverToSpec(f.approval[level]) : null
    if (approver) approvers[level] = approver
  }
  if (Object.keys(handoffs).length > 0) spec.handoffs = handoffs
  if (Object.keys(approvers).length > 0) spec.approvers = approvers
  if (f.prompt.trim() !== '') spec.prompt = f.prompt.trim()
  if (f.message.trim() !== '') spec.message = f.message.trim()
  if (f.max_calls !== '' && f.max_calls !== null) spec.max_calls = Number(f.max_calls)
  const held = holdApprovers(f)
  // A hold needs an approval rule; the plane keeps one named after this rule.
  // A stored one naming another rule (an imported file's) is kept.
  if (held.size > 0) spec.approval_rule = held.has(APPROVER_RULE) ? f.approval_rule : ruleName
  // Only a level that waits for a person: the AI agent reads no mode.
  if (held.has(APPROVER_RULE) || held.has(APPROVER_HUMAN)) {
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
  // A level that starts to hold or hand off gets the default approver, not a
  // stale imported one. Between a hold and a hand-off the approver stays: both
  // read approvers.<level>, and with no entry both file under approval_rule.
  const setLevelAction = (level, action) =>
    setForm((f) => {
      const holds = (a) => isHoldAction(a) || isHandoffAction(a)
      const fresh = holds(action) && !holds(f[level])
      const keep = f.approval[level].approver !== APPROVER_RULE
      const approval = fresh && !keep ? { ...f.approval, [level]: emptyApproval() } : f.approval
      return { ...f, [level]: action, approval }
    })
  // Levels turned to a person before the groups loaded: they get every group
  // once the list arrives, unless the editor picks groups first.
  const groupsLoaded = useRef(false)
  const awaitingGroups = useRef(new Set())
  const setApproval = (level, patch) => {
    if ('groups' in patch) awaitingGroups.current.delete(level)
    setForm((f) => ({ ...f, approval: { ...f.approval, [level]: { ...f.approval[level], ...patch } } }))
  }
  const setHandoffPrompt = (level, prompt) =>
    setForm((f) => ({ ...f, handoff: { ...f.handoff, [level]: { ...f.handoff[level], prompt } } }))
  // The last allowed action cannot be removed: the sidecar refuses an empty list.
  const setAllowed = (level, value, checked) =>
    setForm((f) => {
      const current = f.handoff[level].allowed
      if (!checked && current.length === 1 && current[0] === value) return f
      const next = new Set(current)
      if (checked) next.add(value)
      else next.delete(value)
      const allowed = HANDOFF_CHOICES.map((c) => c.value).filter((v) => next.has(v))
      return { ...f, handoff: { ...f.handoff, [level]: { ...f.handoff[level], allowed } } }
    })
  // A level that turns to a person starts with every group selected; an
  // emptied list leaves the decision to the administrators. Picking the
  // approver a level already has changes nothing, so an administrators-only
  // level keeps its empty list.
  const setApprover = (level, approver) => {
    if (form.approval[level].approver === approver) return
    if (approver === APPROVER_HUMAN && !groupsLoaded.current) awaitingGroups.current.add(level)
    else awaitingGroups.current.delete(level)
    setForm((f) => {
      const current = f.approval[level]
      const groups = approver === APPROVER_HUMAN && current.groups.length === 0 ? [...groupOptions].sort() : current.groups
      return { ...f, approval: { ...f.approval, [level]: { ...current, approver, groups } } }
    })
  }
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
    // A level still waiting gets every group; one the editor changed since does not.
    const settle = (options) => {
      groupsLoaded.current = true
      const waiting = [...awaitingGroups.current]
      awaitingGroups.current = new Set()
      if (waiting.length === 0 || options.length === 0) return
      setForm((f) => {
        const approval = { ...f.approval }
        for (const level of waiting) {
          const current = approval[level]
          if (current.approver === APPROVER_HUMAN && current.groups.length === 0) {
            approval[level] = { ...current, groups: [...options].sort() }
          }
        }
        return { ...f, approval }
      })
    }
    usersService
      .listGroups()
      .then(({ data }) => {
        if (cancelled) return
        const options = Array.isArray(data) ? data : []
        setGroupOptions(options)
        settle(options)
      })
      .catch(() => {
        if (!cancelled) settle([])
      })
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
      // No reviewers_groups: the gateway derives the rule's own approval rule
      // groups from approvers, and an imported rule keeps its groups.
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
    setHandoffPrompt,
    setAllowed,
    setApprover,
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

import { useEffect, useMemo, useState } from 'react'
import { usersService } from '@/services/users'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { showSnackbar } from '@/utils/snackbar'
import {
  AI_RULE_EXISTING,
  AI_RULE_EXISTING_AND_NEW,
  AI_RULE_FEATURES,
  APPROVER_AI,
  APPROVER_HUMAN,
  HANDOFF_ACTION,
  HANDOFF_CHOICES,
  REVIEW_ACTION,
  analyzerActionsFor,
  appliesAIRules,
  isHandoffAction,
  isHoldAction,
  operationsFor,
  readRiskLevel,
} from '@/pages/sidecarRuleVocabulary'
import { useAiSessionAnalyzerStore } from '../store'

const LEVEL_KEYS = ['high', 'medium', 'low']

// A bare hold loaded from a spec whose approval_rule names another rule (an
// imported file's): that rule decides, and this form cannot edit it.
export const APPROVER_RULE = 'approval_rule'

// Who approves one level's hold. A new hold uses the AI agent. A hand-off
// level keeps here the approver of its "Hold for approval" choice.
const emptyApproval = () => ({ approver: APPROVER_AI, groups: [], prompt: '' })
const emptyApprovals = () => Object.fromEntries(LEVEL_KEYS.map((level) => [level, emptyApproval()]))

// A hand-off level's own prompt and its allowed actions. A new one allows all.
// `stored` is a level the form cannot read (both alias keys configure
// require_approval): it is shown read-only and saved as stored.
const emptyHandoff = () => ({ prompt: '', allowed: HANDOFF_CHOICES.map((c) => c.value), stored: null, storedApprovers: [] })
const emptyHandoffs = () => Object.fromEntries(LEVEL_KEYS.map((level) => [level, emptyHandoff()]))

// One level's AI rule options (ENG-662), per feature: off by default. `rules`
// null allows every library rule; `create` lets the AI create one-time rules.
const emptyAIFeature = () => ({ on: false, rules: null, create: false })
const emptyAIRules = () => Object.fromEntries(AI_RULE_FEATURES.map(({ key }) => [key, emptyAIFeature()]))
const emptyAIRuleLevels = () => Object.fromEntries(LEVEL_KEYS.map((level) => [level, emptyAIRules()]))

function readAIRules(read) {
  return Object.fromEntries(
    AI_RULE_FEATURES.map(({ key }) => {
      const opt = read.ai[key]
      if (!opt) return [key, emptyAIFeature()]
      return [key, { on: true, rules: opt.rules, create: opt.mode === AI_RULE_EXISTING_AND_NEW }]
    }),
  )
}

// A level uses AI rules when its action applies them and one feature is on.
const usesAIRules = (action, ai) => appliesAIRules(action) && AI_RULE_FEATURES.some(({ key }) => ai[key].on)

// The level fields of the features that are on. `rules` is written only for
// a selection that is not every rule: absent allows all.
function aiRulesToSpec(ai) {
  const out = {}
  for (const { key } of AI_RULE_FEATURES) {
    const f = ai[key]
    if (!f.on) continue
    out[key] = { mode: f.create ? AI_RULE_EXISTING_AND_NEW : AI_RULE_EXISTING }
    if (f.rules !== null) out[key].rules = [...f.rules]
  }
  return out
}

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
  if (!spec) return { ...EMPTY, approval: emptyApprovals(), handoff: emptyHandoffs(), ai: emptyAIRuleLevels() }
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
    ai: emptyAIRuleLevels(),
  }
  for (const level of LEVEL_KEYS) {
    const read = readRiskLevel(spec[level])
    form.ai[level] = readAIRules(read)
    if (isHandoffAction(read.action)) {
      form[level] = HANDOFF_ACTION
      form.handoff[level] = { ...emptyHandoff(), prompt: read.prompt, allowed: read.allowed.actions }
      form.approval[level] = read.allowed.approval
      if (read.allowed.conflict) {
        form.handoff[level].stored = spec[level]
        form.handoff[level].storedApprovers = read.allowed.approvals.map((a) => a.approver)
      }
      continue
    }
    const hold = isHoldAction(read.action)
    // An unset level allows; one that sets AI rules shows as Allow, so its
    // options stay in view and are saved.
    form[level] = hold ? REVIEW_ACTION : read.action || (usesAIRules('', form.ai[level]) ? 'allow' : '')
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

// A level can hold: its action is a hold, or a hand-off that allows one.
function canHold(f, level) {
  if (isHoldAction(f[level])) return true
  return isHandoffAction(f[level]) && f.handoff[level].allowed.includes(REVIEW_ACTION)
}

// The approvers of the levels that can hold. A stored level counts every
// approver it names.
function holdApprovers(f) {
  const out = new Set()
  for (const level of LEVEL_KEYS) {
    if (!canHold(f, level)) continue
    const stored = isHandoffAction(f[level]) && f.handoff[level].stored
    for (const a of stored ? f.handoff[level].storedApprovers : [f.approval[level].approver]) out.add(a)
  }
  return out
}

// The approver fields of a level object or of allowed_actions.require_approval.
function approverFields(approval) {
  if (approval.approver === APPROVER_HUMAN) {
    const out = { approver: APPROVER_HUMAN }
    if (approval.groups.length > 0) out.approval_groups = approval.groups
    return out
  }
  const out = { approver: APPROVER_AI }
  if (approval.prompt.trim() !== '') out.prompt = approval.prompt.trim()
  return out
}

// allowed_actions is a list, or an object when require_approval has a config
// that is not the default (AI agent with the default prompt).
function handoffToSpec(handoff, approval, ai) {
  if (handoff.stored) return handoff.stored
  const level = { action: HANDOFF_ACTION }
  if (handoff.prompt.trim() !== '') level.prompt = handoff.prompt.trim()
  const cfg = handoff.allowed.includes(REVIEW_ACTION) ? approverFields(approval) : null
  if (!cfg || (cfg.approver === APPROVER_AI && !cfg.prompt)) {
    level.allowed_actions = [...handoff.allowed]
  } else {
    level.allowed_actions = Object.fromEntries(handoff.allowed.map((a) => [a, a === REVIEW_ACTION ? cfg : {}]))
  }
  return { ...level, ...aiRulesToSpec(ai) }
}

// A hold level is an object naming its approver (ENG-680); a bare hold stays
// only for a level an imported approval rule decides, which takes no AI
// rules (an object would name the AI agent). Another level becomes an object
// when it sets AI rules (ENG-662). Block and defer never carry them.
function levelToSpec(action, approval, handoff, ai) {
  if (isHandoffAction(action)) return handoffToSpec(handoff, approval, ai)
  const rules = appliesAIRules(action) ? aiRulesToSpec(ai) : {}
  if (!isHoldAction(action)) return Object.keys(rules).length > 0 ? { action, ...rules } : action
  if (approval.approver === APPROVER_RULE) return REVIEW_ACTION
  return { action: REVIEW_ACTION, ...approverFields(approval), ...rules }
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
    if (f[level] !== '') spec[level] = levelToSpec(f[level], f.approval[level], f.handoff[level], f.ai[level])
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
  const ruleLibrary = useAiSessionAnalyzerStore((s) => s.ruleLibrary)
  const ruleLibraryStatus = useAiSessionAnalyzerStore((s) => s.ruleLibraryStatus)
  const fetchRuleLibrary = useAiSessionAnalyzerStore((s) => s.fetchRuleLibrary)

  const [name, setName] = useState(stored?.name ?? '')
  const [description, setDescription] = useState(stored?.description ?? '')
  const [targets, setTargets] = useState(stored?.sidecar_targets ?? seed)
  // Groups of the rule's own approval rule: an older spec's bare hold reads them.
  const [form, setForm] = useState(() =>
    specToForm(stored?.sidecar_spec, stored?.name, stored?.reviewers_groups ?? []),
  )
  const set = (patch) => setForm((f) => ({ ...f, ...patch }))
  // A level that starts to hold or hand off gets the default approver, not a
  // stale imported one: only a bare hold reads the imported rule. A level the
  // form could not read starts again from the defaults once its action changes.
  const setLevelAction = (level, action) =>
    setForm((f) => {
      if (action === f[level]) return f
      const imported = f.approval[level].approver === APPROVER_RULE
      const unread = Boolean(f.handoff[level].stored)
      const fresh = isHandoffAction(action) || (isHoldAction(action) && !isHoldAction(f[level]))
      const approval = (fresh && imported) || unread ? { ...f.approval, [level]: emptyApproval() } : f.approval
      const handoff = unread ? { ...f.handoff, [level]: emptyHandoff() } : f.handoff
      return { ...f, [level]: action, approval, handoff }
    })
  const setApproval = (level, patch) =>
    setForm((f) => ({ ...f, approval: { ...f.approval, [level]: { ...f.approval[level], ...patch } } }))
  const setHandoffPrompt = (level, prompt) =>
    setForm((f) => ({ ...f, handoff: { ...f.handoff, [level]: { ...f.handoff[level], prompt } } }))
  // The last allowed action cannot be removed: an empty list blocks.
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
  // emptied list leaves the decision to the administrators.
  const setApprover = (level, approver) =>
    setForm((f) => {
      const current = f.approval[level]
      const groups = approver === APPROVER_HUMAN && current.groups.length === 0 ? [...groupOptions].sort() : current.groups
      return { ...f, approval: { ...f.approval, [level]: { ...current, approver, groups } } }
    })
  // The rules a level's feature may name: the library's, plus the level's
  // stored names the library no longer holds, so they can be unselected.
  const aiRuleOptionsFor = (level, key) => {
    const library = ruleLibrary[key]
    const known = new Set(library.map((r) => r.name))
    const stale = (form.ai[level][key].rules ?? [])
      .filter((n) => !known.has(n))
      .map((name) => ({ name, description: 'Not in your rules list. Unselect it to save: the gateway refuses this name.' }))
    return [...library, ...stale]
  }
  const setAIFeature = (level, key, patch) =>
    setForm((f) => ({
      ...f,
      ai: { ...f.ai, [level]: { ...f.ai[level], [key]: { ...f.ai[level][key], ...patch } } },
    }))
  // A selection that is exactly the loaded, non-empty library is "all"
  // (null), which also covers the rules added later. Anything else, an empty
  // selection or one with stale names included, stays a list. "Select all"
  // is the explicit way to choose all.
  const toggleAIRule = (level, key, name) => {
    const options = aiRuleOptionsFor(level, key).map((r) => r.name)
    const current = form.ai[level][key].rules
    const next = new Set(current ?? options)
    if (next.has(name)) next.delete(name)
    else next.add(name)
    const library = ruleLibrary[key]
    const all =
      ruleLibraryStatus === 'success' &&
      library.length > 0 &&
      next.size === library.length &&
      library.every((r) => next.has(r.name))
    setAIFeature(level, key, { rules: all ? null : options.filter((n) => next.has(n)) })
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

  useEffect(() => {
    fetchRuleLibrary()
  }, [fetchRuleLibrary])

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
    setHandoffPrompt,
    setAllowed,
    setApprover,
    groupOptionsFor,
    ruleLibraryStatus,
    aiRuleOptionsFor,
    setAIFeature,
    toggleAIRule,
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

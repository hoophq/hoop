import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Box, Group, Stack, Text } from '@mantine/core'
import { ArrowLeft, Info, TriangleAlert } from 'lucide-react'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import DocsBtnCallOut from '@/components/DocsBtnCallOut'
import MultiSelect from '@/components/MultiSelect'
import NumberInput from '@/components/NumberInput'
import PageLoader from '@/components/PageLoader'
import SectionRow from '@/components/SectionRow'
import Select from '@/components/Select'
import SidecarTargetPicker from '@/components/SidecarTargetPicker'
import TagsInput from '@/components/TagsInput'
import Textarea from '@/components/Textarea'
import TextInput from '@/components/TextInput'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { docsUrl } from '@/utils/docsUrl'
import { showSnackbar } from '@/utils/snackbar'
import { analyzerActionsFor, canHold, operationsFor, REVIEW_ACTION } from '@/pages/sidecarRuleVocabulary'
import { useAiSessionAnalyzerStore } from '../store'

// The analyzer as a SIDECAR runs it: a per-listener BLOCK, not a rule, and a
// different vocabulary from the gateway's. Its actions are allow, warn, block
// and defer, where the gateway's are allow_execution, block_execution and
// require_access_request; it carries its own trigger and call budget, where
// the gateway's rule carries connection names.
//
// The sidecar's sibling of Create/GatewayAiAnalyzerForm. Neither imports the
// other; Router.jsx picks one with <ByProduct>.
//
// The provider, the model and the credential are NOT here. They are the
// sidecar's top-level analyzer section: a credentials_file is a path on the
// sidecar's filesystem, which a control plane cannot supply, and that section
// is restart-bound where everything on this page hot-swaps.

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
  //
  // Read off the levels rather than off a switch. The two halves still travel
  // together — that is the daemon's requirement and it has not moved — but
  // there is no longer a second control that can disagree with the first.
  if (holdsSomeLevel(spec)) spec.approval_rule = ruleName
  return spec
}

// Whether any level holds. One reader, so the form, the save and the payload
// cannot form three opinions about it.
const holdsSomeLevel = (spec) => [spec.high, spec.medium, spec.low].includes(REVIEW_ACTION)

function FormFields({ rule: stored, ruleName, isEdit }) {
  const navigate = useNavigate()
  const submitting = useAiSessionAnalyzerStore((s) => s.submitting)
  const createRule = useAiSessionAnalyzerStore((s) => s.createRule)
  const updateRule = useAiSessionAnalyzerStore((s) => s.updateRule)
  const sidecars = useSidecarStore((s) => s.sidecars)

  const [name, setName] = useState(stored?.name ?? '')
  const [description, setDescription] = useState(stored?.description ?? '')
  const [targets, setTargets] = useState(stored?.sidecar_targets ?? [])
  const [form, setForm] = useState(() => specToForm(stored?.sidecar_spec))
  const set = (patch) => setForm((f) => ({ ...f, ...patch }))

  const lanesByTarget = useMemo(() => {
    const byId = new Map(sidecars.map((sc) => [sc.id, sc]))
    return (ts) =>
      ts.map((t) =>
        byId.get(t.sidecar_id)?.configuration?.listeners?.find((l) => l.name === t.listener_name)
      )
  }, [sidecars])

  const protocol = useMemo(() => lanesByTarget(targets)[0]?.protocol ?? '', [targets, lanesByTarget])

  const isHTTP = protocol.toLowerCase() === 'http'
  const operations = useMemo(() => operationsFor(protocol), [protocol])
  // A hold needs a lane whose client sends the statement again. Refusing it
  // here is the same refusal the sidecar makes at startup, brought forward to
  // the form: bound to an http or ssh lane, this rule would take that
  // sidecar's whole configuration down on its next restart.
  const holdableFor = (ts) => ts.length > 0 && lanesByTarget(ts).every((lane) => canHold(lane?.protocol))
  const holdable = holdableFor(targets)
  const actions = useMemo(() => analyzerActionsFor(holdable), [holdable])

  /**
   * Change the bound listeners, and take the hold off any level that can no
   * longer hold it.
   *
   * This is the event that makes a chosen hold unreachable: a rule set on a
   * postgres lane and then pointed at an http one carries a require_review the
   * sidecar refuses at startup. It is cleared HERE, in the handler, rather
   * than at save time — the operator sees the levels change as they change the
   * listeners, instead of saving something other than what the form showed.
   *
   * The clean-up fails CLOSED: a level that was holding becomes block, never
   * allow. Dropping it to unset would quietly start allowing the statements
   * the operator chose to stop.
   */
  const handleTargets = (next) => {
    setTargets(next)
    // An EMPTY selection is not a selection that cannot hold. Clearing the
    // field — the picker is `clearable`, and removing the last pill does it
    // too — is how an operator starts re-picking, and rewriting their levels
    // mid-edit loses a hold they never chose to drop: re-selecting the same
    // listener does not bring it back, and the save then writes `block` with
    // nothing on screen saying so. holdableFor is false for [] because a rule
    // bound to nothing cannot hold anything, which is the right answer for the
    // option list and the wrong one here.
    if (next.length === 0 || holdableFor(next)) return
    setForm((f) => {
      const cleared = {}
      for (const level of ['high', 'medium', 'low']) {
        if (f[level] === REVIEW_ACTION) cleared[level] = 'block'
      }
      return Object.keys(cleared).length > 0 ? { ...f, ...cleared } : f
    })
  }

  const holding = [form.high, form.medium, form.low].includes(REVIEW_ACTION)
  const noTrigger =
    form.trigger_operations.length === 0 &&
    form.trigger_tables.length === 0 &&
    form.trigger_resources.length === 0

  const canSubmit = name.trim() !== '' && !submitting

  const handleSave = async () => {
    if (!canSubmit) return
    const spec = formToSpec(form, name.trim())
    if (!spec.high && !spec.medium && !spec.low) {
      showSnackbar({
        level: 'error',
        text: 'Set an action for at least one risk level.',
      })
      return
    }
    // The same refusal the daemon makes at startup, brought forward. Reachable
    // only by editing a stored rule whose listeners changed protocol under it:
    // the picker clears a hold the moment the targets stop supporting one.
    if (holdsSomeLevel(spec) && !holdable) {
      showSnackbar({
        level: 'error',
        text: 'Only a database listener can hold a statement.',
        description: 'Pick database listeners, or set those risk levels to something else.',
      })
      return
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
    }
    const { ok, error } = isEdit ? await updateRule(ruleName, payload) : await createRule(payload)
    if (ok) {
      showSnackbar({ level: 'success', text: isEdit ? 'Rule updated.' : 'Rule created.' })
      navigate('/features/ai-session-analyzer')
      return
    }
    showSnackbar({
      level: 'error',
      text: error?.response?.data?.message || 'Failed to save the rule.',
    })
  }

  return (
    <Stack gap="xxlAlt">
      <Box>
        <Button
          variant="transparent"
          color="gray"
          leftSection={<ArrowLeft size={16} />}
          onClick={() => navigate('/features/ai-session-analyzer')}
          px={0}
          w="fit-content"
        >
          Back
        </Button>
      </Box>

      <SectionRow
        title="Set rule information"
        description="Used to identify this analysis across the fleet."
      >
        <Stack gap="md">
          <TextInput
            label="Name"
            placeholder="risky-writes"
            value={name}
            onChange={(e) => setName(e.currentTarget.value)}
            required
            disabled={isEdit}
            description={isEdit ? 'A rule is addressed by name, so it cannot be renamed.' : undefined}
            autoFocus={!isEdit}
          />
          <TextInput
            label="Description (Optional)"
            placeholder="Describe what this watches"
            value={description}
            onChange={(e) => setDescription(e.currentTarget.value)}
          />
        </Stack>
      </SectionRow>

      <SectionRow
        title="Distribute to listeners"
        description="One rule per listener, and the listener needs its analyzer switched on first."
      >
        <SidecarTargetPicker value={targets} onChange={handleTargets} />
      </SectionRow>

      <SectionRow
        title="What gets classified"
        description="This is the only check that leaves the process and costs money per statement. Narrow it."
        callout={
          <DocsBtnCallOut text="See our docs for triggers and costs" href={docsUrl.sidecar.riskAnalysis} variant="indigo" />
        }
      >
        <Stack gap="md">
          {isHTTP ? (
            <TagsInput
              label="Resources"
              placeholder="/orders/**"
              value={form.trigger_resources}
              onChange={(v) => set({ trigger_resources: v })}
            />
          ) : (
            <>
              <MultiSelect
                label="Operations"
                placeholder="Select operations..."
                data={operations}
                value={form.trigger_operations}
                onChange={(v) => set({ trigger_operations: v })}
                searchable
                clearable
              />
              <TagsInput
                label="Tables (optional)"
                placeholder="customers"
                value={form.trigger_tables}
                onChange={(v) => set({ trigger_tables: v })}
              />
            </>
          )}
          {noTrigger && (
            <Alert color="amber" variant="light" icon={<Info size={16} />} radius="md">
              With no trigger, every statement on this listener is sent to the model.
            </Alert>
          )}
          <NumberInput
            label="Call budget (optional)"
            placeholder="Inherit the sidecar’s"
            value={form.max_calls}
            onChange={(v) => set({ max_calls: v })}
            min={0}
          />
        </Stack>
      </SectionRow>

      <SectionRow
        title="What happens per risk level"
        description="A level you leave unset allows."
      >
        <Stack gap="md">
          {[
            ['high', 'High risk'],
            ['medium', 'Medium risk'],
            ['low', 'Low risk'],
          ].map(([level, label]) => (
            <Select
              key={level}
              label={label}
              data={actions}
              value={form[level]}
              onChange={(v) => set({ [level]: v ?? '' })}
              allowDeselect={false}
            />
          ))}
          {/* A stored rule can hold on listeners that can no longer do it: the
              protocol is edited in the sidecar's config file, not here, so
              nothing in this form was touched when it changed. Saving it back
              unchanged would hand that sidecar a configuration it refuses at
              its next start, taking every other lane with it. */}
          {holding && !holdable ? (
            <Alert color="red" variant="light" icon={<TriangleAlert size={16} />} radius="md">
              <Text size="sm">
                This rule holds for approval, and not every listener it names can hold a statement. The sidecar refuses
                this at startup. Pick database listeners, or set those levels to something else.
              </Text>
            </Alert>
          ) : holding ? (
            <Alert color="blue" variant="light" icon={<Info size={16} />} radius="md">
              <Stack gap={4}>
                <Text size="sm">
                  The first attempt is denied and a review is filed. The client is not held open: nothing waits on the
                  approval, and closing the session does not cancel it.
                </Text>
                <Text size="sm">
                  Running the same statement again after an approver releases it lets it through.
                </Text>
              </Stack>
            </Alert>
          ) : (
            <Text size="xs" c="dimmed">
              {holdable
                ? 'Hold for approval denies the first attempt and files a review. Running the same statement again after approval lets it through.'
                : 'Hold for approval needs a client that sends the statement again, so only a database listener can do it.'}
            </Text>
          )}
          <TextInput
            label="Denial message (optional)"
            placeholder="refused by risk analysis"
            value={form.message}
            onChange={(e) => set({ message: e.currentTarget.value })}
          />
        </Stack>
      </SectionRow>

      <SectionRow
        title="Custom analysis prompt"
        description="Replaces the default risk guidance for this listener."
      >
        <Textarea
          label="Your prompt (Optional)"
          placeholder="e.g. Treat any statement touching the payments schema as high risk."
          minRows={6}
          maxRows={12}
          value={form.prompt}
          onChange={(e) => set({ prompt: e.currentTarget.value })}
        />
      </SectionRow>

      <Group justify="flex-end">
        <Button onClick={handleSave} disabled={!canSubmit} loading={submitting}>
          Save
        </Button>
      </Group>
    </Stack>
  )
}

export default function ControlPlaneAiAnalyzerForm() {
  const { ruleName } = useParams()
  const isEdit = Boolean(ruleName)

  const active = useAiSessionAnalyzerStore((s) => s.active)
  const activeStatus = useAiSessionAnalyzerStore((s) => s.activeStatus)
  const fetchActive = useAiSessionAnalyzerStore((s) => s.fetchActive)
  const clearActive = useAiSessionAnalyzerStore((s) => s.clearActive)

  useEffect(() => {
    if (isEdit) fetchActive(ruleName)
    return () => clearActive()
  }, [isEdit, ruleName, fetchActive, clearActive])

  if (isEdit && (activeStatus === 'loading' || activeStatus === 'idle')) {
    return <PageLoader h={400} />
  }
  if (isEdit && activeStatus === 'error') {
    return <PageLoader error h={400} message="Failed to load rule." />
  }

  return (
    <FormFields
      key={isEdit ? (active?.name ?? ruleName) : 'new'}
      rule={isEdit ? active : null}
      ruleName={ruleName}
      isEdit={isEdit}
    />
  )
}

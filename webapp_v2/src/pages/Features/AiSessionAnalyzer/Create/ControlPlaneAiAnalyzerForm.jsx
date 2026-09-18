import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Box, Group, Stack, Text } from '@mantine/core'
import { ArrowLeft, Info } from 'lucide-react'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
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
import { showSnackbar } from '@/utils/snackbar'
import { ANALYZER_ACTIONS, operationsFor } from '@/pages/sidecarRuleVocabulary'
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

function formToSpec(f) {
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
  return spec
}

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

  const protocol = useMemo(() => {
    const byId = new Map(sidecars.map((sc) => [sc.id, sc]))
    const first = targets[0]
    if (!first) return ''
    const lane = byId.get(first.sidecar_id)?.configuration?.listeners?.find(
      (l) => l.name === first.listener_name,
    )
    return lane?.protocol ?? ''
  }, [targets, sidecars])

  const isHTTP = protocol.toLowerCase() === 'http'
  const operations = useMemo(() => operationsFor(protocol), [protocol])
  const noTrigger =
    form.trigger_operations.length === 0 &&
    form.trigger_tables.length === 0 &&
    form.trigger_resources.length === 0

  const canSubmit = name.trim() !== '' && !submitting

  const handleSave = async () => {
    if (!canSubmit) return
    const spec = formToSpec(form)
    if (!spec.high && !spec.medium && !spec.low) {
      showSnackbar({
        level: 'error',
        text: 'Name an action for at least one risk level, or every verdict allows while still paying for the classification.',
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
        description="A listener runs ONE analyzer block, and it needs its analyzer enabled first — the provider, the model and the credential live on the sidecar and cannot be set from here."
      >
        <SidecarTargetPicker value={targets} onChange={setTargets} />
      </SectionRow>

      <SectionRow
        title="What gets classified"
        description="The analyzer is the only evaluator that leaves the process, costs money per statement and can take a second. Narrow it."
      >
        <Stack gap="md">
          {isHTTP ? (
            <TagsInput
              label="Resources"
              description="Only requests matching these paths are sent. A trailing /** matches any deeper path."
              placeholder="/orders/**"
              value={form.trigger_resources}
              onChange={(v) => set({ trigger_resources: v })}
            />
          ) : (
            <>
              <MultiSelect
                label="Operations"
                description="Reads the statement’s most consequential effect, so a data-modifying CTE triggers on the delete it performs."
                placeholder="Select operations..."
                data={operations}
                value={form.trigger_operations}
                onChange={(v) => set({ trigger_operations: v })}
                searchable
                clearable
              />
              <TagsInput
                label="Tables (optional)"
                description="Comes from a scanner rather than a full SQL grammar: a statement whose relations it could not determine does NOT match. Trigger on operations for anything load-bearing."
                placeholder="customers"
                value={form.trigger_tables}
                onChange={(v) => set({ trigger_tables: v })}
              />
            </>
          )}
          {noTrigger && (
            <Alert color="amber" variant="light" icon={<Info size={16} />} radius="md">
              With no trigger this listener classifies EVERY statement: a model call per statement
              shape, bounded only by the cache and the call budget.
            </Alert>
          )}
          <NumberInput
            label="Call budget (optional)"
            description="A process-lifetime backstop for this listener. Past it, statements fall through to the guardrails, the same outcome as a listener with no analyzer."
            placeholder="Inherit the sidecar’s"
            value={form.max_calls}
            onChange={(v) => set({ max_calls: v })}
            min={0}
          />
        </Stack>
      </SectionRow>

      <SectionRow
        title="What happens per risk level"
        description="A level you do not name allows, so you opt into blocking a tier by writing it down."
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
              data={ANALYZER_ACTIONS}
              value={form[level]}
              onChange={(v) => set({ [level]: v ?? '' })}
              allowDeselect={false}
            />
          ))}
          <TextInput
            label="Denial message (optional)"
            description="Reaches the user on a block. Empty falls back to the model’s own title."
            placeholder="refused by risk analysis"
            value={form.message}
            onChange={(e) => set({ message: e.currentTarget.value })}
          />
        </Stack>
      </SectionRow>

      <SectionRow
        title="Custom analysis prompt"
        description="Replaces the inherited risk guidance for this listener. The output contract is appended after it and cannot be removed."
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

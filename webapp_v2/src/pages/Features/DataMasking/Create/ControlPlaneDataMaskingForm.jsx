import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Box, Group, Paper, Stack, Text } from '@mantine/core'
import { ArrowLeft, Plus, Trash2 } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import Button from '@/components/Button'
import DocsBtnCallOut from '@/components/DocsBtnCallOut'
import MultiSelect from '@/components/MultiSelect'
import NumberInput from '@/components/NumberInput'
import PageLoader from '@/components/PageLoader'
import SectionRow from '@/components/SectionRow'
import Select from '@/components/Select'
import SidecarTargetPicker from '@/components/SidecarTargetPicker'
import TagsInput from '@/components/TagsInput'
import TextInput from '@/components/TextInput'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { docsUrl } from '@/utils/docsUrl'
import { showSnackbar } from '@/utils/snackbar'
import {
  ENTITY_TYPES,
  MASK_STRATEGIES,
  SSH_ONLY_STRATEGY,
} from '@/pages/sidecarRuleVocabulary'
import { useDataMaskingStore } from '../store'

// Masking as a SIDECAR runs it, which the docs call out as a different
// implementation from the gateway's: this one decodes the response frame in
// memory and rewrites values, with no DLP provider and no resource roles.
//
// The sidecar's sibling of Create/GatewayDataMaskingForm. Neither imports the
// other; Router.jsx picks one with <ByProduct>.
//
// Two ways to name what gets masked, and they fail in opposite directions.
// An ENTITY rule masks by detection: it reaches anywhere a value appears,
// including inside an opaque HTTP body, and misses whatever the detector does
// not recognize. A COLUMN rule masks by position: it cannot miss, and only
// works where the protocol names its values.

const emptyRule = () => ({
  key: Math.random().toString(36).slice(2),
  name: '',
  match: 'entities',
  entities: [],
  columns: [],
  strategy: 'redact',
  keep_last: 4,
  mask_char: '',
})

function specToRules(spec) {
  const rules = spec?.rules ?? []
  if (rules.length === 0) return [emptyRule()]
  return rules.map((r) => ({
    ...emptyRule(),
    ...r,
    match: (r.columns ?? []).length > 0 ? 'columns' : 'entities',
    strategy: r.strategy || 'redact',
    keep_last: r.keep_last ?? 4,
    mask_char: r.mask_char ? String.fromCodePoint(r.mask_char) : '',
    key: Math.random().toString(36).slice(2),
  }))
}

function rulesToSpec(rules, sshBound) {
  const out = rules
    .filter((r) => r.name.trim() !== '')
    .map((r) => {
      // Clamped here rather than held in state. An ssh lane accepts one
      // strategy, and which lanes are bound changes as the picker changes, so
      // deriving it at save time keeps the two from drifting apart.
      const strategy = sshBound ? SSH_ONLY_STRATEGY : r.strategy
      const rule = { name: r.name.trim(), strategy }
      if (r.match === 'columns') rule.columns = r.columns
      else rule.entities = r.entities
      // Only where the strategy reads it. A keep_last on a hash rule is a
      // field the daemon declares but never looks at, and a form that writes
      // it teaches the reader the wrong thing.
      if (strategy === 'partial') rule.keep_last = Number(r.keep_last)
      if ((strategy === 'mask' || strategy === 'partial') && r.mask_char !== '') {
        // The daemon takes a Go rune, which is the code point as a number.
        rule.mask_char = r.mask_char.codePointAt(0)
      }
      return rule
    })
  return { rules: out }
}

function RuleEditor({ rule, index, onChange, onRemove, removable, strategies, sshBound }) {
  const set = (patch) => onChange({ ...rule, ...patch })
  // What this rule would actually be saved as, which on an ssh lane is the
  // one length-preserving strategy whatever the row holds.
  const value = sshBound ? SSH_ONLY_STRATEGY : rule.strategy
  const strategy = strategies.find((s) => s.value === value)

  return (
    <Paper p="md" radius="md" withBorder>
      <Stack gap="md">
        <Group justify="space-between" align="center">
          <Text size="xs" fw={600} c="dimmed" tt="uppercase">
            {`Rule ${index + 1}`}
          </Text>
          {removable && (
            <ActionIcon variant="subtle" color="gray" onClick={onRemove} aria-label="Remove rule">
              <Trash2 size={16} />
            </ActionIcon>
          )}
        </Group>

        <Group align="flex-end" gap="sm" wrap="nowrap">
          <TextInput
            label="Rule name"
            placeholder="mask-customer-pii"
            value={rule.name}
            onChange={(e) => set({ name: e.currentTarget.value })}
            required
            flex={1}
          />
          <Select
            label="Match by"
            data={[
              { value: 'entities', label: 'Detected entities' },
              { value: 'columns', label: 'Result-set columns' },
            ]}
            value={rule.match}
            onChange={(v) => set({ match: v ?? 'entities' })}
            allowDeselect={false}
            w={220}
          />
        </Group>

        {rule.match === 'entities' ? (
          <MultiSelect
            label="Entity types"
            placeholder="Select entity types..."
            data={ENTITY_TYPES}
            value={rule.entities}
            onChange={(v) => set({ entities: v })}
            searchable
            clearable
          />
        ) : (
          <TagsInput
            label="Columns"
            placeholder="ssn"
            value={rule.columns}
            onChange={(v) => set({ columns: v })}
          />
        )}

        <Group align="flex-end" gap="sm" wrap="nowrap">
          <Select
            label="Strategy"
            data={strategies.map((s) => ({ value: s.value, label: s.label }))}
            value={value}
            onChange={(v) => set({ strategy: v ?? 'redact' })}
            allowDeselect={false}
            description={strategy?.help}
            flex={1}
          />
          {value === 'partial' && (
            <NumberInput
              label="Keep last"
              value={rule.keep_last}
              onChange={(v) => set({ keep_last: v })}
              min={0}
              w={140}
            />
          )}
          {(value === 'mask' || value === 'partial') && (
            <TextInput
              label="Mask character"
              placeholder="*"
              value={rule.mask_char}
              onChange={(e) => set({ mask_char: e.currentTarget.value.slice(0, 1) })}
              w={160}
            />
          )}
        </Group>

        {sshBound && (
          <Text size="sm" c="dimmed">
            An SSH listener masks bytes in place, so Mask is its only strategy.
          </Text>
        )}
      </Stack>
    </Paper>
  )
}

function FormFields({ rule: stored, id, isEdit }) {
  const navigate = useNavigate()
  const submitting = useDataMaskingStore((s) => s.submitting)
  const createRule = useDataMaskingStore((s) => s.createRule)
  const updateRule = useDataMaskingStore((s) => s.updateRule)
  const sidecars = useSidecarStore((s) => s.sidecars)

  const [name, setName] = useState(stored?.name ?? '')
  const [description, setDescription] = useState(stored?.description ?? '')
  const [targets, setTargets] = useState(stored?.sidecar_targets ?? [])
  const [rules, setRules] = useState(() => specToRules(stored?.sidecar_spec))

  // An ssh lane masks a byte stream IN PLACE: a replacement of a different
  // size shifts every byte after it, which desynchronizes a terminal and
  // corrupts a download. Length preservation is the safety property, so the
  // strategy list collapses to the one that has it.
  const sshBound = useMemo(() => {
    const byId = new Map(sidecars.map((sc) => [sc.id, sc]))
    return targets.some((t) => {
      const lane = byId.get(t.sidecar_id)?.configuration?.listeners?.find(
        (l) => l.name === t.listener_name,
      )
      return (lane?.protocol ?? '').toLowerCase() === 'ssh'
    })
  }, [targets, sidecars])

  const strategies = sshBound
    ? MASK_STRATEGIES.filter((s) => s.value === SSH_ONLY_STRATEGY)
    : MASK_STRATEGIES

  const canSubmit = name.trim() !== '' && !submitting

  const handleSave = async () => {
    if (!canSubmit) return
    const spec = rulesToSpec(rules, sshBound)
    if (spec.rules.length === 0) {
      showSnackbar({ level: 'error', text: 'Give every rule a name, or remove it.' })
      return
    }
    const payload = {
      name: name.trim(),
      description,
      // The gateway's own fields stay empty: this rule reaches a sidecar by
      // naming its listeners, not a connection, and a sidecar has no custom
      // recognizer to register.
      connection_ids: [],
      attributes: [],
      supported_entity_types: [],
      custom_entity_types: [],
      sidecar_spec: spec,
      sidecar_targets: targets,
    }
    const { ok, error } = isEdit ? await updateRule(id, payload) : await createRule(payload)
    if (ok) {
      showSnackbar({ level: 'success', text: isEdit ? 'Rule updated.' : 'Rule created.' })
      navigate('/features/data-masking')
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
          onClick={() => navigate('/features/data-masking')}
          px={0}
          w="fit-content"
        >
          Back
        </Button>
      </Box>

      <SectionRow
        title="Set rule information"
        description="Used to identify this masking rule across the fleet."
      >
        <Stack gap="md">
          <TextInput
            label="Name"
            placeholder="mask-customer-pii"
            value={name}
            onChange={(e) => setName(e.currentTarget.value)}
            required
            autoFocus
          />
          <TextInput
            label="Description (Optional)"
            placeholder="Describe what this protects"
            value={description}
            onChange={(e) => setDescription(e.currentTarget.value)}
          />
        </Stack>
      </SectionRow>

      <SectionRow
        title="Distribute to listeners"
        description="A listener's mask rules replace the sidecar defaults rather than adding to them."
      >
        <SidecarTargetPicker value={targets} onChange={setTargets} />
      </SectionRow>

      <SectionRow
        title="Configure rules"
        description="Responses only. A statement on its way in is never rewritten."
        callout={
          <DocsBtnCallOut text="Entities, columns and strategies" href={docsUrl.sidecar.dataMasking} variant="indigo" />
        }
      >
        <Stack gap="md">
          {rules.map((rule, i) => (
            <RuleEditor
              key={rule.key}
              rule={rule}
              index={i}
              strategies={strategies}
              sshBound={sshBound}
              removable={rules.length > 1}
              onChange={(next) => setRules(rules.map((r, j) => (j === i ? next : r)))}
              onRemove={() => setRules(rules.filter((_, j) => j !== i))}
            />
          ))}
          <Button
            variant="light"
            leftSection={<Plus size={16} />}
            onClick={() => setRules([...rules, emptyRule()])}
            w="fit-content"
          >
            Add rule
          </Button>
        </Stack>
      </SectionRow>

      <Group justify="flex-end">
        <Button onClick={handleSave} disabled={!canSubmit} loading={submitting}>
          Save
        </Button>
      </Group>
    </Stack>
  )
}

export default function ControlPlaneDataMaskingForm() {
  const { id } = useParams()
  const isEdit = Boolean(id)

  const active = useDataMaskingStore((s) => s.active)
  const activeStatus = useDataMaskingStore((s) => s.activeStatus)
  const fetchActive = useDataMaskingStore((s) => s.fetchActive)
  const clearActive = useDataMaskingStore((s) => s.clearActive)

  useEffect(() => {
    if (isEdit) fetchActive(id)
    return () => clearActive()
  }, [isEdit, id, fetchActive, clearActive])

  if (isEdit && (activeStatus === 'loading' || activeStatus === 'idle')) {
    return <PageLoader h={400} />
  }
  if (isEdit && activeStatus === 'error') {
    return <Text c="red">Failed to load data masking rule.</Text>
  }

  return (
    <FormFields
      key={isEdit ? (active?.id ?? id) : 'new'}
      rule={isEdit ? active : null}
      id={id}
      isEdit={isEdit}
    />
  )
}

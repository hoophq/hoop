import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Box, Group, Paper, Stack, Text } from '@mantine/core'
import { ArrowLeft, Plus, Trash2 } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import DocsBtnCallOut from '@/components/DocsBtnCallOut'
import Button from '@/components/Button'
import MultiSelect from '@/components/MultiSelect'
import PageLoader from '@/components/PageLoader'
import SectionRow from '@/components/SectionRow'
import Select from '@/components/Select'
import SidecarTargetPicker from '@/components/SidecarTargetPicker'
import Switch from '@/components/Switch'
import TagsInput from '@/components/TagsInput'
import TextInput from '@/components/TextInput'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { docsUrl } from '@/utils/docsUrl'
import { showSnackbar } from '@/utils/snackbar'
import {
  ENTITY_TYPES,
  GUARDRAIL_ACTIONS,
  operationsFor,
  ruleTypesFor,
} from '@/pages/sidecarRuleVocabulary'
import { useGuardrailsStore } from '../store'

// Guardrails as a SIDECAR runs them, which is a different rule engine from the
// gateway's and shares only two of its seven rule types.
//
// The sidecar's sibling of Create/GatewayGuardrailForm. Neither imports the
// other and neither knows the product: Router.jsx picks one with <ByProduct>.
//
// What is absent is as deliberate as what is here. There are no resource roles
// and no attributes: a control plane has no connections, and a rule reaches a
// sidecar by naming its listeners. There is no output side: a sidecar denies
// requests and masks responses, so a response-side control is a masking rule.

const emptyRule = () => ({
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

// Only the fields the chosen type reads reach the payload. A sidecar decodes
// its configuration strictly, and the gateway refuses a rule carrying a field
// its type does not declare — so dropping them here is what lets one form row
// hold every type.
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

function RuleEditor({ rule, index, onChange, onRemove, removable, types, operations }) {
  const type = types.find((t) => t.value === rule.type) ?? types[0]
  const set = (patch) => onChange({ ...rule, ...patch })
  const has = (field) => type?.fields.includes(field)

  return (
    <Paper p="md" radius="md" withBorder>
      <Stack gap="md">
        {/* The number is not decoration. Rules evaluate in order and the first
            denial wins, so which card is second is a fact about the policy. */}
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
            placeholder="no-destructive-sql"
            value={rule.name}
            onChange={(e) => set({ name: e.currentTarget.value })}
            required
            flex={1}
          />
          <Select
            label="Type"
            data={types.map((t) => ({ value: t.value, label: t.label }))}
            value={rule.type}
            onChange={(v) => set({ type: v ?? 'operation' })}
            allowDeselect={false}
            w={200}
          />
        </Group>

        {has('operations') && (
          <MultiSelect
            label="Operations"
            placeholder="Select operations..."
            data={operations}
            value={rule.operations}
            onChange={(v) => set({ operations: v })}
            searchable
            clearable
          />
        )}
        {has('tables') && (
          <Group align="flex-end" gap="sm" wrap="nowrap">
            <TagsInput
              label="Tables"
              placeholder="customers"
              value={rule.tables}
              onChange={(v) => set({ tables: v })}
              flex={1}
            />
            <Select
              label="Access"
              data={[
                { value: '', label: 'Read or write' },
                { value: 'read', label: 'Read only' },
                { value: 'write', label: 'Write only' },
              ]}
              value={rule.access}
              onChange={(v) => set({ access: v ?? '' })}
              allowDeselect={false}
              w={180}
            />
          </Group>
        )}
        {has('require_table_match') && (
          <Switch
            checked={rule.require_table_match}
            onChange={(e) => set({ require_table_match: e.currentTarget.checked })}
            label="Also deny statements whose tables could not be determined"
          />
        )}
        {has('words') && (
          <TagsInput
            label="Words"
            placeholder="pg_sleep"
            value={rule.words}
            onChange={(v) => set({ words: v })}
          />
        )}
        {has('pattern_regex') && (
          <TextInput
            label="Pattern"
            placeholder="(?i)^\\s*delete\\s+from\\s+\\w+\\s*;?\\s*$"
            value={rule.pattern_regex}
            onChange={(e) => set({ pattern_regex: e.currentTarget.value })}
          />
        )}
        {has('entities') && (
          <MultiSelect
            label="Entity types"
            placeholder="Select entity types..."
            data={ENTITY_TYPES}
            value={rule.entities}
            onChange={(v) => set({ entities: v })}
            searchable
            clearable
          />
        )}
        {has('resources') && (
          <TagsInput
            label="Resources"
            placeholder="/admin/**"
            value={rule.resources}
            onChange={(v) => set({ resources: v })}
          />
        )}
        {has('statuses') && (
          <TagsInput
            label="Statuses"
            // The two status types spell a status differently, and the sidecar
            // refuses the wrong spelling at startup: HTTP takes a code or a
            // class, gRPC takes one of its sixteen names or codes.
            placeholder={rule.type === 'grpc_status' ? 'permission_denied' : '5xx'}
            value={rule.statuses}
            onChange={(v) => set({ statuses: v })}
          />
        )}
        {has('methods') && (
          <MultiSelect
            label="Methods (optional)"
            placeholder="Any method"
            data={['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS']}
            value={rule.methods}
            onChange={(v) => set({ methods: v })}
            clearable
          />
        )}

        {rule.type !== 'operation' && (
          <MultiSelect
            label="Only for these operations (optional)"
            placeholder="Every operation"
            data={operations}
            value={rule.operations}
            onChange={(v) => set({ operations: v })}
            searchable
            clearable
          />
        )}

        <Group align="flex-end" gap="sm" wrap="nowrap">
          <TextInput
            label="Denial message"
            placeholder="destructive statements are not permitted on this lane"
            value={rule.message}
            onChange={(e) => set({ message: e.currentTarget.value })}
            flex={1}
          />
          <Select
            label="On a match"
            data={GUARDRAIL_ACTIONS}
            value={rule.action}
            onChange={(v) => set({ action: v ?? '' })}
            allowDeselect={false}
            w={280}
          />
        </Group>
      </Stack>
    </Paper>
  )
}

function FormFields({ guardrail, id, isEdit }) {
  const navigate = useNavigate()
  const submitting = useGuardrailsStore((s) => s.submitting)
  const createGuardrail = useGuardrailsStore((s) => s.createGuardrail)
  const updateGuardrail = useGuardrailsStore((s) => s.updateGuardrail)
  const sidecars = useSidecarStore((s) => s.sidecars)

  const [name, setName] = useState(guardrail?.name ?? '')
  const [description, setDescription] = useState(guardrail?.description ?? '')
  const [targets, setTargets] = useState(guardrail?.sidecar_targets ?? [])
  const [rules, setRules] = useState(() => specToRules(guardrail?.sidecar_spec))

  // Which protocols the bound listeners speak. A sidecar REFUSES a rule its
  // lane cannot read — at startup, taking that sidecar's whole configuration
  // with it — so the type list narrows to what every bound lane accepts
  // instead of letting the save fail later.
  const protocols = useMemo(() => {
    const byId = new Map(sidecars.map((sc) => [sc.id, sc]))
    return targets.map((t) => {
      const lane = byId.get(t.sidecar_id)?.configuration?.listeners?.find(
        (l) => l.name === t.listener_name,
      )
      return lane?.protocol ?? ''
    })
  }, [targets, sidecars])

  const types = useMemo(() => ruleTypesFor(protocols), [protocols])
  const operations = useMemo(() => operationsFor(protocols[0]), [protocols])
  const typeFields = (t) => types.find((x) => x.value === t)?.fields ?? []

  const canSubmit = name.trim() !== '' && !submitting

  const handleSave = async () => {
    if (!canSubmit) return
    const spec = rulesToSpec(rules, typeFields)
    if (spec.rules.length === 0) {
      showSnackbar({ level: 'error', text: 'Give every rule a name, or remove it.' })
      return
    }
    const payload = {
      id: isEdit ? id : '',
      name: name.trim(),
      description,
      // The gateway's own fields stay empty: a control plane has no
      // connections and no attributes to bind a rule to.
      connection_ids: [],
      attributes: [],
      input: { rules: [] },
      output: { rules: [] },
      sidecar_spec: spec,
      sidecar_targets: targets,
    }
    const { ok, error } = isEdit
      ? await updateGuardrail(id, payload)
      : await createGuardrail(payload)
    if (ok) {
      showSnackbar({ level: 'success', text: isEdit ? 'Guardrail updated.' : 'Guardrail created.' })
      navigate('/guardrails')
      return
    }
    showSnackbar({
      level: 'error',
      text: error?.response?.data?.message || 'Failed to save the guardrail.',
    })
  }

  return (
    <Stack gap="xxlAlt">
      <Box>
        <Button
          variant="transparent"
          color="gray"
          leftSection={<ArrowLeft size={16} />}
          onClick={() => navigate('/guardrails')}
          px={0}
          w="fit-content"
        >
          Back
        </Button>
      </Box>

      <SectionRow
        title="Set Guardrail information"
        description="Used to identify this guardrail across the fleet."
      >
        <Stack gap="md">
          <TextInput
            label="Name"
            placeholder="no-destructive-sql"
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
        description="Pick these first: the rule types below narrow to what every listener you choose can run."
      >
        <SidecarTargetPicker value={targets} onChange={setTargets} />
      </SectionRow>

      <SectionRow
        title="Configure rules"
        description="Evaluated in order. The first rule that denies wins."
        callout={
          <DocsBtnCallOut text="See our docs for every rule type" href={docsUrl.sidecar.policyRules} variant="indigo" />
        }
      >
        <Stack gap="md">
          {rules.map((rule, i) => (
            <RuleEditor
              key={rule.key}
              rule={rule}
              index={i}
              types={types}
              operations={operations}
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

export default function ControlPlaneGuardrailForm() {
  const { id } = useParams()
  const isEdit = Boolean(id)

  const active = useGuardrailsStore((s) => s.active)
  const activeStatus = useGuardrailsStore((s) => s.activeStatus)
  const fetchActive = useGuardrailsStore((s) => s.fetchActive)
  const clearActive = useGuardrailsStore((s) => s.clearActive)

  useEffect(() => {
    if (isEdit) fetchActive(id)
    return () => clearActive()
  }, [isEdit, id, fetchActive, clearActive])

  if (isEdit && (activeStatus === 'loading' || activeStatus === 'idle')) {
    return <PageLoader h={400} />
  }
  // A failed fetch leaves `active` null; rendering the form would present a
  // blank "edit" whose save overwrites the real guardrail with empty rules.
  if (isEdit && activeStatus === 'error') {
    return <Text c="red">Failed to load guardrail.</Text>
  }

  return (
    <FormFields
      key={isEdit ? (active?.id ?? id) : 'new'}
      guardrail={isEdit ? active : null}
      id={id}
      isEdit={isEdit}
    />
  )
}

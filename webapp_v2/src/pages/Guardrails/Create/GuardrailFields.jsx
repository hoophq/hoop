import { useEffect } from 'react'
import { Group, Paper, Stack, Text } from '@mantine/core'
import { Info, Plus, Trash2 } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import ConnectionsMultiSelect from '@/components/ConnectionsMultiSelect'
import DocsBtnCallOut from '@/components/DocsBtnCallOut'
import MultiSelect from '@/components/MultiSelect'
import { TaggedLabel, TaggedOption } from '@/components/RuleSupportTag'
import SectionRow from '@/components/SectionRow'
import Select from '@/components/Select'
import SidecarTargetPicker from '@/components/SidecarTargetPicker'
import Switch from '@/components/Switch'
import TagsInput from '@/components/TagsInput'
import TextInput from '@/components/TextInput'
import { useUserStore } from '@/stores/useUserStore'
import { docsUrl } from '@/utils/docsUrl'
import { AGENT_GUARDRAIL_TYPES, AGENT_HALF_DIFFERS, agentSkipsMessage } from '@/utils/ruleSupport'
import { ENTITY_TYPES, GUARDRAIL_ACTIONS } from '@/pages/sidecarRuleVocabulary'
import { useGuardrailsStore } from '../store'
import { AGENT_DLP_REQUIRED } from '../helpers'
import { emptyRule } from './useGuardrailEditor'

// One form for agents and sidecars. The sidecar vocabulary is the superset;
// an option one engine does not run carries a RuleSupportTag.

// Only the agent checks results, with its two engines; every other type is
// a statement rule, so Results stays visible but cannot be picked.
const directionsFor = (type) => [
  { value: 'input', label: 'Statements (requests)' },
  { value: 'output', label: 'Results (responses)', disabled: !AGENT_GUARDRAIL_TYPES.includes(type) },
]

const TypeOption = ({ option }) => (
  <TaggedOption option={option} tagged={!AGENT_GUARDRAIL_TYPES.includes(option.value)} side="sidecar" />
)
const DirectionOption = ({ option }) => <TaggedOption option={option} tagged={option.value === 'output'} side="agent" />
const ActionOption = ({ option }) => <TaggedOption option={option} tagged={option.value === 'defer'} side="sidecar" />

function RuleEditor({ rule, index, onChange, onRemove, removable, types, operations }) {
  const type = types.find((t) => t.value === rule.type) ?? types[0]
  const set = (patch) => onChange({ ...rule, ...patch })
  const has = (field) => type?.fields.includes(field)
  const output = rule.direction === 'output'

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
            onChange={(v) => {
              const next = v ?? 'operation'
              const patch = { type: next }
              // Only the agent checks results, with its two engines.
              if (!AGENT_GUARDRAIL_TYPES.includes(next)) patch.direction = 'input'
              // `operations` is the match on an operation rule and a scope on
              // every other, so it does not survive the switch between them.
              if ((rule.type === 'operation') !== (next === 'operation')) patch.operations = []
              set(patch)
            }}
            renderOption={TypeOption}
            allowDeselect={false}
            w={200}
          />
          <Select
            label="Checks"
            data={directionsFor(rule.type)}
            value={rule.direction}
            onChange={(v) => set({ direction: v ?? 'input' })}
            renderOption={DirectionOption}
            allowDeselect={false}
            w={290}
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
            description="Case-insensitive on sidecars, case-sensitive on agents."
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
            // HTTP takes a code or a class, gRPC one of its status names.
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

        {rule.type !== 'operation' && !output && (
          <MultiSelect
            label={<TaggedLabel text="Only for these operations (optional)" side="sidecar" />}
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
          {!output && (
            <Select
              label="On a match"
              data={GUARDRAIL_ACTIONS}
              value={rule.action}
              onChange={(v) => set({ action: v ?? '' })}
              renderOption={ActionOption}
              allowDeselect={false}
              w={280}
            />
          )}
        </Group>
      </Stack>
    </Paper>
  )
}

export default function GuardrailFields({ editor }) {
  const {
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
    agentHalfDiffers,
  } = editor
  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  // Agents enforce guardrails through a DLP provider; sidecars need none.
  const hasRedactCredentials = useUserStore((s) => s.hasRedactCredentials)
  const attributeOptions = useGuardrailsStore((s) => s.attributes).map((a) => a.name)
  const fetchAttributes = useGuardrailsStore((s) => s.fetchAttributes)
  // The sidecar dialog renders these fields too, with no page around it to load the options.
  useEffect(() => {
    fetchAttributes()
  }, [fetchAttributes])

  return (
    <Stack gap="xxlAlt">
      <SectionRow title="Set Guardrail information" description="Used to identify this guardrail across the fleet.">
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
        title="Associate Resource Roles"
        description={
          hasRedactCredentials
            ? 'Agent traffic. Select the resource roles where this guardrail applies.'
            : AGENT_DLP_REQUIRED
        }
      >
        <ConnectionsMultiSelect
          value={connectionIds}
          onChange={setConnectionIds}
          disabled={!hasRedactCredentials}
        />
      </SectionRow>

      <SectionRow
        title="Attribute configuration"
        description={
          hasRedactCredentials
            ? 'Agent traffic. Select which attributes apply this guardrail.'
            : AGENT_DLP_REQUIRED
        }
      >
        <MultiSelect
          label="Attributes"
          placeholder="Select attributes..."
          data={attributeOptions}
          value={attributes}
          onChange={setAttributes}
          disabled={!hasRedactCredentials}
          searchable
          clearable
        />
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
          {agentHalfDiffers && (
            <Alert color="amber" variant="light" icon={<Info size={16} />}>
              {AGENT_HALF_DIFFERS.guardrail}
            </Alert>
          )}
          {agentBound && agentBlocked.length > 0 && (
            <Alert color="amber" variant="light" icon={<Info size={16} />}>
              {agentSkipsMessage(
                'guardrail',
                agentBlocked.map((r) => rules.indexOf(r)),
              )}
            </Alert>
          )}
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
            disabled={isFreeLicense && rules.length >= 1}
            w="fit-content"
          >
            Add rule
          </Button>
        </Stack>
      </SectionRow>
    </Stack>
  )
}

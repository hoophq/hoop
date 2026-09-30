import { Group, Paper, Stack, Text } from '@mantine/core'
import { Plus, Trash2 } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import Button from '@/components/Button'
import DocsBtnCallOut from '@/components/DocsBtnCallOut'
import MultiSelect from '@/components/MultiSelect'
import SectionRow from '@/components/SectionRow'
import Select from '@/components/Select'
import SidecarTargetPicker from '@/components/SidecarTargetPicker'
import Switch from '@/components/Switch'
import TagsInput from '@/components/TagsInput'
import TextInput from '@/components/TextInput'
import { docsUrl } from '@/utils/docsUrl'
import { ENTITY_TYPES, GUARDRAIL_ACTIONS } from '@/pages/sidecarRuleVocabulary'
import { emptyRule } from './useSidecarGuardrailEditor'

// Guardrails as a SIDECAR runs them, which is a different rule engine from the
// gateway's and shares only two of its seven rule types.
//
// What is absent is as deliberate as what is here. There are no resource roles
// and no attributes: a control plane has no connections, and a rule reaches a
// sidecar by naming its listeners. There is no output side: a sidecar denies
// requests and masks responses, so a response-side control is a masking rule.

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
          <TagsInput label="Words" placeholder="pg_sleep" value={rule.words} onChange={(v) => set({ words: v })} />
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

/** The fields of a sidecar guardrail, driven by useSidecarGuardrailEditor. */
export default function SidecarGuardrailFields({ editor }) {
  const { name, setName, description, setDescription, targets, setTargets, rules, setRules, types, operations } =
    editor

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
    </Stack>
  )
}

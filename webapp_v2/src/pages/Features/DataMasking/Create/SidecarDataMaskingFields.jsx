import { Group, Paper, Stack, Text } from '@mantine/core'
import { Plus, Trash2 } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import Button from '@/components/Button'
import DocsBtnCallOut from '@/components/DocsBtnCallOut'
import MultiSelect from '@/components/MultiSelect'
import NumberInput from '@/components/NumberInput'
import SectionRow from '@/components/SectionRow'
import Select from '@/components/Select'
import SidecarTargetPicker from '@/components/SidecarTargetPicker'
import TagsInput from '@/components/TagsInput'
import TextInput from '@/components/TextInput'
import { docsUrl } from '@/utils/docsUrl'
import { ENTITY_TYPES, SSH_ONLY_STRATEGY, maskPreview } from '@/pages/sidecarRuleVocabulary'
import { emptyRule } from './useSidecarDataMaskingEditor'

// Masking as a SIDECAR runs it, which the docs call out as a different
// implementation from the gateway's: this one decodes the response frame in
// memory and rewrites values, with no DLP provider and no resource roles.
//
// Two ways to name what gets masked, and they fail in opposite directions.
// An ENTITY rule masks by detection: it reaches anywhere a value appears,
// including inside an opaque HTTP body, and misses whatever the detector does
// not recognize. A COLUMN rule masks by position: it cannot miss, and only
// works where the protocol names its values.

function RuleEditor({ rule, index, onChange, onRemove, removable, strategies, sshBound }) {
  const set = (patch) => onChange({ ...rule, ...patch })
  // What this rule would actually be saved as, which on an ssh lane is the
  // one length-preserving strategy whatever the row holds.
  const value = sshBound ? SSH_ONLY_STRATEGY : rule.strategy
  // The example rewrites as the row does, so the mask character and the tail
  // length are visible in their result rather than described.
  const preview = maskPreview(value, { maskChar: rule.mask_char, keepLast: rule.keep_last })

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
          <TagsInput label="Columns" placeholder="ssn" value={rule.columns} onChange={(v) => set({ columns: v })} />
        )}

        {/* Aligned at the TOP, unlike the rows above: the Strategy field
            carries its example under the input, so a bottom alignment would
            line the two inputs beside it up with that text instead of with
            the select. */}
        <Group align="flex-start" gap="sm" wrap="nowrap">
          <Select
            label="Strategy"
            data={strategies.map((s) => ({ value: s.value, label: s.label }))}
            value={value}
            onChange={(v) => set({ strategy: v ?? 'redact' })}
            allowDeselect={false}
            // Under the input, not under the label: it is the result of the
            // whole row, including the two fields to its right.
            description={preview}
            inputWrapperOrder={['label', 'input', 'description', 'error']}
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

/** The fields of a sidecar masking rule, driven by useSidecarDataMaskingEditor. */
export default function SidecarDataMaskingFields({ editor }) {
  const { name, setName, description, setDescription, targets, setTargets, rules, setRules, strategies, sshBound } =
    editor

  return (
    <Stack gap="xxlAlt">
      <SectionRow title="Set rule information" description="Used to identify this masking rule across the fleet.">
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
          <DocsBtnCallOut
            text="See our docs for every masking option"
            href={docsUrl.sidecar.dataMasking}
            variant="indigo"
          />
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
    </Stack>
  )
}

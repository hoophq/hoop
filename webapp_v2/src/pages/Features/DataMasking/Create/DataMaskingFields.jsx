import { Group, Paper, Stack, Text } from '@mantine/core'
import { Info, Plus, Trash2 } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import ConnectionsMultiSelect from '@/components/ConnectionsMultiSelect'
import DocsBtnCallOut from '@/components/DocsBtnCallOut'
import MultiSelect from '@/components/MultiSelect'
import NumberInput from '@/components/NumberInput'
import RuleSupportTag from '@/components/RuleSupportTag'
import SectionRow from '@/components/SectionRow'
import Select from '@/components/Select'
import SidecarTargetPicker from '@/components/SidecarTargetPicker'
import TagsInput from '@/components/TagsInput'
import TextInput from '@/components/TextInput'
import { useUserStore } from '@/stores/useUserStore'
import { docsUrl } from '@/utils/docsUrl'
import { agentRunsNothingMessage } from '@/utils/ruleSupport'
import { SSH_ONLY_STRATEGY, maskPreview } from '@/pages/sidecarRuleVocabulary'
import { useDataMaskingStore } from '../store'
import {
  ENTITY_OPTIONS,
  PRESET_OPTIONS,
  RULE_DRIVEN_PROVIDERS,
  getPresetValues,
  normalizeEntityName,
  presetFor,
} from '../helpers'
import { emptyCustomEntity, emptyRule } from './useDataMaskingEditor'

// One form for agents and sidecars. An agent masks through its DLP provider
// by entity type; a sidecar masks the decoded response itself, by entity or
// by column name, with a strategy. An option one engine does not run carries
// a RuleSupportTag.

const AGENT_PROVIDER_REQUIRED =
  'Needs a rule-driven DLP provider (Microsoft Presidio or alcatraz). Sidecars mask without one.'

const MATCHES = [
  { value: 'entities', label: 'Detected entities' },
  { value: 'columns', label: 'Result-set columns' },
]

function MatchOption({ option }) {
  return (
    <Group gap="xs" justify="space-between" w="100%" wrap="nowrap">
      {option.label}
      {option.value === 'columns' && <RuleSupportTag side="sidecar" />}
    </Group>
  )
}

function RuleEditor({ rule, index, onChange, onRemove, removable, strategies, sshBound, maxEntities }) {
  const set = (patch) => onChange({ ...rule, ...patch })
  const value = sshBound ? SSH_ONLY_STRATEGY : rule.strategy
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
            data={MATCHES}
            value={rule.match}
            onChange={(v) => set({ match: v ?? 'entities' })}
            renderOption={MatchOption}
            allowDeselect={false}
            w={220}
          />
        </Group>

        {rule.match === 'entities' ? (
          <>
            {/* A quick-fill of the entity list; edited afterwards, it reads empty again. */}
            <Select
              label="Preset (optional)"
              placeholder="Fill the entity types from a preset"
              data={PRESET_OPTIONS}
              value={presetFor(rule.entities)}
              onChange={(k) => k && set({ entities: getPresetValues(k) })}
              allowDeselect={false}
            />
            <MultiSelect
              label="Entity types"
              placeholder="Select entity types..."
              data={ENTITY_OPTIONS}
              value={rule.entities}
              onChange={(v) => set({ entities: v })}
              maxValues={maxEntities}
              searchable
              clearable
            />
          </>
        ) : (
          <TagsInput label="Columns" placeholder="ssn" value={rule.columns} onChange={(v) => set({ columns: v })} />
        )}

        {/* Top-aligned: the Strategy hint sits under its input. */}
        <Group align="flex-start" gap="sm" wrap="nowrap">
          <Select
            label={
              <Group gap="xs" component="span">
                Strategy <RuleSupportTag side="sidecar" />
              </Group>
            }
            data={strategies.map((s) => ({ value: s.value, label: s.label }))}
            value={value}
            onChange={(v) => set({ strategy: v ?? 'redact' })}
            allowDeselect={false}
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
        <Text size="sm" c="dimmed">
          Agents mask with their DLP provider; strategy, keep-last and mask character apply to sidecars.
        </Text>

        {sshBound && (
          <Text size="sm" c="dimmed">
            An SSH listener masks bytes in place, so Mask is its only strategy.
          </Text>
        )}
      </Stack>
    </Paper>
  )
}

function CustomEntityRow({ entity, onChange, onRemove }) {
  const set = (patch) => onChange({ ...entity, ...patch })
  return (
    <Group align="flex-end" gap="sm" wrap="nowrap">
      <TextInput
        label="Name"
        placeholder="ORDER_ID"
        value={entity.name}
        onChange={(e) => set({ name: e.currentTarget.value })}
        onBlur={() => set({ name: normalizeEntityName(entity.name) })}
        w={220}
      />
      <TextInput
        label="Regex"
        placeholder="\b[A-Z]{2}[0-9]{3}\b"
        value={entity.regex}
        onChange={(e) => set({ regex: e.currentTarget.value })}
        flex={1}
      />
      <ActionIcon variant="subtle" color="gray" onClick={onRemove} aria-label="Remove custom entity" mb={4}>
        <Trash2 size={16} />
      </ActionIcon>
    </Group>
  )
}

export default function DataMaskingFields({ editor }) {
  const {
    isEdit,
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
    customEntities,
    setCustomEntities,
    scoreThreshold,
    setScoreThreshold,
    strategies,
    sshBound,
    agentBlocked,
    agentBound,
  } = editor
  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  const redactProvider = useUserStore((s) => s.redactProvider)
  const agentReady = RULE_DRIVEN_PROVIDERS.includes(redactProvider)
  const attributeOptions = useDataMaskingStore((s) => s.attributes).map((a) => a.name)

  return (
    <Stack gap="xxlAlt">
      <SectionRow title="Set rule information" description="Used to identify this masking rule across the fleet.">
        <Stack gap="md">
          {/* The API keeps the stored name on update but binds under the sent one. */}
          <TextInput
            label="Name"
            placeholder="mask-customer-pii"
            value={name}
            onChange={(e) => setName(e.currentTarget.value)}
            required
            disabled={isEdit}
            description={isEdit ? 'The name is fixed after creation.' : undefined}
            autoFocus={!isEdit}
          />
          <TextInput
            label="Description (Optional)"
            placeholder="Describe what this protects"
            value={description}
            onChange={(e) => setDescription(e.currentTarget.value)}
          />
          <TextInput
            label={
              <Group gap="xs" component="span">
                Analyzer confidence threshold (Optional) <RuleSupportTag side="agent" />
              </Group>
            }
            type="number"
            min={1}
            max={100}
            placeholder="85"
            value={scoreThreshold}
            onChange={(e) => {
              const v = e.currentTarget.value
              setScoreThreshold(v === '' ? '' : Number(v))
            }}
            description="Minimum confidence level (1-100) a detection needs to be masked. Defaults to 85% for new rules. Leave empty to mask every detection regardless of confidence. Custom entity types with a score below this value are never masked."
          />
        </Stack>
      </SectionRow>

      <SectionRow
        title="Resource Role configuration"
        description={
          agentReady ? 'Agent traffic. Select the resource roles where this rule applies.' : AGENT_PROVIDER_REQUIRED
        }
      >
        <ConnectionsMultiSelect value={connectionIds} onChange={setConnectionIds} disabled={!agentReady} />
      </SectionRow>

      <SectionRow
        title="Attribute configuration"
        description={
          agentReady ? 'Agent traffic. Select which attributes apply this rule.' : AGENT_PROVIDER_REQUIRED
        }
      >
        <MultiSelect
          label="Attributes"
          placeholder="Select attributes..."
          data={attributeOptions}
          value={attributes}
          onChange={setAttributes}
          disabled={!agentReady}
          searchable
          clearable
        />
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
          {agentBound && agentBlocked.length > 0 && (
            <Alert color="amber" variant="light" icon={<Info size={16} />}>
              {agentRunsNothingMessage(
                'datamasking',
                agentBlocked.map((r) => rules.indexOf(r)),
              )}
            </Alert>
          )}
          {rules.map((rule, i) => (
            <RuleEditor
              key={rule.key}
              rule={rule}
              index={i}
              strategies={strategies}
              sshBound={sshBound}
              maxEntities={isFreeLicense ? 1 : undefined}
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

      <SectionRow
        title="Custom entity types"
        badge={<RuleSupportTag side="agent" />}
        description="A regex the agent's DLP provider detects as its own entity. Sidecars have no custom recognizer."
      >
        <Stack gap="md">
          {customEntities.map((entity, i) => (
            <CustomEntityRow
              key={entity.key}
              entity={entity}
              onChange={(next) => setCustomEntities(customEntities.map((c, j) => (j === i ? next : c)))}
              onRemove={() => setCustomEntities(customEntities.filter((_, j) => j !== i))}
            />
          ))}
          <Button
            variant="light"
            leftSection={<Plus size={16} />}
            onClick={() => setCustomEntities([...customEntities, emptyCustomEntity()])}
            w="fit-content"
          >
            Add custom entity
          </Button>
        </Stack>
      </SectionRow>
    </Stack>
  )
}

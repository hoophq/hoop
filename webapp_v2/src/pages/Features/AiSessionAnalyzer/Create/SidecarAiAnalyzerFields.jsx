import { Divider, Group, Stack, Text } from '@mantine/core'
import { Info } from 'lucide-react'
import Alert from '@/components/Alert'
import DocsBtnCallOut from '@/components/DocsBtnCallOut'
import MultiSelect from '@/components/MultiSelect'
import NumberInput from '@/components/NumberInput'
import SectionRow from '@/components/SectionRow'
import Select from '@/components/Select'
import SidecarTargetPicker from '@/components/SidecarTargetPicker'
import TagsInput from '@/components/TagsInput'
import Textarea from '@/components/Textarea'
import TextInput from '@/components/TextInput'
import { docsUrl } from '@/utils/docsUrl'
import { analyzerBlocked } from '@/pages/Sidecars/config'
import { REVIEW_MODE_HOLD, REVIEW_MODES, isHoldAction } from '@/pages/sidecarRuleVocabulary'
import OptionalLabel from '../components/OptionalLabel'
import LevelApprover from './sections/LevelApprover'
import TriggerItemsInput from './sections/TriggerItemsInput'

// Provider, model and credential are not here: they live in the sidecar's own
// analyzer section, a file path the control plane cannot supply.
const listenerBlocked = (sidecar) => analyzerBlocked(sidecar.configuration)

const LEVELS = [
  {
    level: 'low',
    title: 'Low risk',
    description:
      'Activities that appear unlikely to cause significant system, data, or security impact based on intent and structure.',
  },
  {
    level: 'medium',
    title: 'Medium risk',
    description: 'Activities that may modify data, configuration, or runtime behavior in a scoped or limited way.',
  },
  {
    level: 'high',
    title: 'High risk',
    description:
      'Activities that suggest potentially destructive, irreversible, privilege-altering, or security-sensitive behavior.',
  },
]

// An action and, in grey, what it does.
const renderAction = ({ option }) => (
  <Group gap="xs" wrap="nowrap" py={4}>
    <Text size="sm">{option.label}</Text>
    <Text size="sm" c="dimmed">
      {option.description}
    </Text>
  </Group>
)

export default function SidecarAiAnalyzerFields({ editor }) {
  const {
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
  } = editor

  return (
    <Stack gap="xxlAlt">
      <SectionRow title="Set rule information" description="Used to identify this analysis across the fleet.">
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
            label={<OptionalLabel>Description</OptionalLabel>}
            placeholder="Describe what this watches"
            value={description}
            onChange={(e) => setDescription(e.currentTarget.value)}
          />
        </Stack>
      </SectionRow>

      <SectionRow
        title="Distribute to listeners"
        description="One rule per listener. The sidecar needs its analyzer section (provider and model) in its config."
      >
        <SidecarTargetPicker value={targets} onChange={setTargets} listenerBlocked={listenerBlocked} />
      </SectionRow>

      <SectionRow
        title="What gets classified"
        description="This is the only check that leaves the process and costs money per statement. Narrow it."
        callout={
          <DocsBtnCallOut
            text="See our docs for triggers and costs"
            href={docsUrl.sidecar.riskAnalysis}
            variant="indigo"
          />
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
                label={<OptionalLabel>Tables</OptionalLabel>}
                placeholder="customers"
                value={form.trigger_tables}
                onChange={(v) => set({ trigger_tables: v })}
              />
            </>
          )}
          <TriggerItemsInput
            label={<OptionalLabel>Combined conditions</OptionalLabel>}
            description="Classify a statement that matches every field of one condition."
            addLabel="Add condition"
            value={form.trigger_any}
            onChange={(v) => set({ trigger_any: v })}
            operations={operations}
            isHTTP={isHTTP}
          />
          <TriggerItemsInput
            label={<OptionalLabel>Exclusions</OptionalLabel>}
            description="Never classify a statement that matches every field of one exclusion."
            addLabel="Add exclusion"
            value={form.trigger_exclude}
            onChange={(v) => set({ trigger_exclude: v })}
            operations={operations}
            isHTTP={isHTTP}
          />
          {noTrigger && (
            <Alert color="amber" variant="light" icon={<Info size={16} />} radius="md">
              {form.trigger_exclude.length > 0
                ? 'With no trigger, every statement on this listener except the excluded ones is sent to the model.'
                : 'With no trigger, every statement on this listener is sent to the model.'}
            </Alert>
          )}
          <NumberInput
            label={<OptionalLabel>Call budget</OptionalLabel>}
            placeholder="Inherit the sidecar’s"
            value={form.max_calls}
            onChange={(v) => set({ max_calls: v })}
            min={0}
          />
        </Stack>
      </SectionRow>

      <Divider />

      <Stack gap="xl">
        {LEVELS.map(({ level, title, description }) => (
          <SectionRow key={level} title={title} description={description}>
            <Stack gap="md">
              {/* An unset level allows: it shows the placeholder, and clearing returns to it. */}
              <Select
                label="Action"
                placeholder="Select action"
                data={actions}
                renderOption={renderAction}
                value={form[level] || null}
                onChange={(v) => setLevelAction(level, v ?? '')}
                clearable
              />
              {isHoldAction(form[level]) && (
                <LevelApprover
                  approval={form.approval[level]}
                  approvalRule={form.approval_rule}
                  groupOptions={groupOptionsFor(level)}
                  onApprover={(approver) => setApprover(level, approver)}
                  onChange={(patch) => setApproval(level, patch)}
                />
              )}
            </Stack>
          </SectionRow>
        ))}
      </Stack>

      {waits && (
        <SectionRow
          title="While held for approval"
          description="A person has up to 30 minutes to decide, unless a time limit ends it sooner. A client that times out first ends the wait; running the statement again after approval lets it through. On an SSH listener, drop the shell capability first."
        >
          <Stack gap="md">
            <Select
              label="Mode"
              description="Return denies the statement at once with the approval id; after approval, the client must send the identical statement again. A client can also ask for either mode itself."
              data={REVIEW_MODES}
              value={form.review_mode || REVIEW_MODE_HOLD}
              onChange={(v) => set({ review_mode: !v || v === REVIEW_MODE_HOLD ? '' : v })}
              allowDeselect={false}
            />
            {humanHold && (
              <>
                <NumberInput
                  label={<OptionalLabel>Time to decide (minutes)</OptionalLabel>}
                  description="An approval request nobody decides in this time expires; the statement is not released."
                  placeholder="No limit"
                  min={1}
                  max={10080}
                  decimalScale={2}
                  value={pendingMinutes}
                  onChange={editPendingMinutes}
                />
                <NumberInput
                  label={<OptionalLabel>Time to use an approval (minutes)</OptionalLabel>}
                  description="Counts from the approval. After it, running the statement again files a new approval request."
                  placeholder="No limit"
                  min={1}
                  max={10080}
                  decimalScale={2}
                  value={approvalMinutes}
                  onChange={editApprovalMinutes}
                />
              </>
            )}
          </Stack>
        </SectionRow>
      )}

      <Divider />

      <Stack gap="xl">
        <SectionRow
          title="Denial message"
          description="Shown to the user when a statement is blocked or rejected. Applies to all risk levels."
        >
          <TextInput
            label={<OptionalLabel>Message</OptionalLabel>}
            placeholder="e.g. Refused by risk analysis."
            value={form.message}
            onChange={(e) => set({ message: e.currentTarget.value })}
          />
        </SectionRow>

        <SectionRow
          title="Custom analysis prompt"
          description="Replaces the default risk guidance for this listener. It sets the risk level, not the approval."
        >
          <Textarea
            label={<OptionalLabel>Analysis prompt</OptionalLabel>}
            placeholder="e.g. Treat any statement on the payments schema as high risk."
            minRows={3}
            maxRows={12}
            value={form.prompt}
            onChange={(e) => set({ prompt: e.currentTarget.value })}
          />
        </SectionRow>
      </Stack>
    </Stack>
  )
}

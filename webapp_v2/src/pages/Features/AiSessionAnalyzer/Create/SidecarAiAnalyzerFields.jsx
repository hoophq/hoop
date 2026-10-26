import { Box, Group, Input, Paper, Stack, Text } from '@mantine/core'
import { Info } from 'lucide-react'
import Alert from '@/components/Alert'
import Checkbox from '@/components/Checkbox'
import DocsBtnCallOut from '@/components/DocsBtnCallOut'
import MultiSelect from '@/components/MultiSelect'
import NumberInput from '@/components/NumberInput'
import SectionRow from '@/components/SectionRow'
import SegmentedControl from '@/components/SegmentedControl'
import Select from '@/components/Select'
import SidecarTargetPicker from '@/components/SidecarTargetPicker'
import TagsInput from '@/components/TagsInput'
import Textarea from '@/components/Textarea'
import TextInput from '@/components/TextInput'
import { docsUrl } from '@/utils/docsUrl'
import { analyzerBlocked } from '@/pages/Sidecars/config'
import {
  APPROVER_AI,
  APPROVER_HUMAN,
  APPROVERS,
  HANDOFF_CHOICES,
  REVIEW_ACTION,
  REVIEW_MODE_HOLD,
  REVIEW_MODES,
  isHandoffAction,
  isHoldAction,
} from '@/pages/sidecarRuleVocabulary'
import { APPROVER_RULE } from './useSidecarAiAnalyzerEditor'
import TriggerItemsInput from './sections/TriggerItemsInput'

// Provider, model and credential are not here: they live in the sidecar's own
// analyzer section, a file path the control plane cannot supply.
const listenerBlocked = (sidecar) => analyzerBlocked(sidecar.configuration)

const LEVELS = [
  ['high', 'High risk'],
  ['medium', 'Medium risk'],
  ['low', 'Low risk'],
]

// Who approves one level's hold: an AI agent with an optional prompt, or the
// members of some groups. A bare hold from an imported file names its rule.
function LevelApprover({ approval, approvalRule, groupOptions, onChange }) {
  if (approval.approver === APPROVER_RULE) {
    return (
      <Text size="sm" c="dimmed">
        {`The approval rule ${approvalRule} decides who approves.`}
      </Text>
    )
  }
  return (
    <Stack gap="sm">
      <Input.Wrapper label="Approver">
        <Box mt={4}>
          <SegmentedControl
            w="fit-content"
            data={APPROVERS}
            value={approval.approver}
            onChange={(v) => onChange({ approver: v })}
          />
        </Box>
      </Input.Wrapper>
      {approval.approver === APPROVER_AI && (
        <Textarea
          label="Prompt (optional)"
          placeholder="Instructions for the approval agent. Empty uses the default prompt."
          minRows={3}
          maxRows={8}
          value={approval.prompt}
          onChange={(e) => onChange({ prompt: e.currentTarget.value })}
        />
      )}
      {approval.approver === APPROVER_HUMAN && (
        <MultiSelect
          label="Approval groups"
          description="Groups whose members may approve. Empty leaves it to the administrators."
          placeholder="Select groups"
          searchable
          nothingFoundMessage="No user groups defined yet."
          data={groupOptions}
          value={approval.groups}
          onChange={(v) => onChange({ groups: v })}
        />
      )}
    </Stack>
  )
}

// A hand-off level: the agent's prompt and the actions it may pick. A checked
// "Hold for approval" shows who approves it.
function LevelHandoff({ handoff, approval, groupOptions, onPrompt, onAllowed, onApproval }) {
  const last = handoff.allowed.length === 1
  return (
    <Stack gap="sm">
      <Textarea
        label="Prompt (optional)"
        placeholder="Instructions for the hand-off agent. Empty uses the default prompt."
        minRows={3}
        maxRows={8}
        value={handoff.prompt}
        onChange={(e) => onPrompt(e.currentTarget.value)}
      />
      <Input.Wrapper
        label="Allowed actions"
        description="The agent picks one of these. When it does not decide, the most restrictive one applies."
      >
        <Group gap="lg" mt="xs">
          {HANDOFF_CHOICES.map(({ value, label }) => {
            const checked = handoff.allowed.includes(value)
            return (
              <Checkbox
                key={value}
                label={label}
                checked={checked}
                disabled={checked && last}
                onChange={(e) => onAllowed(value, e.currentTarget.checked)}
              />
            )
          })}
        </Group>
      </Input.Wrapper>
      {handoff.allowed.includes(REVIEW_ACTION) && (
        <Paper withBorder p="sm" radius="md">
          <LevelApprover approval={approval} groupOptions={groupOptions} onChange={onApproval} />
        </Paper>
      )}
    </Stack>
  )
}

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
    setHandoffPrompt,
    setAllowed,
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
            label="Description (Optional)"
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
                label="Tables (optional)"
                placeholder="customers"
                value={form.trigger_tables}
                onChange={(v) => set({ trigger_tables: v })}
              />
            </>
          )}
          <TriggerItemsInput
            label="Combined conditions (optional)"
            description="Classify a statement that matches every field of one condition."
            addLabel="Add condition"
            value={form.trigger_any}
            onChange={(v) => set({ trigger_any: v })}
            operations={operations}
            isHTTP={isHTTP}
          />
          <TriggerItemsInput
            label="Exclusions (optional)"
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
        description="A level you leave unset allows. Hold for approval asks the AI agent at once, or waits up to 30 minutes for a person to decide, unless a time limit ends it sooner; a client that times out first ends the wait, and running it again after approval lets it through. Hand off to AI Agent lets an agent pick one of the allowed actions for each statement. On an SSH listener, drop the shell capability first."
      >
        <Stack gap="md">
          {LEVELS.map(([level, label]) => (
            <Stack key={level} gap="sm">
              <Select
                label={label}
                data={actions}
                value={form[level]}
                onChange={(v) => setLevelAction(level, v ?? '')}
                allowDeselect={false}
              />
              {isHoldAction(form[level]) && (
                <Paper withBorder p="sm" radius="md">
                  <LevelApprover
                    approval={form.approval[level]}
                    approvalRule={form.approval_rule}
                    groupOptions={groupOptionsFor(level)}
                    onChange={(patch) => setApproval(level, patch)}
                  />
                </Paper>
              )}
              {isHandoffAction(form[level]) && (
                <Paper withBorder p="sm" radius="md">
                  <LevelHandoff
                    handoff={form.handoff[level]}
                    approval={form.approval[level]}
                    groupOptions={groupOptionsFor(level)}
                    onPrompt={(prompt) => setHandoffPrompt(level, prompt)}
                    onAllowed={(value, checked) => setAllowed(level, value, checked)}
                    onApproval={(patch) => setApproval(level, patch)}
                  />
                </Paper>
              )}
            </Stack>
          ))}
          {waits && (
            <Select
              label="While held for approval"
              description="Return denies the statement at once with the approval id; after approval, the client must send the identical statement again. A client can also ask for either mode itself."
              data={REVIEW_MODES}
              value={form.review_mode || REVIEW_MODE_HOLD}
              onChange={(v) => set({ review_mode: !v || v === REVIEW_MODE_HOLD ? '' : v })}
              allowDeselect={false}
            />
          )}
          {humanHold && (
            <>
              <NumberInput
                label="Time to decide (minutes, optional)"
                description="An approval request nobody decides in this time expires; the statement is not released."
                placeholder="No limit"
                min={1}
                max={10080}
                decimalScale={2}
                value={pendingMinutes}
                onChange={editPendingMinutes}
              />
              <NumberInput
                label="Time to use an approval (minutes, optional)"
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
          <TextInput
            label="Denial message (optional)"
            placeholder="refused by risk analysis"
            value={form.message}
            onChange={(e) => set({ message: e.currentTarget.value })}
          />
        </Stack>
      </SectionRow>

      <SectionRow title="Custom analysis prompt" description="Replaces the default risk guidance for this listener.">
        <Textarea
          label="Your prompt (Optional)"
          placeholder="e.g. Treat any statement touching the payments schema as high risk."
          minRows={6}
          maxRows={12}
          value={form.prompt}
          onChange={(e) => set({ prompt: e.currentTarget.value })}
        />
      </SectionRow>
    </Stack>
  )
}

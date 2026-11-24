import { Divider, Group, Stack, Text, Title } from '@mantine/core'
import NumberInput from '@/components/NumberInput'
import SectionRow from '@/components/SectionRow'
import Select from '@/components/Select'
import SidecarTargetPicker from '@/components/SidecarTargetPicker'
import Textarea from '@/components/Textarea'
import TextInput from '@/components/TextInput'
import { analyzerBlocked, laneFeatureBlocked } from '@/pages/Sidecars/config'
import {
  REVIEW_MODE_HOLD,
  REVIEW_MODES,
  appliesAIRules,
  isHandoffAction,
  isHoldAction,
} from '@/pages/sidecarRuleVocabulary'
import AIPolicyCard from '../components/AIPolicyCard'
import CardStack from '../components/CardStack'
import OptionalLabel from '../components/OptionalLabel'
import LevelApprover from './sections/LevelApprover'
import LevelHandoff from './sections/LevelHandoff'
import { aiPolicyConfigured } from './useSidecarAiAnalyzerEditor'

// Provider, model and credential are not here: they live in the sidecar's own
// analyzer section, a file path the control plane cannot supply.
const listenerBlocked = (sidecar, listener) =>
  analyzerBlocked(sidecar.configuration) ?? laneFeatureBlocked(listener.protocol, 'ai-analyzer')

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

// The form's section headings carry the design's weight.
const TITLE_FW = 600

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
    setApprover,
    groupOptionsFor,
    groupsStatus,
    ruleLibraryStatus,
    aiRuleOptionsFor,
    setAIFeature,
    toggleAIRule,
    pendingMinutes,
    editPendingMinutes,
    approvalMinutes,
    editApprovalMinutes,
    waits,
    humanHold,
    actions,
  } = editor

  // The AI policy settings card of a level whose action applies AI rules:
  // Allow, Warn, Hold for approval and Hand off (not Block, not Defer, and
  // not an unset level, which shows no card). A CardStack entry, so the
  // level's stack opens one card at a time.
  const policyCard = (level) => {
    const action = form[level]
    if (action === '' || !appliesAIRules(action)) return null
    let helper
    if (isHoldAction(action)) helper = 'Rules apply before the approval.'
    else if (isHandoffAction(action)) helper = 'Rules apply when the agent does not block.'
    return {
      key: 'policy',
      configured: aiPolicyConfigured(form.ai[level]),
      render: (card) => (
        <AIPolicyCard
          ai={form.ai[level]}
          status={ruleLibraryStatus}
          optionsFor={(key) => aiRuleOptionsFor(level, key)}
          onFeature={(key, patch) => setAIFeature(level, key, patch)}
          onToggleRule={(key, name) => toggleAIRule(level, key, name)}
          helper={helper}
          card={card}
        />
      ),
    }
  }
  // A level that neither holds nor hands off shows the policy card alone.
  const policyOnly = (level) => {
    const policy = policyCard(level)
    return policy ? <CardStack cards={[policy]} /> : null
  }

  return (
    <Stack gap="xxlAlt">
      <SectionRow
        title="Set rule information"
        titleFw={TITLE_FW}
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
            label={<OptionalLabel>Description</OptionalLabel>}
            placeholder="Describe what this watches"
            value={description}
            onChange={(e) => setDescription(e.currentTarget.value)}
          />
        </Stack>
      </SectionRow>

      <SectionRow
        title="Distribute to listeners"
        titleFw={TITLE_FW}
        description="One rule per listener. The sidecar needs its analyzer section (provider and model) in its config."
      >
        <SidecarTargetPicker value={targets} onChange={setTargets} listenerBlocked={listenerBlocked} />
      </SectionRow>

      <Divider />

      <Stack gap="xl">
        <Stack gap="xs">
          <Title order={4} fw={TITLE_FW}>
            Risk evaluation
          </Title>
          <Text size="sm" c="dimmed">
            Select an action for each risk level. A level with no action allows. Hold for approval waits up to 30
            minutes. For each held level, select an AI agent or a human to approve.
          </Text>
        </Stack>
        {LEVELS.map(({ level, title, description }) => (
          <SectionRow key={level} title={title} titleFw={TITLE_FW} description={description}>
            <Stack gap="md">
              {/* An unset level allows: it shows the placeholder, and picking the set action again returns to it. */}
              <Select
                label="Action"
                placeholder="Select action"
                data={actions}
                renderOption={renderAction}
                value={form[level] || null}
                onChange={(v) => setLevelAction(level, v ?? '')}
                maxDropdownHeight={280}
              />
              {isHoldAction(form[level]) && (
                <LevelApprover
                  policy={policyCard(level)}
                  approval={form.approval[level]}
                  approvalRule={form.approval_rule}
                  groupOptions={groupOptionsFor(level)}
                  groupsLoaded={groupsStatus === 'success'}
                  onApprover={(approver) => setApprover(level, approver)}
                  onChange={(patch) => setApproval(level, patch)}
                />
              )}
              {isHandoffAction(form[level]) && (
                <LevelHandoff
                  handoff={form.handoff[level]}
                  approval={form.approval[level]}
                  approvalRule={form.approval_rule}
                  groupOptions={groupOptionsFor(level)}
                  groupsLoaded={groupsStatus === 'success'}
                  policy={policyCard(level)}
                  onPrompt={(prompt) => setHandoffPrompt(level, prompt)}
                  onAllowed={(value, checked) => setAllowed(level, value, checked)}
                  onApprover={(approver) => setApprover(level, approver)}
                  onApproval={(patch) => setApproval(level, patch)}
                />
              )}
              {!isHoldAction(form[level]) && !isHandoffAction(form[level]) && policyOnly(level)}
            </Stack>
          </SectionRow>
        ))}
      </Stack>

      {waits && (
        <SectionRow
          title="While held for approval"
          titleFw={TITLE_FW}
          description="A person has up to 30 minutes to decide, 4 on an MCP listener, unless a time limit ends it sooner. A client that times out first ends the wait; running the statement again after approval lets it through. On an SSH listener, drop the shell capability first."
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
          titleFw={TITLE_FW}
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
          titleFw={TITLE_FW}
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

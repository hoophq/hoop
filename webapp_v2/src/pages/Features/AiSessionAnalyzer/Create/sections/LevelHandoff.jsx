import { Group, Stack, Text } from '@mantine/core'
import { Bot } from 'lucide-react'
import Checkbox from '@/components/Checkbox'
import { HANDOFF_CHOICES, HANDOFF_HOLD, NO_HOLD_ON_MCP } from '@/pages/sidecarRuleVocabulary'
import CardStack from '../../components/CardStack'
import PromptField from '../../components/PromptField'
import SettingsCard from '../../components/SettingsCard'
import { approverConfigured, handoffConfigured } from '../useSidecarAiAnalyzerEditor'
import { ApproverCard } from './LevelApprover'

// The actions the agent may pick, each with what it does beside it. The last
// checked one cannot be unchecked: an empty list blocks.
function AllowedActions({ allowed, onAllowed, mcpBound }) {
  const last = allowed.length === 1
  return (
    <Stack gap="sm">
      <Text size="sm" fw={600}>
        Allowed actions
      </Text>
      {HANDOFF_CHOICES.map(({ value, label, description }) => {
        const checked = allowed.includes(value)
        const noHold = mcpBound && value === HANDOFF_HOLD
        return (
          <Checkbox
            key={value}
            label={
              <Group gap="xs" wrap="nowrap">
                <Text size="sm" fw={500}>
                  {label}
                </Text>
                <Text size="xs" c="dimmed">
                  {description}
                </Text>
              </Group>
            }
            description={noHold && !checked ? NO_HOLD_ON_MCP : undefined}
            error={noHold && checked ? NO_HOLD_ON_MCP : undefined}
            checked={checked}
            disabled={(checked && last) || (noHold && !checked)}
            onChange={(e) => onAllowed(value, e.currentTarget.checked)}
          />
        )
      })}
    </Stack>
  )
}

// The card stack under a hand-off level: the AI policy settings card
// (`policy`), the Hand-off card (the agent's prompt and allowed actions) and,
// when "Hold for approval" is allowed, the Approver card.
export default function LevelHandoff({
  handoff,
  approval,
  approvalRule,
  groupOptions,
  policy,
  onPrompt,
  onAllowed,
  onApprover,
  onApproval,
  mcpBound,
}) {
  const holds = handoff.allowed.includes(HANDOFF_HOLD)
  const handoffCard = {
    key: 'handoff',
    configured: handoffConfigured(handoff),
    render: (card) => (
      <SettingsCard
        icon={Bot}
        title="Hand-off to AI agent"
        subtitle="Select the actions the AI agent can take on each statement."
        panel={false}
        {...card}
      >
        <Stack gap="lg">
          <PromptField
            prompt={handoff.prompt}
            onChange={onPrompt}
            placeholder="e.g. Block any write that has no WHERE clause."
            helper="The agent selects an action with these instructions. Empty uses the default prompt."
          />
          <AllowedActions allowed={handoff.allowed} onAllowed={onAllowed} mcpBound={mcpBound} />
        </Stack>
      </SettingsCard>
    ),
  }
  const cards = [policy, handoffCard]
  if (holds) {
    cards.push({
      key: 'approver',
      configured: approverConfigured(approval),
      render: (card) => (
        <ApproverCard
          card={card}
          approval={approval}
          approvalRule={approvalRule}
          groupOptions={groupOptions}
          onApprover={onApprover}
          onChange={onApproval}
        />
      ),
    })
  }
  return <CardStack cards={cards.filter(Boolean)} />
}

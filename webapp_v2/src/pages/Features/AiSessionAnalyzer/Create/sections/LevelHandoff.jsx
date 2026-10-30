import { Box, Paper, Stack, Text } from '@mantine/core'
import { Sparkles } from 'lucide-react'
import Checkbox from '@/components/Checkbox'
import { HANDOFF_CHOICES, HANDOFF_HOLD } from '@/pages/sidecarRuleVocabulary'
import PromptField from '../../components/PromptField'
import SettingsCard from '../../components/SettingsCard'
import { ApproverCard } from './LevelApprover'

// The actions the agent may pick. The last checked one cannot be unchecked:
// an empty list blocks.
function AllowedActions({ allowed, onAllowed }) {
  const last = allowed.length === 1
  return (
    <Stack gap="sm">
      <Stack gap={2}>
        <Text size="sm" fw={600}>
          Allowed actions
        </Text>
        <Text size="xs" c="dimmed">
          The agent selects one of these. When it does not decide, the most restrictive one applies.
        </Text>
      </Stack>
      {HANDOFF_CHOICES.map(({ value, label, description }) => {
        const checked = allowed.includes(value)
        return (
          <Checkbox
            key={value}
            label={
              <Text size="sm" fw={500}>
                {label}
              </Text>
            }
            description={description}
            checked={checked}
            disabled={checked && last}
            onChange={(e) => onAllowed(value, e.currentTarget.checked)}
          />
        )
      })}
    </Stack>
  )
}

// The card stack under a hand-off level: the Hand-off card (the agent's
// prompt and allowed actions) and, when "Hold for approval" is allowed, the
// Approver card.
export default function LevelHandoff({
  handoff,
  approval,
  approvalRule,
  groupOptions,
  onPrompt,
  onAllowed,
  onApprover,
  onApproval,
}) {
  const holds = handoff.allowed.includes(HANDOFF_HOLD)
  return (
    <Paper withBorder radius="lg" p="md">
      <SettingsCard
        icon={Sparkles}
        title="Hand-off"
        subtitle="Let an AI agent select the action for each statement in this risk level."
      >
        <Stack gap="lg">
          <PromptField
            prompt={handoff.prompt}
            onChange={onPrompt}
            placeholder="e.g. Block any write that has no WHERE clause."
            helper="The agent selects an action with these instructions. Empty uses the default prompt."
          />
          <AllowedActions allowed={handoff.allowed} onAllowed={onAllowed} />
        </Stack>
      </SettingsCard>
      {holds && (
        <>
          <Box ml={36} w={1} h={24} bg="gray.2" />
          <ApproverCard
            approval={approval}
            approvalRule={approvalRule}
            groupOptions={groupOptions}
            onApprover={onApprover}
            onChange={onApproval}
          />
        </>
      )}
    </Paper>
  )
}

import { Group, Paper, SimpleGrid, Stack, Text, UnstyledButton } from '@mantine/core'
import { Bot, FileCheck2, User } from 'lucide-react'
import Radio from '@/components/Radio'
import { APPROVER_AI, APPROVER_HUMAN, APPROVERS } from '@/pages/sidecarRuleVocabulary'
import CardConnector from '../../components/CardConnector'
import PromptField from '../../components/PromptField'
import SettingsCard from '../../components/SettingsCard'
import { APPROVER_RULE } from '../useSidecarAiAnalyzerEditor'
import ApprovalGroups from './ApprovalGroups'

const APPROVER_ICONS = { [APPROVER_AI]: Bot, [APPROVER_HUMAN]: User }

function ApproverOption({ value, label, description, selected, onSelect }) {
  const Icon = APPROVER_ICONS[value]
  return (
    <UnstyledButton
      role="radio"
      aria-checked={selected}
      onClick={onSelect}
      p="sm"
      bdrs="md"
      bd={`1px solid var(--mantine-color-${selected ? 'indigo-6' : 'gray-2'})`}
      bg={selected ? 'indigo.0' : 'var(--mantine-color-body)'}
    >
      <Group gap={10} wrap="nowrap">
        <Radio.Indicator checked={selected} size="xs" />
        <Icon size={14} aria-hidden="true" />
        <Text size="sm" fw={500}>
          {label}
        </Text>
      </Group>
      <Text size="xs" c="dimmed" pl={26} mt={2}>
        {description}
      </Text>
    </UnstyledButton>
  )
}

// Who approves one level's hold: an AI agent with an optional prompt, or the
// members of some groups. A bare hold from an imported file names its rule.
function WhoApproves({ approval, approvalRule, groupOptions, onApprover, onChange }) {
  if (approval.approver === APPROVER_RULE) {
    return (
      <Text size="sm" c="dimmed">
        {`The approval rule ${approvalRule} decides who approves.`}
      </Text>
    )
  }
  return (
    <Stack gap="md">
      <Text size="sm" fw={600}>
        Who approves
      </Text>
      <SimpleGrid cols={{ base: 1, xs: 2 }} spacing="sm" role="radiogroup" aria-label="Who approves">
        {APPROVERS.map((option) => (
          <ApproverOption
            key={option.value}
            {...option}
            selected={approval.approver === option.value}
            onSelect={() => onApprover(option.value)}
          />
        ))}
      </SimpleGrid>
      {approval.approver === APPROVER_AI && (
        <PromptField
          prompt={approval.prompt}
          onChange={(prompt) => onChange({ prompt })}
          placeholder="e.g. Approve only if the statement has a WHERE clause."
          helper="The agent approves or rejects with these instructions. Empty uses the default prompt."
        />
      )}
      {approval.approver === APPROVER_HUMAN && (
        <ApprovalGroups options={groupOptions} value={approval.groups} onChange={(groups) => onChange({ groups })} />
      )}
    </Stack>
  )
}

// The "Approver" card: who approves a held statement.
export function ApproverCard(props) {
  return (
    <SettingsCard icon={FileCheck2} title="Approver" subtitle="Select who approves requests in this risk level.">
      <WhoApproves {...props} />
    </SettingsCard>
  )
}

// The card stack under a level that holds for approval: the AI policy
// settings card (`policy`) first, since its rules apply before the approval
// (f20), then the Approver card.
export default function LevelApprover({ policy, ...props }) {
  return (
    <Paper withBorder radius="lg" p="md">
      {policy}
      <CardConnector />
      <ApproverCard {...props} />
    </Paper>
  )
}

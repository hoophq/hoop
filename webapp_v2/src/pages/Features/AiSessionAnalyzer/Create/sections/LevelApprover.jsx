import { useState } from 'react'
import { Box, Collapse, Divider, Group, Paper, SimpleGrid, Stack, Text, ThemeIcon, UnstyledButton } from '@mantine/core'
import { Bot, Check, ChevronDown, ChevronUp, FileCheck2, SquarePen, Undo2, User } from 'lucide-react'
import Button from '@/components/Button'
import Radio from '@/components/Radio'
import Textarea from '@/components/Textarea'
import { APPROVER_AI, APPROVER_HUMAN, APPROVERS } from '@/pages/sidecarRuleVocabulary'
import OptionalLabel from '../../components/OptionalLabel'
import { APPROVER_RULE } from '../useSidecarAiAnalyzerEditor'
import ApprovalGroups from './ApprovalGroups'
import classes from '../Create.module.css'

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

// The AI agent's instructions: the default prompt until the user writes one.
function AgentPrompt({ prompt, onChange }) {
  const [editing, setEditing] = useState(prompt !== '')
  const open = editing || prompt !== ''
  const useDefault = () => {
    onChange('')
    setEditing(false)
  }

  return (
    <Stack gap={6}>
      <Group justify="space-between" align="flex-start" wrap="nowrap">
        <Stack gap={2}>
          <Text size="sm">
            <OptionalLabel>Your prompt</OptionalLabel>
          </Text>
          {!open && (
            <Text size="xs" c="dimmed">
              Uses the default prompt.
            </Text>
          )}
        </Stack>
        {open ? (
          <Button variant="transparent" size="xs" px={0} leftSection={<Undo2 size={14} />} onClick={useDefault}>
            Use default
          </Button>
        ) : (
          <Button variant="transparent" size="xs" px={0} leftSection={<SquarePen size={14} />} onClick={() => setEditing(true)}>
            Edit prompt
          </Button>
        )}
      </Group>
      {open && (
        <Textarea
          aria-label="Your prompt"
          placeholder="e.g. Approve only if the statement has a WHERE clause."
          description="The agent approves or rejects with these instructions. Empty uses the default prompt."
          inputWrapperOrder={['label', 'input', 'description', 'error']}
          minRows={4}
          maxRows={10}
          autoFocus={editing && prompt === ''}
          value={prompt}
          onChange={(e) => onChange(e.currentTarget.value)}
        />
      )}
    </Stack>
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
        <AgentPrompt prompt={approval.prompt} onChange={(prompt) => onChange({ prompt })} />
      )}
      {approval.approver === APPROVER_HUMAN && (
        <ApprovalGroups options={groupOptions} value={approval.groups} onChange={(groups) => onChange({ groups })} />
      )}
    </Stack>
  )
}

// The "Approver" card under a level that holds for approval.
export default function LevelApprover(props) {
  const [open, setOpen] = useState(true)
  return (
    <Paper withBorder radius="lg" p="md">
      <Paper withBorder radius="lg" className={classes.clipped}>
        <UnstyledButton
          w="100%"
          p="md"
          bg={open ? 'green.0' : 'gray.0'}
          onClick={() => setOpen((o) => !o)}
          aria-expanded={open}
        >
          <Group wrap="nowrap" gap="md">
            <ThemeIcon size={40} radius="md" variant="light" color={open ? 'green' : 'gray'}>
              <FileCheck2 size={18} aria-hidden="true" />
            </ThemeIcon>
            <Stack gap={2} flex={1}>
              <Text size="sm" fw={600}>
                Approver
              </Text>
              <Text size="xs" c="dimmed">
                Select who approves requests in this risk level.
              </Text>
            </Stack>
            {open ? (
              <Box w={22} h={22} bdrs="xl" bg="var(--mantine-color-body)" opacity={0.7} />
            ) : (
              <ThemeIcon size={22} radius="xl" variant="light" color="green">
                <Check size={12} aria-hidden="true" />
              </ThemeIcon>
            )}
            {open ? <ChevronUp size={16} aria-hidden="true" /> : <ChevronDown size={16} aria-hidden="true" />}
          </Group>
        </UnstyledButton>
        <Collapse in={open}>
          <Divider />
          <Box p="md">
            <Paper withBorder radius="lg" p="lg">
              <WhoApproves {...props} />
            </Paper>
          </Box>
        </Collapse>
      </Paper>
    </Paper>
  )
}
